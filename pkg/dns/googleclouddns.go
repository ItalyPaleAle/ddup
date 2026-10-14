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
	"os"
	"slices"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

const googleCloudDNSScope = "https://www.googleapis.com/auth/ndev.clouddns.readwrite"

// GoogleCloudDNSProvider implements the Provider interface for Google Cloud DNS
type GoogleCloudDNSProvider struct {
	name        string
	projectID   string
	managedZone string
	tokenSource oauth2.TokenSource
	metrics     *appmetrics.AppMetrics
	httpClient  *http.Client
}

// NewGoogleCloudDNSProvider creates a new Google Cloud DNS provider
func NewGoogleCloudDNSProvider(name string, cfg *config.GoogleCloudDNSConfig, metrics *appmetrics.AppMetrics) (*GoogleCloudDNSProvider, error) {
	if cfg.ProjectID == "" {
		return nil, errors.New("project ID is required")
	}
	if cfg.ManagedZone == "" {
		return nil, errors.New("managed zone is required")
	}

	// This context is used to refresh tokens for the lifetime of the provider
	// We set an HTTP client with a timeout
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 20 * time.Second})

	var (
		credentials *google.Credentials
		err         error
	)
	if cfg.CredentialsFile != "" {
		// If a key file is specified, authenticate as the service account
		slog.Info("Authenticating to Google Cloud with a service account key", slog.String("credentialsFile", cfg.CredentialsFile))
		var data []byte
		data, err = os.ReadFile(cfg.CredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("error reading credentials file: %w", err)
		}
		credentials, err = google.CredentialsFromJSONWithType(ctx, data, google.ServiceAccount, googleCloudDNSScope)
		if err != nil {
			return nil, fmt.Errorf("error loading service account key: %w", err)
		}
	} else {
		// Use Application Default Credentials
		slog.Info("Authenticating to Google Cloud with Application Default Credentials")
		credentials, err = google.FindDefaultCredentials(ctx, googleCloudDNSScope)
		if err != nil {
			return nil, fmt.Errorf("error finding Application Default Credentials: %w", err)
		}
	}

	return &GoogleCloudDNSProvider{
		name:        name,
		projectID:   cfg.ProjectID,
		managedZone: cfg.ManagedZone,
		tokenSource: credentials.TokenSource,
		metrics:     metrics,
		httpClient:  http.DefaultClient,
	}, nil
}

// Name returns the provider's name
func (g *GoogleCloudDNSProvider) Name() string {
	return g.name
}

// Capabilities returns what the provider can publish
func (g *GoogleCloudDNSProvider) Capabilities() Capabilities {
	return googleCloudDNSCapabilities
}

// UpdateRecords updates DNS A and AAAA records for the given domain with the provided IPs
// All changes are sent in a single change, which Cloud DNS applies atomically
func (g *GoogleCloudDNSProvider) UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (result UpdateResult, err error) {
	ips, err := plainIPsFromTargets(g.name, targets)
	if err != nil {
		return UpdateResult{}, err
	}

	ipv4, ipv6, err := splitIPs(ips)
	if err != nil {
		return UpdateResult{}, err
	}

	recordName := toFQDN(domain)
	existing, err := g.getExistingRecordSets(ctx, recordName)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("error getting existing records: %w", err)
	}

	recordSets := []struct {
		recordType string
		ips        []string
	}{
		{recordType: recordTypeA, ips: ipv4},
		{recordType: recordTypeAAAA, ips: ipv6},
	}

	var change googleCloudDNSChange
	for _, rs := range recordSets {
		current := existing[rs.recordType]
		currentIPs, err := canonicalizeIPs(current.Rrdatas)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("invalid IP address in existing %s record: %w", rs.recordType, err)
		}
		result.Previous = append(result.Previous, currentIPs...)

		desired := sortedUniqueIPs(rs.ips)
		if slices.Equal(desired, sortedUniqueIPs(currentIPs)) {
			continue
		}

		// Record sets can't be modified in place: they're replaced by deleting the existing one in the same change
		// Deletions must match the existing record set exactly, so we send it back as-is
		if len(current.Rrdatas) > 0 {
			change.Deletions = append(change.Deletions, current)
		}

		if len(desired) == 0 {
			slog.DebugContext(ctx, "No healthy IPs, deleting record", slog.String("recordName", recordName), slog.String("recordType", rs.recordType))
			continue
		}

		slog.DebugContext(ctx, "Creating/updating record with healthy IPs", slog.String("recordName", recordName), slog.String("recordType", rs.recordType), slog.Any("ips", desired))
		change.Additions = append(change.Additions, googleCloudDNSRecordSet{
			Name:    recordName,
			Type:    rs.recordType,
			TTL:     ttl,
			Rrdatas: desired,
		})
	}
	slices.Sort(result.Previous)

	if len(change.Additions) == 0 && len(change.Deletions) == 0 {
		return result, nil
	}

	err = g.createChange(ctx, change)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("error updating records for domain %s: %w", domain, err)
	}

	result.Changed = true
	return result, nil
}

// googleCloudDNSRecordSet represents a record set in the Cloud DNS API
type googleCloudDNSRecordSet struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl"`
	Rrdatas []string `json:"rrdatas"`
	// Only used to detect record sets with a routing policy, which are not supported
	RoutingPolicy *struct{} `json:"routingPolicy,omitempty"`
}

// googleCloudDNSListResponse represents the response from listing record sets
type googleCloudDNSListResponse struct {
	Rrsets        []googleCloudDNSRecordSet `json:"rrsets"`
	NextPageToken string                    `json:"nextPageToken"`
}

// googleCloudDNSChange represents the request body for creating a change
type googleCloudDNSChange struct {
	Additions []googleCloudDNSRecordSet `json:"additions,omitempty"`
	Deletions []googleCloudDNSRecordSet `json:"deletions,omitempty"`
}

// zonePath returns the path of the managed zone in the API
func (g *GoogleCloudDNSProvider) zonePath() string {
	return "/dns/v1/projects/" + g.projectID + "/managedZones/" + g.managedZone
}

// getExistingRecordSets returns the A and AAAA record sets with the given name, keyed by type
func (g *GoogleCloudDNSProvider) getExistingRecordSets(ctx context.Context, recordName string) (map[string]googleCloudDNSRecordSet, error) {
	start := time.Now()
	var success bool
	if g.metrics != nil {
		defer func() {
			g.metrics.RecordAPICall("googleclouddns", http.MethodGet, g.zonePath()+"/rrsets", success, time.Since(start))
		}()
	}

	recordSets := make(map[string]googleCloudDNSRecordSet, 2)
	params := url.Values{}
	params.Set("name", recordName)
	for {
		var response googleCloudDNSListResponse
		err := g.performRequest(ctx, http.MethodGet, "https://dns.googleapis.com"+g.zonePath()+"/rrsets?"+params.Encode(), nil, &response)
		if err != nil {
			return nil, err
		}

		for _, rs := range response.Rrsets {
			if rs.Type != recordTypeA && rs.Type != recordTypeAAAA {
				continue
			}

			// ddup can only manage record sets without a routing policy
			if rs.RoutingPolicy != nil {
				return nil, fmt.Errorf("%s record set %s uses a routing policy, which is not supported", rs.Type, recordName)
			}

			recordSets[rs.Type] = rs
		}

		if response.NextPageToken == "" {
			break
		}
		params.Set("pageToken", response.NextPageToken)
	}

	success = true
	return recordSets, nil
}

func (g *GoogleCloudDNSProvider) createChange(ctx context.Context, change googleCloudDNSChange) error {
	start := time.Now()
	var success bool
	if g.metrics != nil {
		defer func() {
			g.metrics.RecordAPICall("googleclouddns", http.MethodPost, g.zonePath()+"/changes", success, time.Since(start))
		}()
	}

	err := g.performRequest(ctx, http.MethodPost, "https://dns.googleapis.com"+g.zonePath()+"/changes", change, nil)
	if err != nil {
		return err
	}

	success = true
	return nil
}

func (g *GoogleCloudDNSProvider) performRequest(ctx context.Context, method string, reqURL string, data any, dest any) error {
	token, err := g.tokenSource.Token()
	if err != nil {
		return fmt.Errorf("error getting access token: %w", err)
	}

	var bodyReader io.Reader
	if data != nil {
		var body []byte
		body, err = json.Marshal(data)
		if err != nil {
			return fmt.Errorf("error marshalling request body: %w", err)
		}
		bodyReader = bytes.NewReader(body)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	token.SetAuthHeader(req)
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := g.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		resBody, _ := io.ReadAll(res.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", res.StatusCode, string(resBody))
	}

	if dest == nil {
		return nil
	}

	err = json.NewDecoder(res.Body).Decode(dest)
	if err != nil {
		return fmt.Errorf("error decoding response: %w", err)
	}

	return nil
}

// googleCloudDNSCapabilities declares that the provider only publishes A and AAAA records that are not proxied
var googleCloudDNSCapabilities = Capabilities{
	Type: "googleCloudDns",
}
