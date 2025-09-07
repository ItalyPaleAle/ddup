package dns

import (
	"context"
	"fmt"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// Provider defines the interface for DNS providers
type Provider interface {
	// UpdateRecords updates DNS records for the given domain with the provided IPs
	UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error
}

// NewProvider creates a new DNS provider based on the configuration
func NewProvider(cfg *config.ConfigProvider, metrics *appmetrics.AppMetrics) (provider Provider, err error) {
	// We know that only one provider will be non-nil
	switch {
	case cfg.Cloudflare != nil:
		provider, err = NewCloudflareProvider(cfg.Cloudflare, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing Cloudflare provider: %w", err)
		}
		return provider, nil
	default:
		// Indicates a development-time error
		panic("invalid provider")
	}
}
