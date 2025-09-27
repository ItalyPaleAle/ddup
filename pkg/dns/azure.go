package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// AzureProvider implements the Provider interface for Azure DNS
type AzureProvider struct {
	name              string
	subscriptionID    string
	resourceGroupName string
	zoneName          string
	credential        azcore.TokenCredential
	metrics           *appmetrics.AppMetrics
	httpClient        *http.Client
}

// NewAzureProvider creates a new Azure DNS provider
func NewAzureProvider(name string, cfg *config.AzureConfig, metrics *appmetrics.AppMetrics) (*AzureProvider, error) {
	if cfg.SubscriptionID == "" {
		return nil, errors.New("subscription ID is required")
	}
	if cfg.ResourceGroupName == "" {
		return nil, errors.New("resource group name is required")
	}
	if cfg.ZoneName == "" {
		return nil, errors.New("zone name is required")
	}

	// Create the appropriate credential based on auth method
	var (
		credential azcore.TokenCredential
		err        error
	)
	clientOpts := azcore.ClientOptions{
		Telemetry: policy.TelemetryOptions{
			Disabled: true,
		},
	}

	// Otherwise, use the default credentials
	switch {
	case cfg.ClientID != "" && cfg.ClientSecret != "":
		// If client ID and secret are specified, use the service principal
		slog.Info("Authenticating to Azure with a service principal", slog.String("clientId", cfg.ClientID))
		credential, err = azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret, &azidentity.ClientSecretCredentialOptions{
			ClientOptions: clientOpts,
		})
		if err != nil {
			return nil, fmt.Errorf("error creating service principal credential: %w", err)
		}
	case cfg.ManagedIdentityClientID != "":
		// Use managed identity with a specific client ID (for user-assigned identities)
		slog.Info("Authenticating to Azure with a managed identity", slog.String("managedIdentityClientID", cfg.ManagedIdentityClientID))
		credential, err = azidentity.NewManagedIdentityCredential(&azidentity.ManagedIdentityCredentialOptions{
			ClientOptions: clientOpts,
			ID:            azidentity.ClientID(cfg.ManagedIdentityClientID),
		})
		if err != nil {
			return nil, fmt.Errorf("error creating service principal credential: %w", err)
		}
	default:
		// Use the default credentials
		slog.Info("Authenticating to Azure with the default options")
		credential, err = azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{
			ClientOptions: clientOpts,
			TenantID:      cfg.TenantID,
		})
		if err != nil {
			return nil, fmt.Errorf("error creating Default Azure credential: %w", err)
		}
	}

	return &AzureProvider{
		name:              name,
		subscriptionID:    cfg.SubscriptionID,
		resourceGroupName: cfg.ResourceGroupName,
		zoneName:          cfg.ZoneName,
		credential:        credential,
		metrics:           metrics,
		httpClient:        http.DefaultClient,
	}, nil
}

// Name returns the provider's name
func (a *AzureProvider) Name() string {
	return a.name
}

// UpdateRecords updates DNS records for the given domain with the provided IPs
func (a *AzureProvider) UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error {
	// First, get existing records
	currentIPs, err := a.getExistingIPs(ctx, domain)
	if err != nil {
		return fmt.Errorf("error getting existing records: %w", err)
	}

	// Get record name from domain
	recordName := a.getRecordName(domain)

	if len(ips) == 0 {
		// If no healthy IPs, delete the record entirely
		if len(currentIPs) == 0 {
			// There's no existing record, nothing to do
			return nil
		}

		slog.DebugContext(ctx, "No healthy IPs, deleting record", slog.String("recordName", recordName))
		err = a.deleteRecord(ctx, recordName)
		if err != nil {
			return fmt.Errorf("error deleting record for domain %s: %w", domain, err)
		}

		return nil
	}

	// Check if the old list and new list are the same
	// Because the lists could be in a different order, we sort them first (the lists are generally very small)
	diff := len(ips) != len(currentIPs)
	if !diff {
		slices.Sort(ips)
		slices.Sort(currentIPs)
		diff = !slices.Equal(ips, currentIPs)
	}

	// Update if there's any difference
	if diff {
		// Create or update record with healthy IPs
		slog.DebugContext(ctx, "Creating/updating record with healthy IPs", slog.String("recordName", recordName), slog.Any("ips", ips))
		err = a.createOrUpdateRecord(ctx, recordName, ips, ttl)
		if err != nil {
			return fmt.Errorf("error creating/updating record for domain %s: %w", domain, err)
		}
	}

	return nil
}

// azureARecord represents an A record from the Azure DNS API
type azureARecord struct {
	IPv4Address string `json:"ipv4Address"`
}

// azureRecordProperties represents a record's properties from the Azure DNS API
type azureRecordProperties struct {
	TTL      int            `json:"TTL"`
	ARecords []azureARecord `json:"ARecords"`
}

// azureRecord represents a DNS record from Azure DNS API
type azureRecord struct {
	Name       string                `json:"name"`
	Properties azureRecordProperties `json:"properties"`
}

// azureRecordSet represents a record set for creating/updating records
type azureRecordSet struct {
	Properties azureRecordProperties `json:"properties"`
}

// azureRecordsResponse represents the response from listing records
type azureRecordsResponse struct {
	Value []azureRecord `json:"value"`
}

func (a *AzureProvider) getRecordName(domain string) string {
	// Trim the ending dot if present
	domain = strings.TrimSuffix(domain, ".")

	// Extract subdomain from full domain
	if domain == a.zoneName {
		// Root domain
		return "@"
	}
	if strings.HasSuffix(domain, "."+a.zoneName) {
		return domain[:(len(domain) - len(a.zoneName) - 1)]
	}

	// If domain doesn't match zone, return as-is (might be an error case)
	return domain
}

// getAccessToken gets a fresh access token using the Azure identity library
func (a *AzureProvider) getAccessToken(parentCtx context.Context) (string, error) {
	tokenRequestOptions := policy.TokenRequestOptions{
		Scopes: []string{"https://management.azure.com/.default"},
	}

	ctx, cancel := context.WithTimeout(parentCtx, 20*time.Second)
	defer cancel()
	token, err := a.credential.GetToken(ctx, tokenRequestOptions)
	if err != nil {
		return "", fmt.Errorf("error getting access token: %w", err)
	}

	return token.Token, nil
}

func (a *AzureProvider) getExistingIPs(ctx context.Context, domain string) ([]string, error) {
	start := time.Now()
	var success bool
	if a.metrics != nil {
		defer func() {
			a.metrics.RecordAPICall("azure", http.MethodGet,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/A",
					a.subscriptionID, a.resourceGroupName, a.zoneName,
				),
				success, time.Since(start))
		}()
	}

	// Get access token
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting access token: %w", err)
	}

	recordName := a.getRecordName(domain)
	baseURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/A",
		a.subscriptionID, a.resourceGroupName, a.zoneName,
	)

	// Add query parameters
	// We filter for the specific record we want
	params := url.Values{}
	params.Set("api-version", "2018-05-01")
	params.Set("$recordsetnamesuffix", recordName)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, baseURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	res, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		return nil, fmt.Errorf("invalid response status code HTTP %d; response: %s", res.StatusCode, string(body))
	}

	var response azureRecordsResponse
	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	// Get the list of A IPs
	var ips []string
	if len(response.Value) > 0 {
		for _, r := range response.Value {
			if len(r.Properties.ARecords) == 0 || r.Name != recordName {
				continue
			}

			ips = slices.Grow(ips, len(r.Properties.ARecords))
			for _, aRecord := range r.Properties.ARecords {
				ips = append(ips, aRecord.IPv4Address)
			}
		}
	}

	success = true
	return ips, nil
}

func (a *AzureProvider) createOrUpdateRecord(ctx context.Context, recordName string, ips []string, ttl int) error {
	start := time.Now()
	var success bool
	if a.metrics != nil {
		defer func() {
			a.metrics.RecordAPICall(
				"azure", http.MethodPut,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/A/%s",
					a.subscriptionID, a.resourceGroupName, a.zoneName, recordName,
				),
				success, time.Since(start),
			)
		}()
	}

	// Get access token
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("error getting access token: %w", err)
	}

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/A/%s?api-version=2018-05-01",
		a.subscriptionID, a.resourceGroupName, a.zoneName, recordName,
	)

	// Build A records
	aRecords := make([]azureARecord, len(ips))
	for i, ip := range ips {
		aRecords[i] = azureARecord{
			IPv4Address: ip,
		}
	}

	recordSet := azureRecordSet{
		Properties: azureRecordProperties{
			TTL:      ttl,
			ARecords: aRecords,
		},
	}

	jsonData, err := json.Marshal(recordSet)
	if err != nil {
		return fmt.Errorf("error marshalling request body: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, url, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	res, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", res.StatusCode, string(body))
	}

	success = true
	return nil
}

func (a *AzureProvider) deleteRecord(ctx context.Context, recordName string) error {
	start := time.Now()
	var success bool
	if a.metrics != nil {
		defer func() {
			a.metrics.RecordAPICall("azure", http.MethodDelete,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/A/%s",
					a.subscriptionID, a.resourceGroupName, a.zoneName, recordName,
				),
				success, time.Since(start),
			)
		}()
	}

	// Get access token
	accessToken, err := a.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("error getting access token: %w", err)
	}

	url := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/A/%s?api-version=2018-05-01",
		a.subscriptionID, a.resourceGroupName, a.zoneName, recordName,
	)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)

	res, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	// Azure returns 200 for successful deletion or 404 if record doesn't exist
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", res.StatusCode, string(body))
	}

	success = true
	return nil
}
