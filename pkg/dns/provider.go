package dns

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/italypaleale/ddup/pkg/config"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

// Target is a record that should be published for a domain
// The type of the record is explicit: it is never guessed from the value
type Target struct {
	// Type of the DNS record: "A", "AAAA" or "CNAME"
	Type string
	// IP address (for A and AAAA records) or hostname (for CNAME records)
	Value string
	// If true, the record is proxied by the provider
	// Only providers that declare support for the option in Capabilities.ProviderOptions can publish proxied records
	Proxied bool
}

// IPTarget returns the target for an IP address, which is an A record for IPv4 addresses and an AAAA record for IPv6 addresses
func IPTarget(ip string, proxied bool) (Target, error) {
	recordType, err := recordTypeForIP(ip)
	if err != nil {
		return Target{}, err
	}
	canonical, err := canonicalizeIP(ip)
	if err != nil {
		return Target{}, err
	}
	return Target{Type: recordType, Value: canonical, Proxied: proxied}, nil
}

// CNAMETarget returns the target for a hostname, which is a CNAME record
func CNAMETarget(hostname string, proxied bool) Target {
	return Target{Type: recordTypeCNAME, Value: canonicalizeHostname(hostname), Proxied: proxied}
}

// String implements fmt.Stringer
func (t Target) String() string {
	return t.Type + " " + t.Value
}

// Capabilities describes what a DNS provider can publish, beyond plain A and AAAA records
type Capabilities struct {
	// Key of the provider in the configuration, such as "cloudflare"
	Type string
	// If true, the provider can publish CNAME records
	CNAME bool
	// Keys of the endpoint's `providerOptions` that the provider accepts, such as "cloudflare"
	ProviderOptions []string
}

// ErrUnsupportedTarget is returned by providers that can't publish a target
// The configuration check normally rejects these before the application starts
var ErrUnsupportedTarget = errors.New("target is not supported by this DNS provider")

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
	// Capabilities returns what the provider can publish
	// It does not depend on the provider's credentials or on the state of the zone
	Capabilities() Capabilities
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
	case cfg.AWSRoute53 != nil:
		provider, err = NewAWSRoute53Provider(name, cfg.AWSRoute53, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing AWS Route53 provider: %w", err)
		}
		return provider, nil
	case cfg.GoogleCloudDNS != nil:
		provider, err = NewGoogleCloudDNSProvider(name, cfg.GoogleCloudDNS, metrics)
		if err != nil {
			return nil, fmt.Errorf("error initializing Google Cloud DNS provider: %w", err)
		}
		return provider, nil
	default:
		// Indicates a development-time error
		panic("invalid provider")
	}
}

// CapabilitiesFor returns the capabilities of the provider that the configuration selects
// It is used to check the configuration before any provider is created
func CapabilitiesFor(cfg *config.ConfigProvider) Capabilities {
	// We know that only one provider will be non-nil
	switch {
	case cfg.Cloudflare != nil:
		return cloudflareCapabilities
	case cfg.OVH != nil:
		return ovhCapabilities
	case cfg.Azure != nil:
		return azureCapabilities
	case cfg.AWSRoute53 != nil:
		return awsRoute53Capabilities
	case cfg.GoogleCloudDNS != nil:
		return googleCloudDNSCapabilities
	default:
		// Indicates a development-time error
		panic("invalid provider")
	}
}

// plainIPsFromTargets returns the IP addresses for providers that can only publish A and AAAA records that are not proxied
func plainIPsFromTargets(provider string, targets []Target) ([]string, error) {
	ips := make([]string, len(targets))
	for i, t := range targets {
		if (t.Type != recordTypeA && t.Type != recordTypeAAAA) || t.Proxied {
			return nil, fmt.Errorf("%w: provider %s can only publish A and AAAA records that are not proxied, got %s record %q", ErrUnsupportedTarget, provider, t.Type, t.Value)
		}
		ips[i] = t.Value
	}
	return ips, nil
}

// toFQDN returns the domain as a fully-qualified name, with a trailing dot
func toFQDN(domain string) string {
	if strings.HasSuffix(domain, ".") {
		return domain
	}
	return domain + "."
}
