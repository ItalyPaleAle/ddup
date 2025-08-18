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

	Endpoints []*ConfigEndpoint `yaml:"endpoints"`
	DNS       ConfigDNS         `yaml:"dns"`
	Logs      ConfigLogs        `yaml:"logs"`

	// Dev is meant for development only; it's undocumented
	Dev ConfigDev `yaml:"-"`

	// Internal keys
	internal internal `yaml:"-"`
}

// ConfigEndpoint represents a single endpoint to health check
type ConfigEndpoint struct {
	// Endpoint name, used for logging purposes
	// Defaults to the URL
	Name string `yaml:"name"`

	// Health check URL
	// +required
	URL string `yaml:"url"`

	IP string `yaml:"ip"`

	// Request timeout
	// Defaults to 5s
	Timeout time.Duration `yaml:"timeout"`
	Host    string        `yaml:"host"`
}

// ConfigDNS represents DNS provider configuration
type ConfigDNS struct {
	RecordName string `yaml:"recordName"`
	RecordType string `yaml:"recordType"`

	// TTL for the created records, in seconds
	// +default 60
	TTL int `yaml:"ttl"`

	Provider ConfigDNSProvider `yaml:"provider"`
}

type ConfigDNSProvider struct {
	// Config for the Cloudflare provider
	Cloudflare *CloudflareConfig `yaml:"cloudflare"`
}

// CloudflareConfig represents Cloudflare-specific configuration
type CloudflareConfig struct {
	APIToken string `yaml:"apiToken"`
	ZoneID   string `yaml:"zoneId"`
}

// ConfigLogs represents logging configuration
type ConfigLogs struct {
	// Controls log level and verbosity. Supported values: `debug`, `info` (default), `warn`, `error`.
	// +default "info"
	Level string `yaml:"level"`

	// If true, emits logs formatted as JSON, otherwise uses a text-based structured log format.
	// Defaults to false if a TTY is attached (e.g. in development); true otherwise.
	JSON bool `yaml:"json"`
}

// ConfigDev includes options using during development only
type ConfigDev struct {
	// Empty for now
}

// Internal properties
type internal struct {
	instanceID       string
	configFileLoaded string // Path to the config file that was loaded
}

// String implements fmt.Stringer and prints out the config for debugging
func (c *Config) String() string {
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
	// Ensure that one and only one provider is configured
	count := countSetProperties(c.DNS.Provider)
	if count != 1 {
		return errors.New("exactly one DNS provider must be configure")
	}

	// Validate all endpoints
	for i, v := range c.Endpoints {
		// Validate required fields
		if v.URL == "" {
			return fmt.Errorf("endpoint %d is invalid: URL is empty", i)
		}

		// Set the default values
		if v.Name == "" {
			v.Name = v.URL
		}
		if v.Timeout <= 0 {
			v.Timeout = 5 * time.Second
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
