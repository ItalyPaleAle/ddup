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
	"slices"
	"strings"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// CloudflareProvider implements the Provider interface for Cloudflare DNS
type CloudflareProvider struct {
	name       string
	apiToken   string
	zoneID     string
	metrics    *appmetrics.AppMetrics
	httpClient *http.Client
}

// NewCloudflareProvider creates a new Cloudflare DNS provider
func NewCloudflareProvider(name string, cfg *config.CloudflareConfig, metrics *appmetrics.AppMetrics) (*CloudflareProvider, error) {
	if cfg.APIToken == "" {
		return nil, errors.New("API token is required")
	}
	if cfg.ZoneID == "" {
		return nil, errors.New("zone ID is required")
	}

	return &CloudflareProvider{
		name:       name,
		apiToken:   cfg.APIToken,
		zoneID:     cfg.ZoneID,
		metrics:    metrics,
		httpClient: http.DefaultClient,
	}, nil
}

// Name returns the provider's name
func (c *CloudflareProvider) Name() string {
	return c.name
}

// desiredRecord is a record that should exist in DNS
type desiredRecord struct {
	recordType string
	value      string
	proxied    bool
}

// UpdateRecords updates DNS records for the given domain so they point to the targets
//
// Targets are either A/AAAA records (IP addresses) or a single CNAME record, since a CNAME can't coexist with other records.
// When the type of records changes, an existing record is converted in place, so the name never has no records.
func (c *CloudflareProvider) UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (UpdateResult, error) {
	desired, err := canonicalizeTargets(targets)
	if err != nil {
		return UpdateResult{}, err
	}

	// Get the existing A, AAAA and CNAME records
	existing, err := c.getExistingRecords(ctx, domain)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("error getting existing records: %w", err)
	}

	result := UpdateResult{Previous: make([]string, 0, len(existing))}
	for i := range existing {
		existing[i].Content = canonicalRecordContent(existing[i])
		result.Previous = append(result.Previous, existing[i].Content)
	}
	slices.Sort(result.Previous)

	// Records that already exist as desired are kept, and updated in place only if their proxied status differs
	remaining := make([]CloudflareRecord, 0, len(existing))
	missing := make([]desiredRecord, 0, len(desired))
	kept := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		idx := slices.IndexFunc(existing, func(r CloudflareRecord) bool {
			_, used := kept[r.ID]
			return !used && r.Type == d.recordType && r.Content == d.value
		})
		if idx < 0 {
			missing = append(missing, d)
			continue
		}

		rec := existing[idx]
		kept[rec.ID] = struct{}{}
		if rec.Proxied != d.proxied {
			slog.DebugContext(ctx, "Updating proxied status of record", "value", d.value, "proxied", d.proxied)
			err = c.updateRecord(ctx, rec.ID, domain, d, ttl)
			if err != nil {
				return UpdateResult{}, fmt.Errorf("error updating record %s for %s: %w", rec.ID, d.value, err)
			}
			result.Changed = true
		}
	}
	for _, r := range existing {
		_, ok := kept[r.ID]
		if !ok {
			remaining = append(remaining, r)
		}
	}

	// If the type of records changes (A/AAAA to CNAME, CNAME to A/AAAA, or the target of a CNAME changes) we overwrite an existing record in place
	// Cloudflare doesn't allow a CNAME to coexist with other records, so creating the new record before deleting the old one is not possible,
	// and deleting first would leave the name without records for a moment (which resolvers could cache)
	if len(missing) > 0 && len(remaining) > 0 {
		convertIdx := slices.IndexFunc(remaining, func(r CloudflareRecord) bool { return r.Type == recordTypeCNAME })
		if convertIdx < 0 && missing[0].recordType == recordTypeCNAME {
			convertIdx = 0
		}

		if convertIdx >= 0 {
			// A CNAME can't coexist with other records, so when converting to one we first delete the other records
			// The record that is converted keeps serving until the last moment
			if missing[0].recordType == recordTypeCNAME {
				for i, other := range remaining {
					if i == convertIdx {
						continue
					}
					slog.DebugContext(ctx, "Deleting record", "type", other.Type, "value", other.Content, "recordID", other.ID)
					err = c.deleteRecord(ctx, other.ID)
					if err != nil {
						return UpdateResult{}, fmt.Errorf("error deleting record %s for %s: %w", other.ID, other.Content, err)
					}
					result.Changed = true
				}
				remaining = []CloudflareRecord{remaining[convertIdx]}
				convertIdx = 0
			}

			rec := remaining[convertIdx]
			slog.DebugContext(ctx, "Overwriting record in place", "from", rec.Type, "to", missing[0].recordType, "value", missing[0].value)
			err = c.updateRecord(ctx, rec.ID, domain, missing[0], ttl)
			if err == nil {
				result.Changed = true
				remaining = slices.Delete(remaining, convertIdx, convertIdx+1)
				missing = missing[1:]
			} else {
				// Fall back to deleting the record first, which can leave the name without records for a moment
				slog.WarnContext(ctx, "Could not overwrite record in place, deleting it first", "recordID", rec.ID, "error", err)
				err = c.deleteRecord(ctx, rec.ID)
				if err != nil {
					return UpdateResult{}, fmt.Errorf("error deleting record %s for %s: %w", rec.ID, rec.Content, err)
				}
				result.Changed = true
				remaining = slices.Delete(remaining, convertIdx, convertIdx+1)
			}
		}
	}

	// Create replacements before removing stale records so a failed create does not leave the domain empty
	for _, d := range missing {
		slog.DebugContext(ctx, "Creating record", "type", d.recordType, "value", d.value, "proxied", d.proxied)

		err = c.createRecord(ctx, domain, d, ttl)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("error creating record for %s (%s): %w", d.value, d.recordType, err)
		}
		result.Changed = true
	}

	// Delete records that are no longer desired
	for _, r := range remaining {
		slog.DebugContext(ctx, "Deleting record", "type", r.Type, "value", r.Content, "recordID", r.ID)

		err = c.deleteRecord(ctx, r.ID)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("error deleting record %s for %s: %w", r.ID, r.Content, err)
		}
		result.Changed = true
	}

	return result, nil
}

// canonicalizeTargets validates targets, canonicalizes IPs and hostnames, and removes duplicates
func canonicalizeTargets(targets []Target) ([]desiredRecord, error) {
	desired := make([]desiredRecord, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	var hasCNAME bool
	for _, t := range targets {
		d := desiredRecord{recordType: t.RecordType(), proxied: t.Proxied}
		if d.recordType == recordTypeCNAME {
			hasCNAME = true
			d.value = strings.TrimSuffix(strings.ToLower(t.Value), ".")
		} else {
			var err error
			d.value, err = canonicalizeIP(t.Value)
			if err != nil {
				return nil, err
			}
		}

		key := d.recordType + "|" + d.value
		_, dup := seen[key]
		if dup {
			continue
		}
		seen[key] = struct{}{}
		desired = append(desired, d)
	}

	if hasCNAME && len(desired) > 1 {
		return nil, errors.New("a CNAME target can't be published together with other targets")
	}
	return desired, nil
}

// canonicalRecordContent returns the content of the record in canonical form, so it can be compared with desired records
func canonicalRecordContent(r CloudflareRecord) string {
	if r.Type == recordTypeCNAME {
		return strings.TrimSuffix(strings.ToLower(r.Content), ".")
	}
	ip, err := canonicalizeIP(r.Content)
	if err != nil {
		// Leave as-is; it won't match any desired record and will be removed
		return r.Content
	}
	return ip
}

// CloudflareRecord represents a DNS record from Cloudflare API
type CloudflareRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
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

// String implements fmt.Stringer
func (ce CloudflareError) String() string {
	return fmt.Sprintf("(%d) %s", ce.Code, ce.Message)
}

func (c *CloudflareProvider) getExistingRecords(ctx context.Context, domain string) ([]CloudflareRecord, error) {
	var records []CloudflareRecord

	for _, recordType := range []string{recordTypeA, recordTypeAAAA, recordTypeCNAME} {
		typeRecords, err := c.getExistingRecordsByType(ctx, domain, recordType)
		if err != nil {
			return nil, err
		}

		records = append(records, typeRecords...)
	}

	return records, nil
}

func (c *CloudflareProvider) getExistingRecordsByType(ctx context.Context, domain string, recordType string) ([]CloudflareRecord, error) {
	start := time.Now()
	var success bool

	if c.metrics != nil {
		defer func() {
			c.metrics.RecordAPICall(
				"cloudflare",
				http.MethodGet,
				fmt.Sprintf("/v4/zones/%s/dns_records", c.zoneID),
				success,
				time.Since(start),
			)
		}()
	}

	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/zones/%s/dns_records?name=%s&type=%s",
		c.zoneID,
		domain,
		recordType,
	)
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

	success = true
	return cfResp.Result, nil
}

func (c *CloudflareProvider) deleteRecord(ctx context.Context, recordID string) error {
	start := time.Now()
	var success bool
	if c.metrics != nil {
		defer func() {
			c.metrics.RecordAPICall("cloudflare", http.MethodDelete, fmt.Sprintf("/v4/zones/%s/dns_records", c.zoneID), success, time.Since(start))
		}()
	}

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

	success = true
	return nil
}

func (c *CloudflareProvider) createRecord(ctx context.Context, domain string, d desiredRecord, ttl int) error {
	start := time.Now()
	var success bool
	if c.metrics != nil {
		defer func() {
			c.metrics.RecordAPICall("cloudflare", http.MethodPost, fmt.Sprintf("/v4/zones/%s/dns_records", c.zoneID), success, time.Since(start))
		}()
	}

	url := fmt.Sprintf(
		"https://api.cloudflare.com/client/v4/zones/%s/dns_records",
		c.zoneID,
	)

	jsonData, err := json.Marshal(recordBody(domain, d, ttl))
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

	success = true
	return nil
}

// recordBody returns the request body to create or overwrite a record
func recordBody(domain string, d desiredRecord, ttl int) map[string]any {
	// The TTL of proxied records is managed by Cloudflare, and must be 1 (automatic)
	if d.proxied {
		ttl = 1
	}
	return map[string]any{
		"type":    d.recordType,
		"name":    domain,
		"content": d.value,
		"ttl":     ttl,
		"proxied": d.proxied,
	}
}

// updateRecord overwrites an existing record, which can also change its type
func (c *CloudflareProvider) updateRecord(ctx context.Context, recordID string, domain string, d desiredRecord, ttl int) error {
	start := time.Now()
	var success bool
	if c.metrics != nil {
		defer func() {
			c.metrics.RecordAPICall("cloudflare", http.MethodPut, fmt.Sprintf("/v4/zones/%s/dns_records", c.zoneID), success, time.Since(start))
		}()
	}

	jsonData, err := json.Marshal(recordBody(domain, d, ttl))
	if err != nil {
		return fmt.Errorf("error marshalling request body: %w", err)
	}

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/zones/%s/dns_records/%s", c.zoneID, recordID)
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPut, url, bytes.NewReader(jsonData))
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

	success = true
	return nil
}
