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

	// When each endpoint last changed state and last failed, by IP; in memory only
	times map[string]*endpointTimes
	// Returns the current time; nil means time.Now
	clock func() time.Time
}

// endpointTimes tracks the state changes of one endpoint
type endpointTimes struct {
	healthy bool
	// When the endpoint last changed state; for a new endpoint, when it was first checked
	since time.Time
	// True until the endpoint changes state for the first time, so `since` is only when ddup first saw it
	sinceStart bool
	// Last time a check of the endpoint failed; zero if never
	lastFailure time.Time
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
// A failed check that is still within the allowed attempts only updates the last failure, because the endpoint is still healthy
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
		if !r.Healthy {
			t.lastFailure = now
		}
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
	dc.lock.Lock()
	defer dc.lock.Unlock()

	t := dc.times[ip]
	if t == nil {
		return nil
	}
	cp := *t
	return &cp
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
