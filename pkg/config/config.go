package config

import (
	"time"
)

// Config represents the application configuration
type Config struct {
	Interval  time.Duration    `yaml:"interval"`
	Endpoints []EndpointConfig `yaml:"endpoints"`
	DNS       DNSConfig        `yaml:"dns"`
}

// EndpointConfig represents a single endpoint to health check
type EndpointConfig struct {
	Name    string        `yaml:"name"`
	URL     string        `yaml:"url"`
	IP      string        `yaml:"ip"`
	Timeout time.Duration `yaml:"timeout"`
}

// DNSConfig represents DNS provider configuration
type DNSConfig struct {
	Provider   string           `yaml:"provider"`
	RecordName string           `yaml:"recordName"`
	RecordType string           `yaml:"recordType"`
	TTL        int              `yaml:"ttl"`
	Cloudflare CloudflareConfig `yaml:"cloudflare"`
}

// CloudflareConfig represents Cloudflare-specific configuration
type CloudflareConfig struct {
	APIToken string `yaml:"apiToken"`
	ZoneID   string `yaml:"zoneId"`
}
