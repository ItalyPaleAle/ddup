package healthcheck

import (
	"slices"
	"time"
)

type DomainStatus struct {
	LastUpdated time.Time              `json:"lastUpdated"`
	Provider    string                 `json:"provider"`
	Error       string                 `json:"error,omitempty"`
	Endpoints   []DomainStatusEndpoint `json:"endpoints"`
}

type DomainStatusEndpoint struct {
	Healthy      bool   `json:"healthy"`
	IP           string `json:"ip"`
	FailureCount int    `json:"failureCount,omitempty"`
	// When the endpoint last changed state, from healthy to failing or the other way around
	Since *time.Time `json:"since,omitempty"`
	// True if `since` is when ddup first checked the endpoint, not a state change, so the real start is unknown
	SinceStart bool `json:"sinceStart,omitempty"`
	// Last time a check of the endpoint failed
	LastFailure *time.Time `json:"lastFailure,omitempty"`
}

func (hc *HealthChecker) GetAllDomainsStatus() map[string]DomainStatus {
	res := make(map[string]DomainStatus, len(hc.domainCheckers))
	for name, dc := range hc.domainCheckers {
		res[name] = hc.getStatusObject(dc)
	}
	return res
}

func (hc *HealthChecker) GetDomainStatus(domain string) *DomainStatus {
	dc, ok := hc.domainCheckers[domain]
	if !ok {
		return nil
	}

	res := hc.getStatusObject(dc)
	return &res
}

func (hc *HealthChecker) getStatusObject(dc *domainChecker) DomainStatus {
	healthy, unhealthy, lastUpdated, lastError := dc.getState()

	// Endpoints in the unhealthy list could also be in the healthy one,
	// if they failed a recent health check but still less than the max attempts
	newEndpoint := func(ip string, isHealthy bool, failureCount int) DomainStatusEndpoint {
		e := DomainStatusEndpoint{
			Healthy:      isHealthy,
			IP:           ip,
			FailureCount: failureCount,
		}
		times := dc.getTimes(ip)
		if times != nil {
			e.Since = &times.since
			e.SinceStart = times.sinceStart
			if !times.lastFailure.IsZero() {
				e.LastFailure = &times.lastFailure
			}
		}
		return e
	}

	endpoints := make([]DomainStatusEndpoint, 0, len(healthy)+len(unhealthy))
	for _, ip := range healthy {
		endpoints = append(endpoints, newEndpoint(ip, true, unhealthy[ip]))
	}
	for ip, attempts := range unhealthy {
		// Endpoints that are still in the healthy list were added above
		// All others are not in DNS, including those that failed fewer than the max attempts but were never healthy, such as at startup
		if slices.Contains(healthy, ip) {
			continue
		}

		endpoints = append(endpoints, newEndpoint(ip, false, attempts))
	}

	return DomainStatus{
		LastUpdated: lastUpdated,
		Provider:    dc.provider.Name(),
		Error:       lastError,
		Endpoints:   endpoints,
	}
}
