package notify

import (
	"time"

	"github.com/italypaleale/ddup/pkg/config"
)

// Health of a domain, derived from the state of its endpoints
const (
	StatusHealthy   = "healthy"
	StatusWarning   = "warning"
	StatusUnhealthy = "unhealthy"
)

// Event is something that happened to a domain, and that can trigger notifications
type Event struct {
	// Type of the event
	Type config.NotificationEvent
	// Domain the event is about
	Domain string
	// Time of the event
	// If zero, the time when the event is queued is used
	Time time.Time
	// State of each endpoint after the latest health check
	Endpoints []EndpointState
	// Addresses published in DNS after the event (for dns_updated), or that ddup tried to publish (for dns_update_failed)
	Published []string
	// Addresses that were in DNS before the event
	Previous []string
	// Error that caused the event, if any
	// The notifier removes known secrets from it
	Error string
}

// EndpointState is the result of the latest health check for an endpoint
type EndpointState struct {
	Name    string
	IP      string
	Healthy bool
}

// status returns the health of the domain: healthy if all endpoints are, warning if some are, and unhealthy if none are
// A domain with an error is unhealthy
func (e Event) status() string {
	if e.Error != "" || e.Type == config.EventAllUnhealthy || len(e.Endpoints) == 0 {
		return StatusUnhealthy
	}

	var healthy int
	for _, ep := range e.Endpoints {
		if ep.Healthy {
			healthy++
		}
	}
	switch healthy {
	case len(e.Endpoints):
		return StatusHealthy
	case 0:
		return StatusUnhealthy
	default:
		return StatusWarning
	}
}
