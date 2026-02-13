package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// UnifiProvider implements the Provider interface for Unifi DNS
type UnifiProvider struct {
	name               string
	host               string
	apiKey             string
	site               string
	externalController bool
	metrics            *appmetrics.AppMetrics
	httpClient         *http.Client
}

// NewUnifiProvider creates a new Unifi DNS provider
func NewUnifiProvider(name string, cfg *config.UnifiConfig, metrics *appmetrics.AppMetrics) (*UnifiProvider, error) {
	if cfg.Host == "" {
		return nil, errors.New("host is required")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("API key is required")
	}

	// Default site to "default" if not specified
	site := cfg.Site
	if site == "" {
		site = "default"
	}

	// Create HTTP client with cookie jar
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("error creating cookie jar: %w", err)
	}

	httpClient := &http.Client{
		Jar:       jar,
		Transport: http.DefaultTransport,
	}

	// Configure TLS verification
	if cfg.SkipTLSVerify {
		ct, ok := httpClient.Transport.(*http.Transport)
		if !ok {
			// Should never happen...
			return nil, fmt.Errorf("HTTP client's transport is not *http.Transport, but %T", httpClient.Transport)
		}
		transport := ct.Clone()

		if transport.TLSClientConfig == nil {
			//gosec:disable G402 - TLS MinVersion too low can be accepted here
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec

		httpClient.Transport = transport
	}

	return &UnifiProvider{
		name:               name,
		host:               strings.TrimSuffix(cfg.Host, "/"),
		apiKey:             cfg.APIKey,
		site:               site,
		externalController: cfg.ExternalController,
		metrics:            metrics,
		httpClient:         httpClient,
	}, nil
}

// Name returns the provider's name
func (u *UnifiProvider) Name() string {
	return u.name
}

// UpdateRecords updates DNS records for the given domain with the provided IPs
func (u *UnifiProvider) UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error {
	// First, get existing records
	existingRecords, err := u.getExistingRecords(ctx, domain)
	if err != nil {
		return fmt.Errorf("error getting existing records: %w", err)
	}

	// Map of existing IPs and record IDs
	existingIPs := make(map[string]string)
	for _, record := range existingRecords {
		existingIPs[record.Value] = record.ID
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

		err = u.deleteRecord(ctx, recordID)
		if err != nil {
			return fmt.Errorf("error deleting record %s for IP %s: %w", recordID, ip, err)
		}
	}

	// Create new records for healthy IPs that don't exist yet
	for _, ip := range ips {
		_, exists := existingIPs[ip]
		if exists {
			continue
		}

		slog.DebugContext(ctx, "Creating record for healthy IP", "ip", ip)

		err = u.createRecord(ctx, domain, ip, ttl)
		if err != nil {
			return fmt.Errorf("error creating record for IP %s: %w", ip, err)
		}
	}

	return nil
}

// UnifiDNSRecord represents a DNS record from Unifi API
//
//nolint:tagliatelle
type UnifiDNSRecord struct {
	ID         string `json:"_id,omitempty"`
	Key        string `json:"key"`
	RecordType string `json:"record_type"`
	Value      string `json:"value"`
	TTL        int    `json:"ttl"`
	Enabled    bool   `json:"enabled"`
}

// UnifiErrorResponse represents an error response from Unifi API
type UnifiErrorResponse struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	ErrorCode int            `json:"errorCode"`
	Details   map[string]any `json:"details,omitempty"`
}

// String implements fmt.Stringer
func (ue UnifiErrorResponse) String() string {
	return fmt.Sprintf("(%d) %s: %s", ue.ErrorCode, ue.Code, ue.Message)
}

func (u *UnifiProvider) getAPIPath(recordID string) string {
	var basePath string
	if u.externalController {
		basePath = "/v2/api/site/" + u.site + "/static-dns"
	} else {
		basePath = "/proxy/network/v2/api/site/" + u.site + "/static-dns"
	}

	if recordID != "" {
		return basePath + "/" + recordID
	}
	return basePath
}

func (u *UnifiProvider) doRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	url := u.host + path

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	// Set headers
	req.Header.Set("X-Api-Key", u.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := u.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}

	return resp, nil
}

func (u *UnifiProvider) getExistingRecords(ctx context.Context, domain string) ([]UnifiDNSRecord, error) {
	start := time.Now()
	var success bool
	path := u.getAPIPath("")

	if u.metrics != nil {
		defer func() {
			u.metrics.RecordAPICall("unifi", http.MethodGet, path, success, time.Since(start))
		}()
	}

	resp, err := u.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
		return nil, fmt.Errorf("invalid response status code HTTP %d; response: %s", resp.StatusCode, string(body))
	}

	var records []UnifiDNSRecord
	err = json.NewDecoder(resp.Body).Decode(&records)
	if err != nil {
		return nil, fmt.Errorf("error decoding response: %w", err)
	}

	// Filter records matching the domain and type A
	filteredRecords := make([]UnifiDNSRecord, 0, len(records))
	for _, record := range records {
		if record.Key == domain && record.RecordType == "A" && record.Enabled {
			filteredRecords = append(filteredRecords, record)
		}
	}

	success = true
	return filteredRecords, nil
}

func (u *UnifiProvider) deleteRecord(ctx context.Context, recordID string) error {
	start := time.Now()
	var success bool
	path := u.getAPIPath(recordID)

	if u.metrics != nil {
		defer func() {
			u.metrics.RecordAPICall("unifi", http.MethodDelete, path, success, time.Since(start))
		}()
	}

	resp, err := u.doRequest(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", resp.StatusCode, string(body))
	}

	success = true
	return nil
}

func (u *UnifiProvider) createRecord(ctx context.Context, domain, ip string, ttl int) error {
	start := time.Now()
	var success bool
	path := u.getAPIPath("")

	if u.metrics != nil {
		defer func() {
			u.metrics.RecordAPICall("unifi", http.MethodPost, path, success, time.Since(start))
		}()
	}

	record := UnifiDNSRecord{
		Key:        domain,
		RecordType: "A",
		Value:      ip,
		TTL:        ttl,
		Enabled:    true,
	}

	jsonData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("error marshalling request body: %w", err)
	}

	resp, err := u.doRequest(ctx, http.MethodPost, path, jsonData)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", resp.StatusCode, string(body))
	}

	success = true
	return nil
}
