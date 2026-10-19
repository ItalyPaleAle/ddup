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

	// Target published for each endpoint, by IP or CNAME hostname
	targets map[string]dns.Target

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
