package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// Config represents the application configuration
type Config struct {
	// Interval to perform health checks, as a duration
	// +default 30s
	Interval time.Duration `yaml:"interval"`

	// Domains allows configuring multiple domains, each with its own endpoints
	Domains []ConfigDomain `yaml:"domains"`

	// Provider contains shared provider configuration (shared across all domains)
	Providers map[string]ConfigProvider `yaml:"providers"`

	// Logs contains configuration for logging
	Logs ConfigLogs `yaml:"logs"`

	// Server contains configuration for the server
	Server ConfigServer `yaml:"server"`

	// Webhooks are called when events occur (e.g. DNS records updated, no healthy endpoints)
	Webhooks []ConfigWebhook `yaml:"webhooks"`

	// Dev is meant for development only; it's undocumented
	Dev ConfigDev `yaml:"-"`

	// Internal keys
	internal internal `yaml:"-"`
}

// ConfigDomain represents a single domain and its endpoints
type ConfigDomain struct {
	// RecordName is the DNS record to update for this domain (e.g., "app.example.com")
	// +required
	RecordName string `yaml:"recordName"`

	// Name of the DNS provider as configured in the `providers` dictionary.
	// +required
	Provider string `yaml:"provider"`

	// TTL for the created records, in seconds
	// +default 60
	TTL int `yaml:"ttl"`

	// Configuration for health checks
	HealthChecks ConfigHealthChecks `yaml:"healthChecks"`

	// Endpoints to health check for this domain
	// +required
	Endpoints []*ConfigEndpoint `yaml:"endpoints"`
}

// ConfigHealthChecks configures the health checks for the endpoints
type ConfigHealthChecks struct {
	// Request timeout
	// Defaults to 3s
	Timeout time.Duration `yaml:"timeout"`

	// Maximum number of consecutive attempts before considering the endpoint unhealthy
	// Defaults to 2
	Attempts int `yaml:"attempts"`

	// Number of consecutive successful checks required before an endpoint that was removed from DNS is added back
	// Has no effect on endpoints that have not been removed since ddup started
	// Defaults to 1
	RecoverAfter int `yaml:"recoverAfter"`

	// HTTP method for the health check request: GET or HEAD
	// Defaults to GET
	Method string `yaml:"method"`

	// HTTP status code that indicates a healthy endpoint: either an exact code (like "204") or "2xx" for any 2xx code
	// Defaults to "2xx"
	ExpectStatus string `yaml:"expectStatus"`
}

// StatusMatches returns true if the HTTP status code indicates a healthy endpoint
// The value of ExpectStatus must have been validated
func (c ConfigHealthChecks) StatusMatches(code int) bool {
	if c.ExpectStatus == "" || c.ExpectStatus == "2xx" {
		return code >= 200 && code <= 299
	}
	expected, _ := strconv.Atoi(c.ExpectStatus)
	return code == expected
}

func validateExpectStatus(v string) error {
	if v == "" || v == "2xx" {
		return nil
	}
	code, err := strconv.Atoi(v)
	if err != nil || code < 100 || code > 599 {
		return fmt.Errorf("expectStatus must be a status code (like 204) or 2xx, got %q", v)
	}
	return nil
}

// ConfigWebhook represents a webhook called when events occur
type ConfigWebhook struct {
	// Webhook name, used for logging purposes
	// Defaults to the URL host
	Name string `yaml:"name"`

	// URL to call
	// +required
	URL string `yaml:"url"`

	// HTTP method
	// Defaults to POST
	Method string `yaml:"method"`

	// Additional headers to send
	// Values are Go templates, rendered with the event
	Headers map[string]string `yaml:"headers"`

	// Events that trigger this webhook; if empty, all events
	// Valid values: dns_updated, dns_update_failed, all_unhealthy
	Events []string `yaml:"events"`

	// Go template for the request body, rendered with the event
	// If empty, the event is sent as JSON
	Body string `yaml:"body"`

	// Request timeout for each attempt
	// Defaults to 10s
	Timeout time.Duration `yaml:"timeout"`

	// Maximum number of delivery attempts, with exponential backoff starting at 2s
	// Defaults to 5
	Attempts int `yaml:"attempts"`
}

// ConfigEndpoint represents a single endpoint to health check
type ConfigEndpoint struct {
	// Endpoint name, used for logging purposes
	// Defaults to the URL
	Name string `yaml:"name"`

	// Health check URL
	// Required, unless the endpoint uses `ipLookup`: then the endpoint is not health-checked, and is healthy as long as the IP address can be looked up
	URL string `yaml:"url"`

	// IP address to include in DNS records when healthy
	// IPv4 addresses create A records and IPv6 addresses create AAAA records
	// Exactly one of `ip`, `cname` and `ipLookup` is required
	IP string `yaml:"ip"`

	// Looks up the IP address to publish by calling a service that returns the caller's public IP address, like ipify
	// This is for dynamic DNS: the record follows the public IP address of the network ddup runs in
	// Exactly one of `ip`, `cname` and `ipLookup` is required
	IPLookup *ConfigIPLookup `yaml:"ipLookup"`

	// Hostname to publish as a CNAME record when healthy
	// A CNAME record can't coexist with other records, so an endpoint with a `cname` must be alone in its priority
	// Exactly one of `ip`, `cname` and `ipLookup` is required
	CNAME string `yaml:"cname"`

	// If true, the record is proxied by the DNS provider (Cloudflare only)
	// All endpoints with the same priority must use the same value
	Proxied bool `yaml:"proxied"`

	// Priority of the endpoint; lower values are preferred
	// Only the healthy endpoints with the lowest priority value are published, and when none are healthy ddup falls back to the next priority
	// Endpoints with the same priority are all published together (round-robin)
	// Defaults to 0
	Priority int `yaml:"priority"`

	// Hostname to include in the requests
	// This can be used when the request is made to an IP address or to a hostname different from the desired one
	Host string `yaml:"host"`
}

// ConfigIPLookup configures how an endpoint's IP address is looked up
type ConfigIPLookup struct {
	// URLs of services that return the caller's IP address; they are tried in order, and the first that works is used
	// By default the response body is expected to contain only the IP address, as plain text
	// +required
	URLs []string `yaml:"urls"`

	// Expected IP version, 4 or 6; a response with the other version is an error
	// This is how you choose between an A and an AAAA record, as the service you call decides which version it sees
	// If empty, any version is accepted
	Family int `yaml:"family"`

	// Optional regular expression with exactly one capture group, to extract the IP address from the response (for example from JSON)
	// Example: `"ip":"([^"]+)"`
	Pattern string `yaml:"pattern"`
}

// Dynamic returns true if the IP address of the endpoint is looked up
func (e *ConfigEndpoint) Dynamic() bool {
	return e.IPLookup != nil
}

// Target returns what the endpoint publishes in DNS: its IP address, or its CNAME hostname
// It's empty for endpoints that look up their IP address, as it's not known until the lookup runs
func (e *ConfigEndpoint) Target() string {
	if e.CNAME != "" {
		return e.CNAME
	}
	return e.IP
}

type ConfigProvider struct {
	// Config for the Cloudflare provider
	Cloudflare *CloudflareConfig `yaml:"cloudflare"`
	// Config for the OVH provider
	OVH *OVHConfig `yaml:"ovh"`
	// Config for the Azure DNS provider
	Azure *AzureConfig `yaml:"azure"`
}

// CloudflareConfig represents Cloudflare-specific configuration
type CloudflareConfig struct {
	APIToken string `yaml:"apiToken"`
	ZoneID   string `yaml:"zoneId"`
}

// OVHConfig represents OVH-specific configuration
type OVHConfig struct {
	APIKey      string `yaml:"apiKey"`
	APISecret   string `yaml:"apiSecret"`
	ConsumerKey string `yaml:"consumerKey"`
	ZoneName    string `yaml:"zoneName"`
	// OVH API endpoint (defaults to EU if not specified)
	// Valid values: "eu", "ca", "us" or full URL
	Endpoint string `yaml:"endpoint,omitempty"`
}

// AzureConfig represents Azure DNS-specific configuration
type AzureConfig struct {
	SubscriptionID    string `yaml:"subscriptionId"`
	ResourceGroupName string `yaml:"resourceGroupName"`
	ZoneName          string `yaml:"zoneName"`
	TenantID          string `yaml:"tenantId"`
	// Client ID for authenticating with a service principal
	ClientID string `yaml:"clientId,omitempty"`
	// Client secret for authenticating with a service principal
	ClientSecret string `yaml:"clientSecret,omitempty"`
	// Managed identity client ID for authenticating with a user-assigned managed identity
	ManagedIdentityClientID string `yaml:"managedIdentityClientId,omitempty"`
}

// ConfigLogs represents logging configuration
type ConfigLogs struct {
	// Controls log level and verbosity. Supported values: `debug`, `info` (default), `warn`, `error`.
	// +default "info"
	Level string `yaml:"level"`

	// If true, emits logs formatted as JSON, otherwise uses a text-based structured log format.
	// Defaults to false if a TTY is attached (e.g. when running the binary directly in the terminal or in development); true otherwise.
	JSON bool `yaml:"json"`
}

// ConfigServer represents server configuration
type ConfigServer struct {
	// Enable the server
	// +default false
	Enabled bool `yaml:"enabled"`

	// Address to bind to
	// +default "127.0.0.1"
	Bind string `yaml:"bind"`

	// Port to listen on
	// +default 7401
	Port int `yaml:"port"`
}

// ConfigDev includes options using during development only
type ConfigDev struct {
	// If true, enables CORS from anywhere
	// This is used by the dashboarddev mode
	EnableCORS bool
}

// Internal properties
type internal struct {
	instanceID       string
	configFileLoaded string // Path to the config file that was loaded
}

// String implements fmt.Stringer and prints out the config for debugging
func (c *Config) String() string {
	//nolint:errchkjson,musttag
	enc, _ := json.Marshal(c)
	return string(enc)
}

// GetLoadedConfigPath returns the path to the config file that was loaded
func (c *Config) GetLoadedConfigPath() string {
	return c.internal.configFileLoaded
}

// SetLoadedConfigPath sets the path to the config file that was loaded
func (c *Config) SetLoadedConfigPath(filePath string) {
	c.internal.configFileLoaded = filePath
}

// GetInstanceID returns the instance ID.
func (c *Config) GetInstanceID() string {
	return c.internal.instanceID
}

// Validate the configuration and performs some sanitization
func (c *Config) Validate(logger *slog.Logger) error {
	// Ensure that at least one provider is configured
	if len(c.Providers) == 0 {
		return errors.New("at least one provider must be configured")
	}

	// Validate the providers
	for name, p := range c.Providers {
		// Ensure that one and only one provider is configured
		count := countSetProperties(p)
		if count != 1 {
			return fmt.Errorf("provider '%s' is invalid: exactly one provider must be configured", name)
		}
	}

	// Require at least one domain to be configured
	if len(c.Domains) == 0 {
		return errors.New("no domains configured; specify at least one domain under 'domains'")
	}

	// Validate domains
	for di := range c.Domains {
		// Use a pointer so that defaults and sanitization are persisted
		d := &c.Domains[di]
		if d.RecordName == "" {
			return fmt.Errorf("domain %d is invalid: recordName is empty", di)
		}
		if len(d.Endpoints) == 0 {
			return fmt.Errorf("domain %s is invalid: endpoints list is empty", d.RecordName)
		}
		if d.Provider == "" {
			return fmt.Errorf("domain %d is invalid: provider is empty", di)
		}

		// Ensure the provider exists
		_, ok := c.Providers[d.Provider]
		if !ok {
			return fmt.Errorf("domain %d is invalid: provider '%s' does not exist in the provider configuration", di, d.Provider)
		}

		// Default TTL is 120s
		if d.TTL <= 0 {
			d.TTL = 120
		}

		// Validate the health check settings
		d.HealthChecks.Method = strings.ToUpper(d.HealthChecks.Method)
		switch d.HealthChecks.Method {
		case "", http.MethodGet, http.MethodHead:
		default:
			return fmt.Errorf("domain %s is invalid: healthChecks.method must be GET or HEAD", d.RecordName)
		}
		err := validateExpectStatus(d.HealthChecks.ExpectStatus)
		if err != nil {
			return fmt.Errorf("domain %s is invalid: healthChecks.%w", d.RecordName, err)
		}
		if d.HealthChecks.RecoverAfter < 0 {
			return fmt.Errorf("domain %s is invalid: healthChecks.recoverAfter must not be negative", d.RecordName)
		}

		// Validate endpoints for this domain
		err = c.validateEndpoints(d)
		if err != nil {
			return err
		}
	}

	return c.validateWebhooks()
}

func (c *Config) validateEndpoints(d *ConfigDomain) error {
	// Features only the Cloudflare provider supports
	cloudflare := c.Providers[d.Provider].Cloudflare != nil

	targets := make(map[string]struct{}, len(d.Endpoints))
	type tier struct {
		count   int
		cname   bool
		proxied bool
	}
	tiers := make(map[int]*tier)

	for ei, v := range d.Endpoints {
		if v.URL == "" && !v.Dynamic() {
			return fmt.Errorf("domain %s endpoint %d is invalid: URL is empty", d.RecordName, ei)
		}
		var kinds int
		for _, set := range []bool{v.IP != "", v.CNAME != "", v.Dynamic()} {
			if set {
				kinds++
			}
		}
		if kinds == 0 {
			return fmt.Errorf("domain %s endpoint %d is invalid: one of ip, cname and ipLookup is required", d.RecordName, ei)
		}
		if kinds > 1 {
			return fmt.Errorf("domain %s endpoint %d is invalid: ip, cname and ipLookup are mutually exclusive", d.RecordName, ei)
		}
		if v.Dynamic() {
			err := v.IPLookup.validate()
			if err != nil {
				return fmt.Errorf("domain %s endpoint %d is invalid: ipLookup %w", d.RecordName, ei, err)
			}
		}
		if v.Priority < 0 {
			return fmt.Errorf("domain %s endpoint %d is invalid: priority must not be negative", d.RecordName, ei)
		}

		if v.IP != "" {
			ip, err := netip.ParseAddr(v.IP)
			if err != nil {
				return fmt.Errorf("domain %s endpoint %d is invalid: IP %q is not a valid IPv4 or IPv6 address", d.RecordName, ei, v.IP)
			}
			v.IP = ip.String()
		} else if v.CNAME != "" {
			cname, err := normalizeHostname(v.CNAME)
			if err != nil {
				return fmt.Errorf("domain %s endpoint %d is invalid: cname %w", d.RecordName, ei, err)
			}
			v.CNAME = cname
		}

		if (v.CNAME != "" || v.Proxied) && !cloudflare {
			return fmt.Errorf("domain %s endpoint %d is invalid: cname and proxied are only supported by the Cloudflare provider", d.RecordName, ei)
		}

		// The target of endpoints that look up their IP address is not known yet
		if !v.Dynamic() {
			_, dup := targets[v.Target()]
			if dup {
				return fmt.Errorf("domain %s endpoint %d is invalid: %s is used by more than one endpoint", d.RecordName, ei, v.Target())
			}
			targets[v.Target()] = struct{}{}
		}

		if v.Name == "" {
			v.Name = v.URL
			if v.Name == "" {
				v.Name = v.IPLookup.URLs[0]
			}
		}

		t := tiers[v.Priority]
		if t == nil {
			t = &tier{proxied: v.Proxied}
			tiers[v.Priority] = t
		}
		t.count++
		t.cname = t.cname || v.CNAME != ""
		if t.proxied != v.Proxied {
			return fmt.Errorf("domain %s endpoint %d is invalid: endpoints with priority %d must all have the same value for proxied", d.RecordName, ei, v.Priority)
		}
	}

	for priority, t := range tiers {
		if t.cname && t.count > 1 {
			return fmt.Errorf("domain %s is invalid: priority %d has a cname endpoint and other endpoints, but a CNAME record can't coexist with other records; give the cname endpoint its own priority", d.RecordName, priority)
		}
	}

	return nil
}

func (l *ConfigIPLookup) validate() error {
	if len(l.URLs) == 0 {
		return errors.New("requires at least one URL")
	}
	for _, raw := range l.URLs {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("URL %q is not an absolute http(s) URL", raw)
		}
	}
	if l.Family != 0 && l.Family != 4 && l.Family != 6 {
		return errors.New("family must be 4 or 6")
	}
	if l.Pattern != "" {
		re, err := regexp.Compile(l.Pattern)
		if err != nil {
			return fmt.Errorf("pattern is invalid: %w", err)
		}
		if re.NumSubexp() != 1 {
			return errors.New("pattern must have exactly one capture group")
		}
	}
	return nil
}

var hostnameLabelRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{0,61}[a-z0-9])?$`)

// normalizeHostname lowercases a hostname, removes the trailing dot, and checks that it's valid and isn't an IP address
func normalizeHostname(name string) (string, error) {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || len(name) > 253 {
		return "", fmt.Errorf("%q is not a valid hostname", name)
	}
	_, err := netip.ParseAddr(name)
	if err == nil {
		return "", fmt.Errorf("%q is an IP address; use ip instead", name)
	}
	for label := range strings.SplitSeq(name, ".") {
		if !hostnameLabelRegexp.MatchString(label) {
			return "", fmt.Errorf("%q is not a valid hostname", name)
		}
	}
	return name, nil
}

// WebhookTemplateFuncs are the functions available in webhook body and header templates
var WebhookTemplateFuncs = template.FuncMap{
	"join": strings.Join,
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
}

// WebhookEvents is the list of valid webhook event names
var WebhookEvents = []string{"dns_updated", "dns_update_failed", "all_unhealthy"}

func (c *Config) validateWebhooks() error {
	for i := range c.Webhooks {
		w := &c.Webhooks[i]
		u, err := url.Parse(w.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("webhook %d is invalid: url must be an absolute http(s) URL", i)
		}
		if w.Name == "" {
			w.Name = u.Host
		}
		w.Method = strings.ToUpper(w.Method)
		if w.Method == "" {
			w.Method = http.MethodPost
		}
		if w.Timeout <= 0 {
			w.Timeout = 10 * time.Second
		}
		if w.Attempts <= 0 {
			w.Attempts = 5
		}
		for _, ev := range w.Events {
			if !slices.Contains(WebhookEvents, ev) {
				return fmt.Errorf("webhook %q is invalid: unknown event %q (valid: %s)", w.Name, ev, strings.Join(WebhookEvents, ", "))
			}
		}
		// Templates are parsed again at runtime; this catches syntax errors early
		_, err = template.New("body").Funcs(WebhookTemplateFuncs).Parse(w.Body)
		if err != nil {
			return fmt.Errorf("webhook %q is invalid: body template: %w", w.Name, err)
		}
		for k, v := range w.Headers {
			_, err = template.New(k).Funcs(WebhookTemplateFuncs).Parse(v)
			if err != nil {
				return fmt.Errorf("webhook %q is invalid: header %q template: %w", w.Name, k, err)
			}
		}
	}
	return nil
}

func countSetProperties(s any) int {
	typ := reflect.TypeOf(s)
	val := reflect.ValueOf(s)

	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
		val = val.Elem()
	}
	if typ.Kind() != reflect.Struct {
		// Indicates a development-time error
		panic("param must be a struct")
	}

	var count int
	for _, field := range val.Fields() {
		if field.IsValid() && !field.IsZero() {
			count++
		}
	}

	return count
}
