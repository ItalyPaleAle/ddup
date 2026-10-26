package healthcheck

import (
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
)

type domainChecker struct {
	lock        sync.RWMutex
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

	// Target published for each endpoint, by IP or CNAME hostname
	targets map[string]dns.Target
	// When each endpoint last changed state and last failed, by IP
	times map[string]*endpointTimes
	// Returns the current time
	// nil means time.Now
	clock func() time.Time

	// True if no endpoint is healthy, and the all_unhealthy notification was sent
	allUnhealthy bool

	// Error of the latest failed DNS update, and the dns_update_failed notification was sent
	notifiedError string

	// True if the last update of the DNS records failed, so the next check tries again even if the published IPs did not change
	// Only used by the health check loop
	updatePending bool
}

// endpointTimes tracks the state changes of one endpoint
type endpointTimes struct {
	healthy bool
	// When the endpoint last changed state
	// For a new endpoint, when it was first checked
	since time.Time
	// True until the endpoint changes state for the first time, so `since` is only when ddup first saw it
	sinceStart bool
	// Last time a check of the endpoint failed
	// Zero if never
	lastFailure time.Time
	// Error of the last failed check
	lastError string
	// First failed check of the current run of failures
	// Zero and empty when the last check passed
	firstFailure time.Time
	// True if the endpoint was checked in the current cycle
	seen bool
}

func (dc *domainChecker) now() time.Time {
	if dc.clock != nil {
		return dc.clock()
	}
	return time.Now()
}

// recordCycle notes the outcome of a cycle of checks, given the IPs that are healthy after it
// A failed check that is still within the allowed attempts only updates the failure times and errors, because the endpoint is still healthy
// Endpoints that were not checked in this cycle are forgotten
func (dc *domainChecker) recordCycle(results []checker.Result, healthyIPs []string) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	now := dc.now()
	if dc.times == nil {
		dc.times = make(map[string]*endpointTimes, len(results))
	}

	for _, r := range results {
		ip := r.Endpoint.IP
		t := dc.times[ip]
		healthy := slices.Contains(healthyIPs, ip)
		if t == nil {
			t = &endpointTimes{
				healthy:    healthy,
				since:      now,
				sinceStart: true,
			}
			dc.times[ip] = t
		} else if t.healthy != healthy {
			t.healthy = healthy
			t.since = now
			t.sinceStart = false
		}

		t.seen = true
		if r.Healthy {
			t.firstFailure = time.Time{}
			continue
		}

		errMsg := ""
		if r.Error != nil {
			errMsg = r.Error.Error()
		}
		if t.firstFailure.IsZero() {
			t.firstFailure = now
		}
		t.lastFailure = now
		t.lastError = errMsg
	}

	for ip, t := range dc.times {
		if !t.seen {
			delete(dc.times, ip)
			continue
		}
		t.seen = false
	}
}

// getTimes returns a copy of the times for an endpoint, or nil if there are none
func (dc *domainChecker) getTimes(ip string) *endpointTimes {
	dc.lock.RLock()
	defer dc.lock.RUnlock()

	t := dc.times[ip]
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
}

func (dc *domainChecker) getState() (healthyIPs []string, failedIPs map[string]int, lastUpdated time.Time, lastError string) {
	dc.lock.RLock()
	defer dc.lock.RUnlock()

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
	dc.lock.RLock()
	defer dc.lock.RUnlock()

	res := make(map[string]int, len(dc.recovering))
	maps.Copy(res, dc.recovering)
	return res
}

func (dc *domainChecker) setRecovering(r map[string]int) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	dc.recovering = r
}

func (dc *domainChecker) setTargets(targets map[string]dns.Target) {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	dc.targets = targets
}

// getTargets returns the target published for each endpoint, as of the last check
func (dc *domainChecker) getTargets() map[string]dns.Target {
	dc.lock.Lock()
	defer dc.lock.Unlock()

	return dc.targets
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
