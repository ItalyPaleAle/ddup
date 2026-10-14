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

// Capabilities returns what the provider can publish
func (c *CloudflareProvider) Capabilities() Capabilities {
	return cloudflareCapabilities
}

// cloudflareCapabilities declares that Cloudflare publishes CNAME records, and that the proxied flag is set with the "cloudflare" provider options
var cloudflareCapabilities = Capabilities{
	Type:            "cloudflare",
	CNAME:           true,
	ProviderOptions: []string{"cloudflare"},
}

// UpdateRecords updates DNS records for the given domain so they point to the targets
//
// Targets are either A and AAAA records or a single CNAME record, because a CNAME can't coexist with other records.
// When the kind of records changes, an existing record is overwritten in place so the name is never left without records.
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

	result := UpdateResult{
		Previous: make([]string, len(existing)),
	}
	for i := range existing {
		existing[i].Content = canonicalRecordContent(existing[i])
		result.Previous[i] = existing[i].Content
	}
	slices.Sort(result.Previous)

	// Records that already exist are kept, and are overwritten only if their proxied flag differs
	missing := make([]Target, 0, len(desired))
	kept := make(map[string]struct{}, len(desired))
	for _, d := range desired {
		idx := slices.IndexFunc(existing, func(r CloudflareRecord) bool {
			_, used := kept[r.ID]
			return !used && r.Type == d.Type && r.Content == d.Value
		})
		if idx < 0 {
			missing = append(missing, d)
			continue
		}

		rec := existing[idx]
		kept[rec.ID] = struct{}{}
		if rec.Proxied == d.Proxied {
			continue
		}

		slog.DebugContext(ctx, "Updating proxied flag of record", "type", d.Type, "value", d.Value, "proxied", d.Proxied)
		err = c.updateRecord(ctx, rec.ID, domain, d, ttl)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("error updating record %s for %s: %w", rec.ID, d.Value, err)
		}
		result.Changed = true
	}

	// Records that are not desired anymore
	stale := make([]CloudflareRecord, 0, len(existing))
	for _, r := range existing {
		_, ok := kept[r.ID]
		if !ok {
			stale = append(stale, r)
		}
	}

	// A CNAME can't coexist with other records, so in some cases a new record can't be created next to the existing ones
	// This is the case when going from a CNAME to A and AAAA records, and when going to a CNAME from anything else (including another CNAME)
	// To avoid leaving the name without records, which resolvers could cache, we overwrite one of the existing records with the first missing one
	if len(missing) > 0 && len(stale) > 0 {
		var changed bool
		changed, missing, stale, err = c.overwriteInPlace(ctx, domain, ttl, missing, stale)
		if err != nil {
			return UpdateResult{}, err
		}
		result.Changed = result.Changed || changed
	}

	// Create replacements before removing stale records so a failed create does not leave the domain empty
	for _, d := range missing {
		slog.DebugContext(ctx, "Creating record", "type", d.Type, "value", d.Value, "proxied", d.Proxied)

		err = c.createRecord(ctx, domain, d, ttl)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("error creating record for %s: %w", d, err)
		}
		result.Changed = true
	}

	// Delete records that are no longer desired
	for _, r := range stale {
		slog.DebugContext(ctx, "Deleting record", "type", r.Type, "value", r.Content, "recordID", r.ID)

		err = c.deleteRecord(ctx, r.ID)
		if err != nil {
			return UpdateResult{}, fmt.Errorf("error deleting record %s for %s %s: %w", r.ID, r.Type, r.Content, err)
		}
		result.Changed = true
	}

	return result, nil
}

// overwriteInPlace overwrites a stale record with the first missing target, when the new record could not be created next to the stale ones
// It returns the missing targets and the stale records that are left to handle
func (c *CloudflareProvider) overwriteInPlace(ctx context.Context, domain string, ttl int, missing []Target, stale []CloudflareRecord) (changed bool, remainingMissing []Target, remainingStale []CloudflareRecord, err error) {
	// Prefer an existing CNAME, which would block any other record
	// If the target is a CNAME, any stale record can be overwritten
	idx := slices.IndexFunc(stale, func(r CloudflareRecord) bool { return r.Type == recordTypeCNAME })
	if idx < 0 && missing[0].Type == recordTypeCNAME {
		idx = 0
	}
	if idx < 0 {
		// Only A and AAAA records can be created next to the existing ones
		return false, missing, stale, nil
	}

	// Going to a CNAME requires that there are no other records, so we delete them first
	// The record that is overwritten keeps serving until the last moment
	if missing[0].Type == recordTypeCNAME {
		for i, r := range stale {
			if i == idx {
				continue
			}

			slog.DebugContext(ctx, "Deleting record", "type", r.Type, "value", r.Content, "recordID", r.ID)
			err = c.deleteRecord(ctx, r.ID)
			if err != nil {
				return changed, missing, stale, fmt.Errorf("error deleting record %s for %s %s: %w", r.ID, r.Type, r.Content, err)
			}
			changed = true
		}
		stale = []CloudflareRecord{stale[idx]}
		idx = 0
	}

	rec := stale[idx]
	slog.DebugContext(ctx, "Overwriting record in place", "from", rec.Type, "to", missing[0].Type, "value", missing[0].Value)
	err = c.updateRecord(ctx, rec.ID, domain, missing[0], ttl)
	if err == nil {
		return true, missing[1:], slices.Delete(stale, idx, idx+1), nil
	}

	// If the record can't be overwritten, delete it, which leaves the name without records for a moment
	slog.WarnContext(ctx, "Could not overwrite record in place, deleting it first", "recordID", rec.ID, "error", err)
	err = c.deleteRecord(ctx, rec.ID)
	if err != nil {
		return changed, missing, stale, fmt.Errorf("error deleting record %s for %s %s: %w", rec.ID, rec.Type, rec.Content, err)
	}
	return true, missing, slices.Delete(stale, idx, idx+1), nil
}

// canonicalizeTargets validates the targets, canonicalizes their values and removes duplicates
func canonicalizeTargets(targets []Target) ([]Target, error) {
	desired := make([]Target, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	var hasCNAME bool
	for _, t := range targets {
		switch t.Type {
		case recordTypeA, recordTypeAAAA:
			ip, err := IPTarget(t.Value, t.Proxied)
			if err != nil {
				return nil, err
			}
			if ip.Type != t.Type {
				return nil, fmt.Errorf("target %q is not valid for a %s record", t.Value, t.Type)
			}
			t = ip
		case recordTypeCNAME:
			hasCNAME = true
			t.Value = canonicalizeHostname(t.Value)
		default:
			return nil, fmt.Errorf("%w: unknown record type %q", ErrUnsupportedTarget, t.Type)
		}

		key := t.String()
		_, dup := seen[key]
		if dup {
			continue
		}
		seen[key] = struct{}{}
		desired = append(desired, t)
	}

	if hasCNAME && len(desired) > 1 {
		return nil, errors.New("a CNAME target can't be published together with other targets")
	}
	return desired, nil
}

// canonicalRecordContent returns the content of an existing record in canonical form, so it can be compared with the targets
func canonicalRecordContent(r CloudflareRecord) string {
	if r.Type == recordTypeCNAME {
		return canonicalizeHostname(r.Content)
	}
	ip, err := canonicalizeIP(r.Content)
	if err != nil {
		// Leave as-is: it won't match any target and the record will be removed
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

func (c *CloudflareProvider) createRecord(ctx context.Context, domain string, target Target, ttl int) error {
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

	jsonData, err := json.Marshal(recordBody(domain, target, ttl))
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
func recordBody(domain string, target Target, ttl int) map[string]any {
	// Cloudflare manages the TTL of proxied records, and requires it to be 1 (automatic)
	if target.Proxied {
		ttl = 1
	}
	return map[string]any{
		"type":    target.Type,
		"name":    domain,
		"content": target.Value,
		"ttl":     ttl,
		"proxied": target.Proxied,
	}
}

// updateRecord overwrites an existing record, which can change its type too
func (c *CloudflareProvider) updateRecord(ctx context.Context, recordID string, domain string, target Target, ttl int) error {
	start := time.Now()
	var success bool
	if c.metrics != nil {
		defer func() {
			c.metrics.RecordAPICall("cloudflare", http.MethodPut, fmt.Sprintf("/v4/zones/%s/dns_records", c.zoneID), success, time.Since(start))
		}()
	}

	jsonData, err := json.Marshal(recordBody(domain, target, ttl))
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
