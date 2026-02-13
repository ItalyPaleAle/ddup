package dns

import (
	"context"
	"fmt"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// Provider defines the interface for DNS providers
type Provider interface {
	// Name returns the provider's name
	Name() string
	// UpdateRecords updates DNS records for the given domain with the provided IPs
	UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error
}

// NewProvider creates a new DNS provider based on the configuration
func NewProvider(name string, cfg *config.ConfigProvider, metrics *appmetrics.AppMetrics) (provider Provider, err error) {
	// We know that only one provider will be non-nil
	switch {
	case cfg.Cloudflare != nil:
		provider, err = NewCloudflareProvider(name, cfg.Cloudflare, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing Cloudflare provider: %w", err)
		}
		return provider, nil
	case cfg.OVH != nil:
		provider, err = NewOVHProvider(name, cfg.OVH, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing OVH provider: %w", err)
		}
		return provider, nil
	case cfg.Azure != nil:
		provider, err = NewAzureProvider(name, cfg.Azure, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing Azure provider: %w", err)
		}
		return provider, nil
	case cfg.Unifi != nil:
		provider, err = NewUnifiProvider(name, cfg.Unifi, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing Unifi provider: %w", err)
		}
		return provider, nil
	default:
		// Indicates a development-time error
		panic("invalid provider")
	}
}
