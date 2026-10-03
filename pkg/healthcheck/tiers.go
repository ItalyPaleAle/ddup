package healthcheck

import (
	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
)

// publication is what should be in DNS for a set of healthy endpoints
type publication struct {
	// Targets that should be published: the healthy endpoints with the lowest priority value
	targets []dns.Target
	// Priority of the endpoints that are published
	priority int
}

// keys returns strings that identify the targets, including whether they're proxied, so publications can be compared
func (p publication) keys() []string {
	keys := make([]string, len(p.targets))
	for i, t := range p.targets {
		keys[i] = t.Value
		if t.Proxied {
			keys[i] += "|proxied"
		}
	}
	return keys
}

func (p publication) values() []string {
	values := make([]string, len(p.targets))
	for i, t := range p.targets {
		values[i] = t.Value
	}
	return values
}

// selectPublication picks what to publish for the healthy targets (IPs or CNAME hostnames)
// Only the healthy endpoints with the lowest priority value are published. Targets that don't match a known endpoint count as priority 0
func selectPublication(endpoints map[string]*config.ConfigEndpoint, healthy []string) publication {
	var pub publication
	for i, value := range healthy {
		var t dns.Target
		var priority int
		t.Value = value
		ep := endpoints[value]
		if ep != nil {
			t.Proxied = ep.Proxied
			priority = ep.Priority
		}

		switch {
		case i == 0 || priority < pub.priority:
			pub = publication{targets: []dns.Target{t}, priority: priority}
		case priority == pub.priority:
			pub.targets = append(pub.targets, t)
		}
	}
	return pub
}
