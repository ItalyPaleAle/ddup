package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
)

// CloudflareProvider implements the Provider interface for Cloudflare DNS
type CloudflareProvider struct {
	apiToken   string
	zoneID     string
	recordType string
	ttl        int
	httpClient *http.Client
}

// NewCloudflareProvider creates a new Cloudflare DNS provider
func NewCloudflareProvider(cfg *config.CloudflareConfig, recordType string, ttl int) (*CloudflareProvider, error) {
	if cfg.APIToken == "" {
		return nil, errors.New("API token is required")
	}
	if cfg.ZoneID == "" {
		return nil, errors.New("zone ID is required")
	}

	return &CloudflareProvider{
		apiToken:   cfg.APIToken,
		zoneID:     cfg.ZoneID,
		recordType: recordType,
		ttl:        ttl,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}, nil
}

// UpdateRecords updates DNS records for the given domain with the provided IPs
func (c *CloudflareProvider) UpdateRecords(ctx context.Context, domain string, ips []string) error {
	// First, get existing records
	existingRecords, err := c.getExistingRecords(ctx, domain)
	if err != nil {
		return fmt.Errorf("error getting existing records: %w", err)
	}

	// Delete existing records
	for _, record := range existingRecords {
		err = c.deleteRecord(ctx, record.ID)
		if err != nil {
			return fmt.Errorf("error deleting record %s: %w", record.ID, err)
		}
	}

	// Create new records for healthy IPs
	for _, ip := range ips {
		err = c.createRecord(ctx, domain, ip)
		if err != nil {
			return fmt.Errorf("error creating record for IP %s: %w", ip, err)
		}
	}

	return nil
}

// CloudflareRecord represents a DNS record from Cloudflare API
type CloudflareRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// CloudflareResponse represents the response structure from Cloudflare API
type CloudflareResponse struct {
	Success bool               `json:"success"`
	Errors  []CloudflareError  `json:"errors"`
	Result  []CloudflareRecord `json:"result"`
}

// CloudflareError represents an error from Cloudflare API
type CloudflareError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *CloudflareProvider) getExistingRecords(ctx context.Context, domain string) ([]CloudflareRecord, error) {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s&type=%s", c.zoneID, domain, c.recordType)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var cfResp CloudflareResponse
	err = json.NewDecoder(resp.Body).Decode(&cfResp)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	if !cfResp.Success {
		return nil, fmt.Errorf("API error: %v", cfResp.Errors)
	}

	return cfResp.Result, nil
}

func (c *CloudflareProvider) deleteRecord(ctx context.Context, recordID string) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", c.zoneID, recordID)

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", resp.StatusCode, string(body))
	}

	return nil
}

func (c *CloudflareProvider) createRecord(ctx context.Context, domain, ip string) error {
	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records", c.zoneID)

	record := map[string]interface{}{
		"type":    c.recordType,
		"name":    domain,
		"content": ip,
		"ttl":     c.ttl,
	}

	jsonData, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("error marshalling request body: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request error: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("invalid response status code HTTP %d; response: %s", resp.StatusCode, string(body))
	}

	return nil
}
