package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
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
	// +required
	IP string `yaml:"ip"`

	// Hostname to include in the requests
	// This can be used when the request is made to an IP address or to a hostname different from the desired one
	Host string `yaml:"host"`
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

// Validates the configuration and performs some sanitization
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
		d := c.Domains[di]
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

		// Validate endpoints for this domain
		for ei, v := range d.Endpoints {
			if v.URL == "" {
				return fmt.Errorf("domain %s endpoint %d is invalid: URL is empty", d.RecordName, ei)
			}
			if v.IP == "" {
				return fmt.Errorf("domain %s endpoint %d is invalid: IP is empty", d.RecordName, ei)
			}
			if v.Name == "" {
				v.Name = v.URL
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
	for i := range val.NumField() {
		field := val.Field(i)
		if field.IsValid() && !field.IsZero() {
			count++
		}
	}

	return count
}
