package healthcheck

import (
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
