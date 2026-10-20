package healthcheck

import (
	"slices"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	"github.com/italypaleale/ddup/pkg/notify"
)

// Notifier is implemented by objects that deliver notifications
// Notify must not block
type Notifier interface {
	Notify(ev notify.Event)
}

// SetNotifier sets the object that delivers notifications
// It must be called before Run
func (hc *HealthChecker) SetNotifier(n Notifier) {
	hc.notifier = n
}

func (hc *HealthChecker) notify(ev notify.Event) {
	if hc.notifier == nil {
		return
	}
	hc.notifier.Notify(ev)
}

// notifyEndpointStates returns the state of the endpoints for a notification
// An endpoint is healthy if it's in the list of healthy IPs, which takes into account the retries and the recovery
func notifyEndpointStates(results []checker.Result, healthyIPs []string) []notify.EndpointState {
	states := make([]notify.EndpointState, len(results))
	for i, result := range results {
		states[i] = notify.EndpointState{
			Name:    result.Endpoint.Name,
			IP:      result.Endpoint.IP,
			Healthy: slices.Contains(healthyIPs, result.Endpoint.IP),
		}
	}
	return states
}

// notifyOutage sends the all_unhealthy event when a domain has no healthy endpoints
// The event is sent once, when the domain transitions to that state, and not on every check
// The recovery is reported by the dns_updated event that follows
func (hc *HealthChecker) notifyOutage(dc *domainChecker, domain string, endpoints []notify.EndpointState, previous []string, allUnhealthy bool) {
	wasUnhealthy := dc.swapAllUnhealthy(allUnhealthy)
	if !allUnhealthy || wasUnhealthy {
		return
	}

	hc.notify(notify.Event{
		Type:      config.EventAllUnhealthy,
		Domain:    domain,
		Endpoints: endpoints,
		Previous:  previous,
	})
}

// notifyUpdateFailed sends the dns_update_failed event
// The event is sent once for each distinct error, because the update is retried at every check
func (hc *HealthChecker) notifyUpdateFailed(dc *domainChecker, domain string, endpoints []notify.EndpointState, published []string, previous []string, err error) {
	prev := dc.swapNotifiedError(err.Error())
	if prev == err.Error() {
		return
	}

	hc.notify(notify.Event{
		Type:      config.EventDNSUpdateFailed,
		Domain:    domain,
		Endpoints: endpoints,
		Published: published,
		Previous:  previous,
		Error:     err.Error(),
	})
}
