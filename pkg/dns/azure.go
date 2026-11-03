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

// ProviderType returns the key of the provider in the configuration
func (a *AzureProvider) ProviderType() string {
	return "azure"
}

// HasCNAME returns true for Azure DNS
func (a *AzureProvider) HasCNAME() bool {
	return true
}

// ValidateProviderOptions checks the provider options of the endpoints of a domain
// The provider has no options, so it rejects all of them
// It also rejects endpoints with cname at the apex of the zone, where Azure DNS doesn't allow CNAME records
func (a *AzureProvider) ValidateProviderOptions(d *config.ConfigDomain) error {
	err := rejectProviderOptions(d, a.ProviderType())
	if err != nil {
		return err
	}

	// CNAME records cannot be set for the apex domain
	// UpdateRecords deletes the other record sets before creating a CNAME, so this must be caught before the records are changed
	if a.getRecordName(d.RecordName) != "@" {
		return nil
	}
	for ei, e := range d.Endpoints {
		if e.CNAME != "" {
			return fmt.Errorf("domain %s endpoint %d (%s) is invalid: cname can't be used at the apex of the zone with provider '%s' (%s)", d.RecordName, ei, e.Name, d.Provider, a.ProviderType())
		}
	}

	return nil
}

// UpdateRecords updates the A, AAAA and CNAME record sets for the given domain so they point to the targets
//
// Azure DNS can't change more than one record set at a time, and doesn't allow a CNAME record set next to other record sets with the same name
// Normally, record sets are created or updated before the others are deleted, so the name is never left without records
// When there's a CNAME record set before or after the update, the record sets that are not desired anymore are deleted first, which leaves the name without records for a moment
func (a *AzureProvider) UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (result UpdateResult, err error) {
	desired, err := recordSetsFromTargets(a.name, targets)
	if err != nil {
		return UpdateResult{}, err
	}

	// Read all record types before making changes
	current := make([][]string, len(desired))
	var hasCNAME bool
	for i, rs := range desired {
		current[i], err = a.getExistingValues(ctx, domain, rs.Type)
		switch {
		case err != nil && rs.Type == recordTypeCNAME && len(rs.Values) == 0 && isAzureForbidden(err):
			// Identities with a custom role that only allows managing A and AAAA records, as documented before ddup supported CNAME records with Azure DNS, can't read them
			// When no CNAME is desired, we assume there's none: if there is one, creating the other records fails
			slog.DebugContext(ctx, "Not allowed to read CNAME records, assuming there are none", slog.String("domain", domain))
			current[i] = nil
		case err != nil:
			return UpdateResult{}, fmt.Errorf("error getting existing %s records: %w", rs.Type, err)
		}
		result.Previous = append(result.Previous, current[i]...)

		if rs.Type == recordTypeCNAME && (len(rs.Values) > 0 || len(current[i]) > 0) {
			hasCNAME = true
		}
	}
	slices.Sort(result.Previous)

	// Additions first (record types with desired values), then removals (record types with none)
	// A CNAME record set can't coexist with other record sets, so in that case removals go first
	order := []bool{true, false}
	if hasCNAME {
		order = []bool{false, true}
	}
	for _, wantValues := range order {
		for i, rs := range desired {
			if (len(rs.Values) > 0) != wantValues {
				continue
			}
			changed, err := a.updateRecordType(ctx, domain, rs.Type, ttl, rs.Values, current[i])
			if err != nil {
				return UpdateResult{}, err
			}
			result.Changed = result.Changed || changed
		}
	}

	return result, nil
}

// updateRecordType makes the record set for the type match the values, and returns true if it had to write anything
func (a *AzureProvider) updateRecordType(ctx context.Context, domain string, recordType string, ttl int, values []string, currentValues []string) (bool, error) {
	recordName := a.getRecordName(domain)

	if len(values) == 0 {
		if len(currentValues) == 0 {
			return false, nil
		}

		slog.DebugContext(
			ctx,
			"Deleting record set",
			slog.String("recordName", recordName),
			slog.String("recordType", recordType),
		)
		err := a.deleteRecord(ctx, recordName, recordType)
		if err != nil {
			return false, fmt.Errorf(
				"error deleting %s record for domain %s: %w",
				recordType,
				domain,
				err,
			)
		}

		return true, nil
	}

	current := append([]string(nil), currentValues...)
	slices.Sort(current)

	// values is already sorted
	if slices.Equal(values, current) {
		return false, nil
	}

	slog.DebugContext(
		ctx,
		"Creating/updating record set",
		slog.String("recordName", recordName),
		slog.String("recordType", recordType),
		slog.Any("values", values),
	)

	err := a.createOrUpdateRecord(
		ctx,
		recordName,
		recordType,
		values,
		ttl,
	)
	if err != nil {
		return false, fmt.Errorf("error creating/updating %s record for domain %s: %w", recordType, domain, err)
	}

	return true, nil
}

// azureARecord represents an A record from the Azure DNS API
type azureARecord struct {
	IPv4Address string `json:"ipv4Address"`
}

// azureAAAARecord represents an AAAA record from the Azure DNS API
type azureAAAARecord struct {
	IPv6Address string `json:"ipv6Address"`
}

// azureCNAMERecord represents a CNAME record from the Azure DNS API
type azureCNAMERecord struct {
	CNAME string `json:"cname"`
}

// azureRecordProperties represents a record's properties from the Azure DNS API
//
//nolint:tagliatelle
type azureRecordProperties struct {
	TTL         int               `json:"TTL"`
	ARecords    []azureARecord    `json:"ARecords,omitempty"`
	AAAARecords []azureAAAARecord `json:"AAAARecords,omitempty"`
	CNAMERecord *azureCNAMERecord `json:"CNAMERecord,omitempty"`
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

// azureHTTPError is returned when the Azure API responds with a status code that is not successful
type azureHTTPError struct {
	StatusCode int
	Body       string
}

// Error implements the error interface
func (e azureHTTPError) Error() string {
	return fmt.Sprintf("invalid response status code HTTP %d; response: %s", e.StatusCode, e.Body)
}

// isAzureForbidden returns true if the error is a response from the Azure API saying that the identity is not allowed to perform the request
func isAzureForbidden(err error) bool {
	var httpErr azureHTTPError
	return errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusForbidden
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

// getExistingValues returns the canonical values of the record set with the domain's name and the type: IP addresses for A and AAAA records, and the hostname for CNAME records
func (a *AzureProvider) getExistingValues(ctx context.Context, domain string, recordType string) ([]string, error) {
	start := time.Now()
	var success bool
	if a.metrics != nil {
		defer func() {
			a.metrics.RecordAPICall("azure", http.MethodGet,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s",
					a.subscriptionID, a.resourceGroupName, a.zoneName, recordType,
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
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s",
		a.subscriptionID,
		a.resourceGroupName,
		a.zoneName,
		recordType,
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
		return nil, azureHTTPError{StatusCode: res.StatusCode, Body: string(body)}
	}

	var response azureRecordsResponse
	err = json.NewDecoder(res.Body).Decode(&response)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	// Get the values of the record set
	var values []string
	for _, record := range response.Value {
		if record.Name != recordName {
			continue
		}

		switch recordType {
		case recordTypeA:
			for _, item := range record.Properties.ARecords {
				values = append(values, item.IPv4Address)
			}
		case recordTypeAAAA:
			for _, item := range record.Properties.AAAARecords {
				values = append(values, item.IPv6Address)
			}
		case recordTypeCNAME:
			// A CNAME record set can exist without a value
			if record.Properties.CNAMERecord != nil && record.Properties.CNAMERecord.CNAME != "" {
				values = append(values, canonicalizeHostname(record.Properties.CNAMERecord.CNAME))
			}
		}
	}

	if recordType == recordTypeCNAME {
		success = true
		return values, nil
	}

	canonicalIPs, err := canonicalizeIPs(values)
	if err != nil {
		return nil, fmt.Errorf("invalid IP address in existing %s record: %w", recordType, err)
	}

	success = true
	return canonicalIPs, nil
}

// createOrUpdateRecord creates or replaces the record set with the values
// A CNAME record set must have exactly one value
func (a *AzureProvider) createOrUpdateRecord(ctx context.Context, recordName string, recordType string, values []string, ttl int) error {
	start := time.Now()
	var success bool
	if a.metrics != nil {
		defer func() {
			a.metrics.RecordAPICall(
				"azure", http.MethodPut,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s/%s",
					a.subscriptionID,
					a.resourceGroupName,
					a.zoneName,
					recordType,
					recordName,
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

	requestURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s/%s?api-version=2018-05-01",
		a.subscriptionID,
		a.resourceGroupName,
		a.zoneName,
		recordType,
		recordName,
	)

	properties := azureRecordProperties{
		TTL: ttl,
	}

	switch recordType {
	case recordTypeA:
		properties.ARecords = make([]azureARecord, len(values))

		for i, ip := range values {
			properties.ARecords[i] = azureARecord{
				IPv4Address: ip,
			}
		}
	case recordTypeAAAA:
		properties.AAAARecords = make([]azureAAAARecord, len(values))

		for i, ip := range values {
			properties.AAAARecords[i] = azureAAAARecord{
				IPv6Address: ip,
			}
		}
	case recordTypeCNAME:
		if len(values) != 1 {
			// Indicates a development-time error, since a CNAME target can't be published with other targets
			return fmt.Errorf("a CNAME record set must have exactly one value, got %d", len(values))
		}

		properties.CNAMERecord = &azureCNAMERecord{
			CNAME: values[0],
		}
	default:
		return fmt.Errorf("%w: unknown record type %q", ErrUnsupportedTarget, recordType)
	}

	recordSet := azureRecordSet{
		Properties: properties,
	}

	jsonData, err := json.Marshal(recordSet)
	if err != nil {
		return fmt.Errorf("error marshalling request body: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, requestURL, bytes.NewReader(jsonData))
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

func (a *AzureProvider) deleteRecord(ctx context.Context, recordName string, recordType string) error {
	start := time.Now()
	var success bool
	if a.metrics != nil {
		defer func() {
			a.metrics.RecordAPICall("azure", http.MethodDelete,
				fmt.Sprintf(
					"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s/%s",
					a.subscriptionID,
					a.resourceGroupName,
					a.zoneName,
					recordType,
					recordName,
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

	requestURL := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/dnsZones/%s/%s/%s?api-version=2018-05-01",
		a.subscriptionID,
		a.resourceGroupName,
		a.zoneName,
		recordType,
		recordName,
	)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, requestURL, nil)
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
