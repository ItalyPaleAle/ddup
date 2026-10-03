package dns

import (
	"context"
	"errors"
	"fmt"
	"net/netip"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// Target is something a DNS record points to: an IP address (A or AAAA record) or a hostname (CNAME record)
type Target struct {
	// IP address or hostname
	Value string
	// If true, the record is proxied by the provider (supported by Cloudflare only)
	Proxied bool
}

// RecordType returns the type of record for this target: A, AAAA or CNAME
func (t Target) RecordType() string {
	parsed, err := netip.ParseAddr(t.Value)
	switch {
	case err != nil:
		return recordTypeCNAME
	case parsed.Is4():
		return recordTypeA
	default:
		return recordTypeAAAA
	}
}

// IPTargets returns plain (not proxied) targets for IP addresses
func IPTargets(ips ...string) []Target {
	targets := make([]Target, len(ips))
	for i, ip := range ips {
		targets[i] = Target{Value: ip}
	}
	return targets
}

// ErrUnsupportedTarget is returned by providers that can't publish a kind of target, such as CNAME records or proxied records
var ErrUnsupportedTarget = errors.New("target is not supported by this DNS provider")

// ipsFromTargets returns the IP addresses for providers that only support plain A/AAAA records
func ipsFromTargets(provider string, targets []Target) ([]string, error) {
	ips := make([]string, len(targets))
	for i, t := range targets {
		if t.Proxied || t.RecordType() == recordTypeCNAME {
			return nil, fmt.Errorf("%w: provider %s only supports A and AAAA records that are not proxied, got %q", ErrUnsupportedTarget, provider, t.Value)
		}
		ips[i] = t.Value
	}
	return ips, nil
}

// UpdateResult describes the outcome of a successful UpdateRecords call
type UpdateResult struct {
	// True if any record was created, changed or deleted
	// False if the records already matched the desired IPs
	Changed bool
	// Sorted, canonical IPs and hostnames that were in DNS before the update
	Previous []string
}

// Provider defines the interface for DNS providers
type Provider interface {
	// Name returns the provider's name
	Name() string
	// UpdateRecords updates DNS records for the given domain so they point to the targets
	// Providers only write the difference with the existing records, and report whether there was any
	UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (UpdateResult, error)
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
	default:
		// Indicates a development-time error
		panic("invalid provider")
	}
}
