package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/healthcheck"
)

// providerWithEndpoints returns endpoints of every kind in the status
type providerWithEndpoints struct {
	fakeProvider
}

func (p *providerWithEndpoints) GetAllDomainsStatus() map[string]healthcheck.DomainStatus {
	return map[string]healthcheck.DomainStatus{
		"a.example.com": {
			Provider: "p",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{Healthy: true, IP: "192.0.2.10", Type: "A"},
				{Healthy: true, IP: "tunnel.example.net", Type: "CNAME", Proxied: true},
			},
		},
	}
}

func (p *providerWithEndpoints) GetDomainStatus(domain string) *healthcheck.DomainStatus {
	status, ok := p.GetAllDomainsStatus()[domain]
	if !ok {
		return nil
	}
	return &status
}

func TestStatusEndpointTypeAndProxied(t *testing.T) {
	s, err := NewServer(NewServerOpts{HealthChecker: &providerWithEndpoints{}})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/status", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var res map[string]struct {
		Endpoints []map[string]any `json:"endpoints"`
	}
	err = json.Unmarshal(rec.Body.Bytes(), &res)
	require.NoError(t, err)

	endpoints := res["a.example.com"].Endpoints
	require.Len(t, endpoints, 2)
	assert.Equal(t, map[string]any{"healthy": true, "ip": "192.0.2.10", "type": "A"}, endpoints[0])
	assert.Equal(t, map[string]any{"healthy": true, "ip": "tunnel.example.net", "type": "CNAME", "proxied": true}, endpoints[1])
}
