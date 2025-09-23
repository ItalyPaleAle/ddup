package healthcheck

type StatusProvider interface {
	GetAllDomainsStatus() map[string]DomainStatus
	GetDomainStatus(domain string) *DomainStatus
}
