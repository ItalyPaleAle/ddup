package healthcheck

import (
	"github.com/italypaleale/ddup/pkg/config"
)

// endpointPriorities returns the priority of each endpoint, by target (the IP address or the CNAME hostname)
func endpointPriorities(endpoints []*config.ConfigEndpoint) map[string]int {
	res := make(map[string]int, len(endpoints))
	for _, ep := range endpoints {
		res[ep.Target()] = ep.Priority
	}
	return res
}

// selectLowestPriority returns the healthy targets that should be published: the ones with the lowest priority value
// IPs that are not in the priorities map count as priority 0
func selectLowestPriority(priorities map[string]int, healthy []string) []string {
	res := make([]string, 0, len(healthy))
	var lowest int
	for i, ip := range healthy {
		priority := priorities[ip]
		switch {
		case i == 0 || priority < lowest:
			lowest = priority
			res = append(res[:0], ip)
		case priority == lowest:
			res = append(res, ip)
		}
	}
	return res
}
