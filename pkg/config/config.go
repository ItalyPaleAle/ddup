package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
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

// ConfigEndpoint represents a single endpoint to health check
type ConfigEndpoint struct {
	// Endpoint name, used for logging purposes
	// Defaults to the URL
	Name string `yaml:"name"`

	// Health check URL
	// +required
	URL string `yaml:"url"`

	// IP address to include in DNS records when healthy
	// IPv4 addresses are published as A records and IPv6 addresses as AAAA records
	// Exactly one of `ip` and `cname` is required
	IP string `yaml:"ip"`

	// Hostname to publish in a CNAME record when healthy
	// A CNAME record can't coexist with other records, so an endpoint with `cname` must be the only one with its priority
	// Only supported by some DNS providers
	// Exactly one of `ip` and `cname` is required
	CNAME string `yaml:"cname"`

	// Optional priority of the endpoint
	// Only the healthy endpoints with the lowest priority value are published: if none of them are healthy, ddup falls back to the next priority
	// Endpoints with the same priority are all published together
	// Must be a non-negative integer
	// +default 0
	Priority int `yaml:"priority"`

	// Hostname to include in the requests
	// This can be used when the request is made to an IP address or to a hostname different from the desired one
	Host string `yaml:"host"`

	// Options that apply only when the domain uses a specific DNS provider
	ProviderOptions ConfigEndpointProviderOptions `yaml:"providerOptions"`
}

// ConfigEndpointProviderOptions contains the options for an endpoint that are specific to a DNS provider
// Each key can be used only when the domain's provider is the one it is named after
type ConfigEndpointProviderOptions struct {
	// Options for the Cloudflare provider
	Cloudflare *ConfigEndpointCloudflareOptions `yaml:"cloudflare"`
}

// ConfigEndpointCloudflareOptions contains the options for an endpoint that apply to the Cloudflare provider
type ConfigEndpointCloudflareOptions struct {
	// If true, the record is proxied by Cloudflare
	// Works with both `ip` and `cname`
	// All endpoints with the same priority must use the same value, because the records published together are either all proxied or all DNS-only
	// +default false
	Proxied bool `yaml:"proxied"`
}

// Keys returns the keys of the options that are set, which are named after the DNS providers they are for
func (o ConfigEndpointProviderOptions) Keys() []string {
	var keys []string
	if o.Cloudflare != nil {
		keys = append(keys, "cloudflare")
	}
	return keys
}

// Proxied returns true if the records for this endpoint should be proxied by the DNS provider
func (e *ConfigEndpoint) Proxied() bool {
	return e.ProviderOptions.Cloudflare != nil && e.ProviderOptions.Cloudflare.Proxied
}

// Target returns what the endpoint publishes in DNS, which is its IP address or its CNAME hostname
// It is unique among the endpoints of a domain
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
	// Config for the AWS Route53 provider
	AWSRoute53 *AWSRoute53Config `yaml:"awsRoute53"`
	// Config for the Google Cloud DNS provider
	GoogleCloudDNS *GoogleCloudDNSConfig `yaml:"googleCloudDns"`
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

// AWSRoute53Config represents AWS Route53-specific configuration
type AWSRoute53Config struct {
	// ID of the hosted zone (e.g. "Z0123456789ABCDEFGHIJ")
	HostedZoneID string `yaml:"hostedZoneId"`
	// Access key ID for authenticating with static credentials
	// If empty, uses the default AWS credential chain
	AccessKeyID string `yaml:"accessKeyId,omitempty"`
	// Secret access key for authenticating with static credentials
	SecretAccessKey string `yaml:"secretAccessKey,omitempty"`
}

// GoogleCloudDNSConfig represents Google Cloud DNS-specific configuration
type GoogleCloudDNSConfig struct {
	// ID of the Google Cloud project containing the managed zone
	ProjectID string `yaml:"projectId"`
	// Name of the managed zone resource (e.g. "my-zone"), which is not the DNS name
	ManagedZone string `yaml:"managedZone"`
	// Path to a service account key file
	// If empty, uses Application Default Credentials
	CredentialsFile string `yaml:"credentialsFile,omitempty"`
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
		for ei, v := range d.Endpoints {
			if v.URL == "" {
				return fmt.Errorf("domain %s endpoint %d is invalid: URL is empty", d.RecordName, ei)
			}
			if v.Priority < 0 {
				return fmt.Errorf("domain %s endpoint %d is invalid: priority must not be negative", d.RecordName, ei)
			}
			err = v.validateTarget(d.RecordName)
			if err != nil {
				return fmt.Errorf("domain %s endpoint %d is invalid: %w", d.RecordName, ei, err)
			}
			if v.Name == "" {
				v.Name = v.URL
			}
		}

		err = validateDomainEndpoints(d)
		if err != nil {
			return err
		}
	}

	return nil
}

// Options that set what an endpoint publishes, of which exactly one is required
var endpointTargetOptions = []string{"ip", "cname"}

// validateTarget checks what the endpoint publishes, and sanitizes it
func (e *ConfigEndpoint) validateTarget(recordName string) error {
	var count int
	for _, set := range []bool{e.IP != "", e.CNAME != ""} {
		if set {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("exactly one of %s must be set", strings.Join(endpointTargetOptions, " or "))
	}

	switch {
	case e.IP != "":
		ip, err := netip.ParseAddr(e.IP)
		if err != nil {
			return fmt.Errorf("IP %q is not a valid IPv4 or IPv6 address", e.IP)
		}
		e.IP = ip.String()
	case e.CNAME != "":
		cname, err := normalizeHostname(e.CNAME)
		if err != nil {
			return fmt.Errorf("cname %w", err)
		}
		if cname == normalizeRecordName(recordName) {
			return fmt.Errorf("cname %q is the domain's own record name", cname)
		}
		e.CNAME = cname
	}

	return nil
}

// validateDomainEndpoints checks the rules that involve all the endpoints of a domain
// It must be called after each endpoint has been validated
func validateDomainEndpoints(d *ConfigDomain) error {
	type priorityGroup struct {
		count   int
		cname   bool
		proxied bool
	}
	groups := make(map[int]*priorityGroup, len(d.Endpoints))
	targets := make(map[string]int, len(d.Endpoints))

	for ei, e := range d.Endpoints {
		prev, dup := targets[e.Target()]
		if dup {
			return fmt.Errorf("domain %s endpoint %d is invalid: %s is already used by endpoint %d", d.RecordName, ei, e.Target(), prev)
		}
		targets[e.Target()] = ei

		g := groups[e.Priority]
		if g == nil {
			g = &priorityGroup{proxied: e.Proxied()}
			groups[e.Priority] = g
		}
		g.count++
		g.cname = g.cname || e.CNAME != ""

		// A set of records is either all proxied or all DNS-only
		if g.proxied != e.Proxied() {
			return fmt.Errorf("domain %s endpoint %d is invalid: endpoints with priority %d must all use the same value for providerOptions.cloudflare.proxied", d.RecordName, ei, e.Priority)
		}
	}

	for _, priority := range slices.Sorted(maps.Keys(groups)) {
		g := groups[priority]
		if g.cname && g.count > 1 {
			return fmt.Errorf("domain %s is invalid: priority %d has an endpoint with cname and other endpoints, but a CNAME record can't coexist with other records; give the endpoint with cname its own priority", d.RecordName, priority)
		}
	}

	return nil
}

var hostnameLabelRegexp = regexp.MustCompile(`^[a-z0-9]([a-z0-9_-]{0,61}[a-z0-9])?$`)

// normalizeRecordName lowercases a record name and removes the trailing dot
func normalizeRecordName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// normalizeHostname lowercases a hostname, removes the trailing dot, and checks that it's valid and isn't an IP address
func normalizeHostname(name string) (string, error) {
	name = normalizeRecordName(name)
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
