package healthcheck

import (
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
	endpoints := make([]DomainStatusEndpoint, 0, len(healthy)+len(unhealthy))
	for _, ip := range healthy {
		endpoints = append(endpoints, DomainStatusEndpoint{
			Healthy:      true,
			IP:           ip,
			FailureCount: unhealthy[ip],
		})
	}
	for ip, attempts := range unhealthy {
		// If the number of attempts is less than the max, the endpoint was in the healthy list too
		if attempts >= dc.checker.GetMaxAttempts() {
			endpoints = append(endpoints, DomainStatusEndpoint{
				Healthy:      false,
				IP:           ip,
				FailureCount: attempts,
			})
		}
	}

	return DomainStatus{
		LastUpdated: lastUpdated,
		Provider:    dc.provider.Name(),
		Error:       lastError,
		Endpoints:   endpoints,
	}
}
