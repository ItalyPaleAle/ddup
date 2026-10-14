package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/italypaleale/ddup/pkg/iplookup"
)

// Config represents the application configuration
type Config struct {
	// Interval to perform health checks, as a duration
	// +default 30s
	Interval time.Duration `yaml:"interval"`

	// Domains allows configuring multiple domains, each with its own endpoints
	Domains []ConfigDomain `yaml:"domains"`

	// Optional list of services used to look up the public IP address of the endpoints that set `ipLookupFamily`
	// If set, it replaces the built-in list, which uses Akamai and ipify
	// The services are called over HTTPS: those for the same IP family are asked at the same time, and the first valid answer is used
	IPLookupURLs []ConfigIPLookupURL `yaml:"ipLookupURLs"` //nolint:tagliatelle // The key keeps the URLs initialism

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
	// Required, unless the endpoint sets `ipLookupFamily`: then the endpoint is not health-checked, and is healthy as long as its IP address can be looked up
	URL string `yaml:"url"`

	// IP address to include in DNS records when healthy
	// Exactly one of `ip` and `ipLookupFamily` is required
	IP string `yaml:"ip"`

	// IP version of the public IP address to look up and include in DNS records when healthy: 4 creates an A record, 6 an AAAA record
	// This is for dynamic DNS: the record follows the public IP address of the network ddup runs in
	// Exactly one of `ip` and `ipLookupFamily` is required
	IPLookupFamily int `yaml:"ipLookupFamily"`

	// Optional priority of the endpoint
	// Only the healthy endpoints with the lowest priority value are published: if none of them are healthy, ddup falls back to the next priority
	// Endpoints with the same priority are all published together
	// Must be a non-negative integer
	// +default 0
	Priority int `yaml:"priority"`

	// Hostname to include in the requests
	// This can be used when the request is made to an IP address or to a hostname different from the desired one
	Host string `yaml:"host"`
}

// ConfigIPLookupURL is a service that returns the public IP address of the caller
type ConfigIPLookupURL struct {
	// URL of the service, which must use https
	// By default the response body is expected to contain only the IP address, as plain text
	// +required
	URL string `yaml:"url"`

	// IP version the service answers for: 4 or 6
	// +required
	Family int `yaml:"family"`

	// Optional regular expression with exactly one capture group, to extract the IP address from the response (for example from JSON)
	// Example: `"ip":"([^"]+)"`
	Pattern string `yaml:"pattern"`
}

// Dynamic returns true if the IP address of the endpoint is looked up
func (e *ConfigEndpoint) Dynamic() bool {
	return e.IPLookupFamily != 0
}

// IPLookupSources returns the services used to look up IP addresses: the configured list, or the built-in one
// The configuration must have been validated
func (c *Config) IPLookupSources() ([]iplookup.Source, error) {
	if len(c.IPLookupURLs) == 0 {
		return iplookup.DefaultSources(), nil
	}

	res := make([]iplookup.Source, len(c.IPLookupURLs))
	for i, u := range c.IPLookupURLs {
		res[i] = iplookup.Source{
			URL:    u.URL,
			Family: u.Family,
		}
		if u.Pattern != "" {
			re, err := regexp.Compile(u.Pattern)
			if err != nil {
				return nil, fmt.Errorf("ipLookupURLs[%d] has an invalid pattern: %w", i, err)
			}
			res[i].Pattern = re
		}
	}
	return res, nil
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

	err := c.validateIPLookupURLs()
	if err != nil {
		return err
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
		err = validateExpectStatus(d.HealthChecks.ExpectStatus)
		if err != nil {
			return fmt.Errorf("domain %s is invalid: healthChecks.%w", d.RecordName, err)
		}
		if d.HealthChecks.RecoverAfter < 0 {
			return fmt.Errorf("domain %s is invalid: healthChecks.recoverAfter must not be negative", d.RecordName)
		}

		// Validate endpoints for this domain
		for ei, v := range d.Endpoints {
			err = v.validateAddress(c.ipLookupFamilies())
			if err != nil {
				return fmt.Errorf("domain %s endpoint %d is invalid: %w", d.RecordName, ei, err)
			}
			if v.Priority < 0 {
				return fmt.Errorf("domain %s endpoint %d is invalid: priority must not be negative", d.RecordName, ei)
			}
			if v.Name == "" {
				v.Name = v.defaultName()
			}
		}
	}

	return nil
}

// validateAddress checks how the endpoint gets the IP address to publish: a fixed `ip`, or a public address looked up for `ipLookupFamily`
// The families are those that have at least one service to look up the address
func (e *ConfigEndpoint) validateAddress(families map[int]bool) error {
	var set int
	for _, isSet := range []bool{e.IP != "", e.Dynamic()} {
		if isSet {
			set++
		}
	}
	switch {
	case set == 0:
		return errors.New("one of ip and ipLookupFamily is required")
	case set > 1:
		return errors.New("ip and ipLookupFamily are mutually exclusive")
	}

	// Endpoints that look up their address can skip the health check
	if e.URL == "" && !e.Dynamic() {
		return errors.New("URL is empty")
	}

	if e.Dynamic() {
		if e.IPLookupFamily != 4 && e.IPLookupFamily != 6 {
			return fmt.Errorf("ipLookupFamily must be 4 or 6, got %d", e.IPLookupFamily)
		}
		if !families[e.IPLookupFamily] {
			return fmt.Errorf("ipLookupFamily is %d, but there is no service for IPv%d in the list of ipLookupURLs", e.IPLookupFamily, e.IPLookupFamily)
		}
		return nil
	}

	ip, err := netip.ParseAddr(e.IP)
	if err != nil {
		return fmt.Errorf("IP %q is not a valid IPv4 or IPv6 address", e.IP)
	}
	e.IP = ip.String()
	return nil
}

// defaultName returns the name of an endpoint that doesn't set one
func (e *ConfigEndpoint) defaultName() string {
	if e.URL != "" {
		return e.URL
	}
	return fmt.Sprintf("IPv%d lookup", e.IPLookupFamily)
}

// ipLookupFamilies returns the IP versions that have at least one service in the list used for lookups
func (c *Config) ipLookupFamilies() map[int]bool {
	res := make(map[int]bool, 2)
	if len(c.IPLookupURLs) == 0 {
		for _, s := range iplookup.DefaultSources() {
			res[s.Family] = true
		}
		return res
	}
	for _, u := range c.IPLookupURLs {
		res[u.Family] = true
	}
	return res
}

// validateIPLookupURLs validates the optional list of services used for IP lookups
func (c *Config) validateIPLookupURLs() error {
	type key struct {
		url    string
		family int
	}
	seen := make(map[key]struct{}, len(c.IPLookupURLs))

	for i, u := range c.IPLookupURLs {
		// The URL can contain a token, so it's never part of an error message
		if u.URL == "" {
			return fmt.Errorf("ipLookupURLs[%d] is invalid: url is empty", i)
		}
		err := iplookup.ValidateURL(u.URL)
		if err != nil {
			return fmt.Errorf("ipLookupURLs[%d] is invalid: url %w", i, err)
		}
		if u.Family != 4 && u.Family != 6 {
			return fmt.Errorf("ipLookupURLs[%d] (%s) is invalid: family must be 4 or 6, got %d", i, iplookup.RedactURL(u.URL), u.Family)
		}
		if u.Pattern != "" {
			re, err := regexp.Compile(u.Pattern)
			if err != nil {
				return fmt.Errorf("ipLookupURLs[%d] (%s) is invalid: pattern is not a valid regular expression: %w", i, iplookup.RedactURL(u.URL), err)
			}
			if re.NumSubexp() != 1 {
				return fmt.Errorf("ipLookupURLs[%d] (%s) is invalid: pattern must have exactly one capture group", i, iplookup.RedactURL(u.URL))
			}
		}

		k := key{url: u.URL, family: u.Family}
		_, dup := seen[k]
		if dup {
			return fmt.Errorf("ipLookupURLs[%d] (%s) is invalid: the same url and family are already in the list", i, iplookup.RedactURL(u.URL))
		}
		seen[k] = struct{}{}
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
