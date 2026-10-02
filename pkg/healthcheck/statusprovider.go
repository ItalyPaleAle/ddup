package healthcheck

import "context"

type StatusProvider interface {
	GetAllDomainsStatus() map[string]DomainStatus
	GetDomainStatus(domain string) *DomainStatus
	// ForceCheck runs health checks for all domains right away, and blocks until they're done or the context is canceled
	ForceCheck(ctx context.Context) error
}
