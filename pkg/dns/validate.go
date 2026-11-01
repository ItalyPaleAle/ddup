package dns

import (
	"fmt"

	"github.com/italypaleale/ddup/pkg/config"
)

// ValidateConfig checks that the DNS providers can publish what the endpoints ask for
// It must be called after the configuration has been validated, with the providers that were created from it
func ValidateConfig(cfg *config.Config, providers map[string]Provider) error {
	for di := range cfg.Domains {
		d := &cfg.Domains[di]
		provider, ok := providers[d.Provider]
		if !ok {
			// Indicates a development-time error, since the configuration has been validated
			return fmt.Errorf("domain %s references provider '%s' that does not exist", d.RecordName, d.Provider)
		}

		for ei, e := range d.Endpoints {
			if e.CNAME != "" && !provider.HasCNAME() {
				return fmt.Errorf("domain %s endpoint %d (%s) is invalid: cname is not supported by provider '%s' (%s)", d.RecordName, ei, e.Name, d.Provider, provider.ProviderType())
			}
		}

		err := provider.ValidateProviderOptions(d)
		if err != nil {
			return fmt.Errorf("invalid provider options: %w", err)
		}
	}

	return nil
}

// rejectProviderOptions returns an error if any endpoint of the domain has provider options
// It's used by the providers that don't have any
func rejectProviderOptions(d *config.ConfigDomain, providerType string) error {
	for ei, e := range d.Endpoints {
		for _, key := range e.ProviderOptions.Keys() {
			return fmt.Errorf("domain %s endpoint %d (%s) is invalid: providerOptions.%s can only be used with a %s provider, but provider '%s' is %s", d.RecordName, ei, e.Name, key, key, d.Provider, providerType)
		}
	}

	return nil
}
