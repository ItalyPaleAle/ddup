package healthcheck

import (
	"fmt"
	"strings"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

// buildLookupers returns the lookuper of each endpoint of the domain that sets `ipLookupFamily`
// The services of each endpoint are resolved once, here; endpoints with the same list of services share a lookuper
// The options are applied to every lookuper
// If no endpoint looks up its address, the result is empty and no service is ever contacted
func buildLookupers(cfg *config.Config, d config.ConfigDomain, opts ...iplookup.Option) (map[*config.ConfigEndpoint]*iplookup.Lookuper, error) {
	res := make(map[*config.ConfigEndpoint]*iplookup.Lookuper)
	byList := make(map[string]*iplookup.Lookuper)

	for _, ep := range d.Endpoints {
		if !ep.Dynamic() {
			continue
		}

		sources, err := cfg.IPLookupSources(ep)
		if err != nil {
			return nil, fmt.Errorf("endpoint '%s': %w", ep.Name, err)
		}

		key := sourcesKey(sources)
		lookuper, ok := byList[key]
		if !ok {
			lookuper = iplookup.New(sources, append([]iplookup.Option{iplookup.WithTimeout(d.HealthChecks.Timeout)}, opts...)...)
			byList[key] = lookuper
		}
		res[ep] = lookuper
	}

	return res, nil
}

// sourcesKey identifies a list of services
func sourcesKey(sources []iplookup.Source) string {
	var b strings.Builder
	for _, s := range sources {
		pattern := ""
		if s.Pattern != nil {
			pattern = s.Pattern.String()
		}
		fmt.Fprintf(&b, "%d\x00%s\x00%s\x00", s.Family, s.URL, pattern)
	}
	return b.String()
}
