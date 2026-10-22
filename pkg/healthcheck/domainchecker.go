package healthcheck

import (
	"maps"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
)

type domainChecker struct {
	lock        sync.Mutex
	checker     checker.Checker
	ttl         int
	healthyIPs  []string
	failedIPs   map[string]int
	provider    dns.Provider
	lastUpdated time.Time
	lastError   string

	// Consecutive successful checks for endpoints that were removed and are recovering
	recovering map[string]int

	// Priority of each endpoint, by IP
	priorities map[string]int

	// Priority of the endpoints that look up their IP address, by the address they found
	dynamicPriorities map[string]int

	// True if no endpoint is healthy, and the all_unhealthy notification was sent
	allUnhealthy bool

	// Error of the latest failed DNS update, and the dns_update_failed notification was sent
	notifiedError string

	// True if the last update of the DNS records failed, so the next check tries again even if the published IPs did not change
	// Only used by the health check loop
	updatePending bool
}

func (dc *domainChecker) getState() (healthyIPs []string, failedIPs map[string]int, lastUpdated time.Time, lastError string) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	return dc.healthyIPs, dc.failedIPs, dc.lastUpdated, dc.lastError
}

func (dc *domainChecker) setState(healthyIPs []string, failedIPs map[string]int) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	dc.healthyIPs = healthyIPs
	dc.failedIPs = failedIPs
	dc.lastUpdated = time.Now()
	dc.lastError = ""
}

func (dc *domainChecker) setError(err string) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	dc.lastUpdated = time.Now()
	dc.lastError = err
}

// getRecovering returns a copy of the recovery counters
func (dc *domainChecker) getRecovering() map[string]int {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	res := make(map[string]int, len(dc.recovering))
	maps.Copy(res, dc.recovering)
	return res
}

func (dc *domainChecker) setRecovering(r map[string]int) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	dc.recovering = r
}

// notePriorities records the priority of the endpoints that look up their IP address, under the address they found
// Addresses that are not in the results or in the keep lists are forgotten, so the map doesn't grow when the address changes
func (dc *domainChecker) notePriorities(results []checker.Result, keep ...[]string) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	if dc.dynamicPriorities == nil {
		dc.dynamicPriorities = make(map[string]int)
	}

	keepSet := make(map[string]struct{})
	for _, list := range keep {
		for _, ip := range list {
			keepSet[ip] = struct{}{}
		}
	}
	for _, r := range results {
		ip := resultIP(r)
		if ip == "" {
			continue
		}
		keepSet[ip] = struct{}{}
		if r.Endpoint.Dynamic() {
			dc.dynamicPriorities[ip] = r.Endpoint.Priority
		}
	}

	maps.DeleteFunc(dc.dynamicPriorities, func(ip string, _ int) bool {
		_, ok := keepSet[ip]
		return !ok
	})
}

// getPriorities returns the priority of each endpoint, by IP
func (dc *domainChecker) getPriorities() map[string]int {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	res := make(map[string]int, len(dc.priorities)+len(dc.dynamicPriorities))
	maps.Copy(res, dc.priorities)
	maps.Copy(res, dc.dynamicPriorities)
	return res
}

// resultIP returns the IP address to publish for the endpoint of a result
// Endpoints that look up their IP address report what they found, and others have a fixed address
func resultIP(r checker.Result) string {
	if r.Target != "" {
		return r.Target
	}
	if r.Endpoint.Dynamic() {
		return ""
	}
	return r.Endpoint.IP
}

// dedupeResults keeps one result per IP address
// Endpoints that look up their IP address can find the same one as another endpoint, and an address must be published only once
// If one of the results is healthy it wins; otherwise the first is kept
func dedupeResults(results []checker.Result) []checker.Result {
	index := make(map[string]int, len(results))
	out := make([]checker.Result, 0, len(results))
	for _, r := range results {
		ip := resultIP(r)

		// Results without an address are kept as they are, since they can't clash with anything
		if ip == "" {
			out = append(out, r)
			continue
		}

		i, ok := index[ip]
		switch {
		case !ok:
			index[ip] = len(out)
			out = append(out, r)
		case r.Healthy && !out[i].Healthy:
			out[i] = r
		}
	}
	return out
}

// forgetOtherIPs removes from the maps the IP addresses that are not in the results
func forgetOtherIPs(results []checker.Result, states ...map[string]int) {
	current := make(map[string]struct{}, len(results))
	for _, r := range results {
		current[resultIP(r)] = struct{}{}
	}

	for _, m := range states {
		maps.DeleteFunc(m, func(ip string, _ int) bool {
			_, ok := current[ip]
			return !ok
		})
	}
}

// swapAllUnhealthy sets whether the domain has no healthy endpoints, and returns the previous value
func (dc *domainChecker) swapAllUnhealthy(v bool) bool {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	prev := dc.allUnhealthy
	dc.allUnhealthy = v
	return prev
}

// swapNotifiedError sets the error of the latest failed DNS update (empty if the update succeeded), and returns the previous value
func (dc *domainChecker) swapNotifiedError(v string) string {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	prev := dc.notifiedError
	dc.notifiedError = v
	return prev
}
