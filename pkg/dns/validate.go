package dns

import (
	"fmt"
	"slices"

	"github.com/italypaleale/ddup/pkg/config"
)

// ValidateConfig checks that the DNS providers can publish what the endpoints ask for
// It must be called after the configuration has been validated, and before the providers are created
func ValidateConfig(cfg *config.Config) error {
	for _, d := range cfg.Domains {
		providerCfg, ok := cfg.Providers[d.Provider]
		if !ok {
			// Indicates a development-time error, since the configuration has been validated
			return fmt.Errorf("domain %s references provider '%s' that does not exist", d.RecordName, d.Provider)
		}
		caps := CapabilitiesFor(&providerCfg)

		for ei, e := range d.Endpoints {
			if e.CNAME != "" && !caps.CNAME {
				return fmt.Errorf("domain %s endpoint %d (%s) is invalid: cname is not supported by provider '%s' (%s)", d.RecordName, ei, e.Name, d.Provider, caps.Type)
			}

			for _, key := range e.ProviderOptions.Keys() {
				if !slices.Contains(caps.ProviderOptions, key) {
					return fmt.Errorf("domain %s endpoint %d (%s) is invalid: providerOptions.%s can only be used with a %s provider, but provider '%s' is %s", d.RecordName, ei, e.Name, key, key, d.Provider, caps.Type)
				}
			}
		}
	}

	return nil
}
