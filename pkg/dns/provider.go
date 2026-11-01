package dns

import (
	"context"
	"errors"
	"fmt"
	"slices"
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
	// Only some providers can publish proxied records
	Proxied bool
}

// NewTarget returns the target that an endpoint publishes
// The type of the record depends on which key the endpoint uses: an IP address is an A or AAAA record, depending on its family, and cname is a CNAME record
func NewTarget(e *config.ConfigEndpoint) (Target, error) {
	proxied := e.ProviderOptions.Cloudflare.IsProxied()
	if e.CNAME != "" {
		return CNAMETarget(e.CNAME, proxied), nil
	}
	return IPTarget(e.IP, proxied)
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
	// ProviderType returns the key of the provider in the configuration, such as "cloudflare"
	ProviderType() string
	// HasCNAME returns true if the provider can publish CNAME records
	HasCNAME() bool
	// ValidateProviderOptions checks the `providerOptions` of the endpoints of a domain that uses this provider
	// It rejects the options meant for other providers, and applies the rules that are specific to this provider
	ValidateProviderOptions(d *config.ConfigDomain) error
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

// canonicalizeTargets validates the targets, canonicalizes their values and removes duplicates
func canonicalizeTargets(targets []Target) ([]Target, error) {
	desired := make([]Target, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	var hasCNAME bool
	for _, t := range targets {
		switch t.Type {
		case recordTypeA, recordTypeAAAA:
			ip, err := IPTarget(t.Value, t.Proxied)
			if err != nil {
				return nil, err
			}
			if ip.Type != t.Type {
				return nil, fmt.Errorf("target %q is not valid for a %s record", t.Value, t.Type)
			}
			t = ip
		case recordTypeCNAME:
			hasCNAME = true
			t.Value = canonicalizeHostname(t.Value)
		default:
			return nil, fmt.Errorf("%w: unknown record type %q", ErrUnsupportedTarget, t.Type)
		}

		key := t.String()
		_, dup := seen[key]
		if dup {
			continue
		}

		seen[key] = struct{}{}
		desired = append(desired, t)
	}

	if hasCNAME && len(desired) > 1 {
		return nil, errors.New("a CNAME target can't be published together with other targets")
	}

	return desired, nil
}

// recordSet contains the values of the records with the same name and type
type recordSet struct {
	// Type of the records: "A", "AAAA" or "CNAME"
	Type string
	// Canonical values of the records, sorted and without duplicates
	// A CNAME record set has at most one value
	Values []string
}

// recordSetTypes are the types of the record sets that providers manage, in the order they handle them
var recordSetTypes = []string{recordTypeA, recordTypeAAAA, recordTypeCNAME}

// recordSetsFromTargets groups the targets into a record set for each of the A, AAAA and CNAME types, in this order
// It's used by the providers that manage the records with the same name and type together, and that can't publish proxied records
// The record sets of the types that have no targets are empty, so the existing records of those types are deleted
func recordSetsFromTargets(provider string, targets []Target) ([]recordSet, error) {
	desired, err := canonicalizeTargets(targets)
	if err != nil {
		return nil, err
	}

	sets := make([]recordSet, len(recordSetTypes))
	for i, recordType := range recordSetTypes {
		sets[i].Type = recordType
	}

	for _, t := range desired {
		if t.Proxied {
			return nil, fmt.Errorf("%w: provider %s can only publish records that are not proxied", ErrUnsupportedTarget, provider)
		}

		// canonicalizeTargets returns an error for the other types, so the index is always valid
		i := slices.Index(recordSetTypes, t.Type)
		sets[i].Values = append(sets[i].Values, t.Value)
	}

	for i := range sets {
		slices.Sort(sets[i].Values)
	}

	return sets, nil
}

// plainIPsFromTargets returns the IP addresses for providers that can only publish A and AAAA records that are not proxied
func plainIPsFromTargets(provider string, targets []Target) ([]string, error) {
	ips := make([]string, len(targets))
	for i, t := range targets {
		if t.Type != recordTypeA && t.Type != recordTypeAAAA {
			return nil, fmt.Errorf("%w: provider %s can only publish A and AAAA records, got %s record %q", ErrUnsupportedTarget, provider, t.Type, t.Value)
		}
		if t.Proxied {
			return nil, fmt.Errorf("%w: provider %s can only publish records that are not proxied", ErrUnsupportedTarget, provider)
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
