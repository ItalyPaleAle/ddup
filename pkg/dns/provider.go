package dns

import (
	"context"
	"fmt"

	"github.com/italypaleale/ddup/pkg/config"
)

// Provider defines the interface for DNS providers
type Provider interface {
	// UpdateRecords updates DNS records for the given domain with the provided IPs
	UpdateRecords(ctx context.Context, domain string, ips []string) error
}

// NewProvider creates a new DNS provider based on the configuration
func NewProvider(cfg config.DNSConfig) (provider Provider, err error) {
	switch cfg.Provider {
	case "cloudflare":
		provider, err = NewCloudflareProvider(cfg.Cloudflare, cfg.RecordType, cfg.TTL)
		if err != nil {
			return nil, fmt.Errorf("error initializing Cloudflare provider: %w", err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("unsupported DNS provider: %s", cfg.Provider)
	}
}
