package healthcheck

import (
	"fmt"
	"strings"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

// buildLookupers returns the lookuper of each endpoint of the domain that sets `ipLookupFamily`
// The services of each endpoint are resolved once, here; endpoints with the same list of services share a lookuper
// The options are used for every lookuper; if they have no timeout, the health check timeout of the domain is used
// If no endpoint looks up its address, the result is empty and no service is ever contacted
func buildLookupers(cfg *config.Config, d config.ConfigDomain, opts iplookup.Options) map[*config.ConfigEndpoint]*iplookup.Lookuper {
	if opts.Timeout <= 0 {
		opts.Timeout = d.HealthChecks.Timeout
	}

	res := make(map[*config.ConfigEndpoint]*iplookup.Lookuper)
	byList := make(map[string]*iplookup.Lookuper)

	for _, ep := range d.Endpoints {
		if !ep.Dynamic() {
			continue
		}

		sources := cfg.IPLookupSources(ep)
		key := sourcesKey(sources)
		lookuper, ok := byList[key]
		if !ok {
			lookuper = iplookup.New(sources, opts)
			byList[key] = lookuper
		}
		res[ep] = lookuper
	}

	return res
}

// sourcesKey identifies a list of services
func sourcesKey(sources []iplookup.Source) string {
	var b strings.Builder
	for _, s := range sources {
		fmt.Fprintf(&b, "%d\x00%s\x00", s.Family, s.URL)
	}
	return b.String()
}
