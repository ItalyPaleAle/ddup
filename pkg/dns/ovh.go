package dns

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// getOVHEndpoint returns the full API endpoint URL based on the provided endpoint
func getOVHEndpoint(endpoint string) string {
	switch endpoint {
	case "", "eu":
		return "https://eu.api.ovh.com/1.0"
	case "ca":
		return "https://ca.api.ovh.com/1.0"
	case "us":
		return "https://api.us.ovhcloud.com/1.0"
	default:
		// If it's not a known region, assume it's a full URL
		// Remove trailing slash if present
		if len(endpoint) > 0 && endpoint[len(endpoint)-1] == '/' {
			return endpoint[:len(endpoint)-1]
		}
		return endpoint
	}
}

// OVHProvider implements the Provider interface for OVH DNS
type OVHProvider struct {
	name        string
	apiKey      string
	apiSecret   string
	consumerKey string
	zoneName    string
	endpoint    string
	metrics     *appmetrics.AppMetrics
	httpClient  *http.Client
}

// NewOVHProvider creates a new OVH DNS provider
func NewOVHProvider(name string, cfg *config.OVHConfig, metrics *appmetrics.AppMetrics) (*OVHProvider, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("API key is required")
	}
	if cfg.APISecret == "" {
		return nil, errors.New("API secret is required")
	}
	if cfg.ConsumerKey == "" {
		return nil, errors.New("consumer key is required")
	}
	if cfg.ZoneName == "" {
		return nil, errors.New("zone name is required")
	}

	endpoint := getOVHEndpoint(cfg.Endpoint)

	return &OVHProvider{
		name:        name,
		apiKey:      cfg.APIKey,
		apiSecret:   cfg.APISecret,
		consumerKey: cfg.ConsumerKey,
		zoneName:    cfg.ZoneName,
		endpoint:    endpoint,
		metrics:     metrics,
		httpClient:  http.DefaultClient,
	}, nil
}

// Name returns the provider's name
func (o *OVHProvider) Name() string {
	return o.name
}

// UpdateRecords updates DNS records for the given domain with the provided IPs
func (o *OVHProvider) UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error {
	// First, get existing records
	existingRecords, err := o.getExistingRecords(ctx, domain)
	if err != nil {
		return fmt.Errorf("error getting existing records: %w", err)
	}

	// Map of existing IPs and record IDs
	existingIPs := make(map[string]int64)
	for _, record := range existingRecords {
		existingIPs[record.Target] = record.ID
	}

	// Map of IPs we want to preserve
	desiredIPs := make(map[string]struct{})
	for _, ip := range ips {
		desiredIPs[ip] = struct{}{}
	}

	// Delete records for IPs that are no longer healthy
	for ip, recordID := range existingIPs {
		_, ok := desiredIPs[ip]
		if ok {
			continue
		}

		slog.DebugContext(ctx, "Deleting record for unhealthy IP", "ip", ip, "recordID", recordID)

		err = o.deleteRecord(ctx, recordID)
		if err != nil {
			return fmt.Errorf("error deleting record %d for IP %s: %w", recordID, ip, err)
		}
	}

	// Create new records for healthy IPs that don't exist yet
	for _, ip := range ips {
		_, exists := existingIPs[ip]
		if exists {
			continue
		}

		slog.DebugContext(ctx, "Creating record for healthy IP", "ip", ip)

		err = o.createRecord(ctx, domain, ip, ttl)
		if err != nil {
			return fmt.Errorf("error creating record for IP %s: %w", ip, err)
		}
	}

	return nil
}

// OVHRecord represents a DNS record from OVH API
type OVHRecord struct {
	ID        int64  `json:"id"`
	FieldType string `json:"fieldType"`
	SubDomain string `json:"subDomain"`
	Target    string `json:"target"`
	TTL       int    `json:"ttl"`
	Zone      string `json:"zone"`
}

// OVHCreateRecordRequest represents the request structure for creating a DNS record
type OVHCreateRecordRequest struct {
	FieldType string `json:"fieldType"`
	SubDomain string `json:"subDomain"`
	Target    string `json:"target"`
	TTL       int    `json:"ttl"`
}

func (o *OVHProvider) getExistingRecords(ctx context.Context, domain string) ([]OVHRecord, error) {
	start := time.Now()
	var success bool
	if o.metrics != nil {
		defer func() {
			o.metrics.RecordAPICall("ovh", http.MethodGet, "/v1/domain/zone/"+o.zoneName+"/record", success, time.Since(start))
		}()
	}

	// Extract subdomain from full domain
	subDomain := ""
	if domain != o.zoneName {
		if len(domain) > len(o.zoneName)+1 && domain[len(domain)-len(o.zoneName)-1:] == "."+o.zoneName {
			subDomain = domain[:len(domain)-len(o.zoneName)-1]
		} else {
			return nil, fmt.Errorf("domain %s is not a subdomain of zone %s", domain, o.zoneName)
		}
	}

	url := fmt.Sprintf("%s/domain/zone/%s/record?fieldType=A&subDomain=%s", o.endpoint, o.zoneName, subDomain)

	var recordIDs []int64
	err := o.performJSONRequest(ctx, http.MethodGet, url, nil, &recordIDs)
	if err != nil {
		return nil, err
	}

	// Get detailed information for each record
	records := make([]OVHRecord, len(recordIDs))
	for i, recordID := range recordIDs {
		record, err := o.getRecord(ctx, recordID)
		if err != nil {
			return nil, fmt.Errorf("error getting record details for ID %d: %w", recordID, err)
		}
		records[i] = *record
	}

	success = true
	return records, nil
}

func (o *OVHProvider) getRecord(ctx context.Context, recordID int64) (*OVHRecord, error) {
	url := fmt.Sprintf("%s/domain/zone/%s/record/%d", o.endpoint, o.zoneName, recordID)

	var record OVHRecord
	err := o.performJSONRequest(ctx, http.MethodGet, url, nil, &record)
	if err != nil {
		return nil, err
	}

	if record.ID != recordID {
		return nil, fmt.Errorf("record ID mismatches in response: got '%d' but expected '%d'", record.ID, recordID)
	}

	return &record, nil
}

func (o *OVHProvider) deleteRecord(ctx context.Context, recordID int64) error {
	start := time.Now()
	var success bool
	if o.metrics != nil {
		defer func() {
			o.metrics.RecordAPICall("ovh", http.MethodDelete, "/v1/domain/zone/"+o.zoneName+"/record", success, time.Since(start))
		}()
	}

	url := fmt.Sprintf("%s/domain/zone/%s/record/%d", o.endpoint, o.zoneName, recordID)

	err := o.performJSONRequest(ctx, http.MethodDelete, url, nil, nil)
	if err != nil {
		return err
	}

	success = true
	return nil
}

func (o *OVHProvider) createRecord(ctx context.Context, domain, ip string, ttl int) error {
	start := time.Now()
	var success bool
	if o.metrics != nil {
		defer func() {
			o.metrics.RecordAPICall("ovh", http.MethodPost, "/v1/domain/zone/"+o.zoneName+"/record", success, time.Since(start))
		}()
	}

	// Extract subdomain from full domain
	subDomain := ""
	if domain != o.zoneName {
		if len(domain) > len(o.zoneName)+1 && domain[len(domain)-len(o.zoneName)-1:] == "."+o.zoneName {
			subDomain = domain[:len(domain)-len(o.zoneName)-1]
		} else {
			return fmt.Errorf("domain %s is not a subdomain of zone %s", domain, o.zoneName)
		}
	}

	url := o.endpoint + "/domain/zone/" + o.zoneName + "/record"

	record := OVHCreateRecordRequest{
		FieldType: "A",
		SubDomain: subDomain,
		Target:    ip,
		TTL:       ttl,
	}

	err := o.performJSONRequest(ctx, http.MethodPost, url, record, nil)
	if err != nil {
		return err
	}

	success = true
	return nil
}

func (o *OVHProvider) performJSONRequest(ctx context.Context, method string, url string, data any, dest any) error {
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := o.createAuthenticatedRequest(reqCtx, method, url, data)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	res, err := o.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", res.StatusCode, string(body))
	}

	// If the caller doesn't want the body, short-circuit
	if dest == nil {
		return nil
	}

	ct := res.Header.Get("Content-Type")
	if ct != "application/json" && !strings.HasPrefix(ct, "application/json;") {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("invalid response Content-Type '%s'; response: %s", ct, string(body))
	}

	err = json.NewDecoder(res.Body).Decode(&dest)
	if err != nil {
		return fmt.Errorf("error decoding JSON response: %w", err)
	}

	return nil
}

func (o *OVHProvider) createAuthenticatedRequest(ctx context.Context, method string, url string, data any) (*http.Request, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	var (
		bodyReader io.Reader
		bodyData   []byte
	)
	if data != nil {
		var err error
		bodyData, err = json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("error marshalling request body: %w", err)
		}

		bodyReader = bytes.NewReader(bodyData)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	// Calculate signature
	signature := o.calculateSignature(method, url, string(bodyData), timestamp)

	// Set headers
	req.Header.Set("X-Ovh-Application", o.apiKey)
	req.Header.Set("X-Ovh-Consumer", o.consumerKey)
	req.Header.Set("X-Ovh-Signature", signature)
	req.Header.Set("X-Ovh-Timestamp", timestamp)

	if bodyData != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

func (o *OVHProvider) calculateSignature(method, url, body, timestamp string) string {
	// OVH signature calculation: $1$<sha1_hex>(AS+CK+METHOD+URL+BODY+TSTAMP)
	data := o.apiSecret + "+" + o.consumerKey + "+" + method + "+" + url + "+" + body + "+" + timestamp

	hash := sha1.Sum([]byte(data))
	return "$1$" + hex.EncodeToString(hash[:])
}
