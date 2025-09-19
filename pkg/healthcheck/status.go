package healthcheck

import (
	"time"
)

type DomainStatus struct {
	LastUpdated time.Time      `json:"lastUpdated,omitempty"`
	Provider    string         `json:"provider"`
	Error       string         `json:"error,omitempty"`
	Active      []string       `json:"active"`
	Failed      map[string]int `json:"failed,omitempty"`
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

func (hc HealthChecker) getStatusObject(dc *domainChecker) DomainStatus {
	healthy, unhealthy, lastUpdated, lastError := dc.getState()

	return DomainStatus{
		LastUpdated: lastUpdated,
		Provider:    dc.provider.Name(),
		Error:       lastError,
		Active:      healthy,
		Failed:      unhealthy,
	}
}
