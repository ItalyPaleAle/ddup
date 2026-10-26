package healthcheck

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
)

func proxiedEndpointOptions() config.ConfigEndpointProviderOptions {
	return config.ConfigEndpointProviderOptions{
		Cloudflare: &config.ConfigEndpointCloudflareOptions{Proxied: true},
	}
}

func TestEndpointTarget(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *config.ConfigEndpoint
		expected dns.Target
	}{
		{
			name:     "IPv4 is an A record",
			endpoint: &config.ConfigEndpoint{IP: "192.0.2.10"},
			expected: dns.Target{Type: "A", Value: "192.0.2.10"},
		},
		{
			name:     "IPv6 is an AAAA record",
			endpoint: &config.ConfigEndpoint{IP: "2001:db8::10"},
			expected: dns.Target{Type: "AAAA", Value: "2001:db8::10"},
		},
		{
			name:     "proxied IP",
			endpoint: &config.ConfigEndpoint{IP: "192.0.2.10", ProviderOptions: proxiedEndpointOptions()},
			expected: dns.Target{Type: "A", Value: "192.0.2.10", Proxied: true},
		},
		{
			name:     "cname is a CNAME record",
			endpoint: &config.ConfigEndpoint{CNAME: "tunnel.example.net"},
			expected: dns.Target{Type: "CNAME", Value: "tunnel.example.net"},
		},
		{
			name:     "proxied cname",
			endpoint: &config.ConfigEndpoint{CNAME: "tunnel.example.net", ProviderOptions: proxiedEndpointOptions()},
			expected: dns.Target{Type: "CNAME", Value: "tunnel.example.net", Proxied: true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			target, err := endpointTarget(tc.endpoint)
			require.NoError(t, err)
			assert.Equal(t, tc.expected, target)
		})
	}

	t.Run("invalid IP", func(t *testing.T) {
		_, err := endpointTarget(&config.ConfigEndpoint{IP: "nope"})
		require.ErrorContains(t, err, `invalid IP address "nope"`)
	})
}

func TestTargetsMatch(t *testing.T) {
	a := dns.Target{Type: "A", Value: "192.0.2.10"}
	b := dns.Target{Type: "A", Value: "192.0.2.11"}

	assert.True(t, targetsMatch(nil, nil))
	assert.True(t, targetsMatch([]dns.Target{a, b}, []dns.Target{b, a}), "order does not matter")
	assert.False(t, targetsMatch([]dns.Target{a}, []dns.Target{a, b}))
	assert.False(t, targetsMatch([]dns.Target{a}, []dns.Target{b}))
	assert.False(t, targetsMatch([]dns.Target{a}, []dns.Target{{Type: "A", Value: "192.0.2.10", Proxied: true}}), "proxied is part of the target")
	assert.False(t, targetsMatch([]dns.Target{{Type: "CNAME", Value: "x.example.net"}}, []dns.Target{{Type: "A", Value: "x.example.net"}}), "type is part of the target")
}

// The user's setup: VPS endpoints published as plain A records, and a Cloudflare Tunnel as the last resort
func TestHealthChecker_CNAMETier(t *testing.T) {
	down := errors.New("down")

	endpoints := []*config.ConfigEndpoint{
		{Name: "vps-us", IP: "192.0.2.10"},
		{Name: "vps-eu", IP: "192.0.2.11"},
		{Name: "tunnel", CNAME: "0a1b2c3d.cfargotunnel.com", Priority: 1, ProviderOptions: proxiedEndpointOptions()},
	}
	vpsUS := dns.Target{Type: "A", Value: "192.0.2.10"}
	vpsEU := dns.Target{Type: "A", Value: "192.0.2.11"}
	tunnel := dns.Target{Type: "CNAME", Value: "0a1b2c3d.cfargotunnel.com", Proxied: true}

	provider := dns.NewMockProvider(false)
	mc := &checker.MockChecker{Domain: "example.com", MaxAttempts: 1}
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mc,
				ttl:        60,
				priorities: endpointPriorities(endpoints),
				failedIPs:  make(map[string]int),
				provider:   provider,
			},
		},
	}
	setResults := func(healthy ...bool) {
		mc.Results = make([]checker.Result, len(endpoints))
		for i, ep := range endpoints {
			mc.Results[i] = checker.Result{Endpoint: ep, Healthy: healthy[i]}
			if !healthy[i] {
				mc.Results[i].Error = down
			}
		}
	}

	// The IP endpoints are published, and the tunnel is on standby
	setResults(true, true, true)
	hc.checkAndUpdateDNS(t.Context())
	assert.ElementsMatch(t, []dns.Target{vpsUS, vpsEU}, provider.LastTargets)
	assert.Equal(t, 1, provider.CallCount)

	// The tunnel failing while on standby changes nothing
	setResults(true, true, false)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 1, provider.CallCount)

	// One VPS down: the other one is published alone
	setResults(false, true, true)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []dns.Target{vpsEU}, provider.LastTargets)
	assert.Equal(t, 2, provider.CallCount)

	// All VPS down: fail over to the tunnel, a proxied CNAME
	setResults(false, false, true)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []dns.Target{tunnel}, provider.LastTargets)
	assert.Equal(t, 3, provider.CallCount)

	// Nothing changes while the situation is the same
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 3, provider.CallCount)

	// The VPS come back: go back to the A records
	setResults(true, true, true)
	hc.checkAndUpdateDNS(t.Context())
	assert.ElementsMatch(t, []dns.Target{vpsUS, vpsEU}, provider.LastTargets)
	assert.Equal(t, 4, provider.CallCount)

	// The status keeps the target in the "ip" field, and has the type and proxied flag
	status := hc.GetDomainStatus("example.com")
	require.NotNil(t, status)
	assert.ElementsMatch(t, []DomainStatusEndpoint{
		{Healthy: true, IP: "192.0.2.10", Type: "A"},
		{Healthy: true, IP: "192.0.2.11", Type: "A"},
		{Healthy: true, IP: "0a1b2c3d.cfargotunnel.com", Type: "CNAME", Proxied: true},
	}, withoutTimes(status.Endpoints))
}

func TestHealthChecker_CNAMEFailoverWithRecoverAfter(t *testing.T) {
	endpoints := []*config.ConfigEndpoint{
		{Name: "vps", IP: "192.0.2.10"},
		{Name: "tunnel", CNAME: "tunnel.example.net", Priority: 1},
	}
	provider := dns.NewMockProvider(false)
	mc := &checker.MockChecker{Domain: "example.com", MaxAttempts: 1, RecoverAfter: 2}
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mc,
				ttl:        60,
				priorities: endpointPriorities(endpoints),
				failedIPs:  make(map[string]int),
				provider:   provider,
			},
		},
	}
	setResults := func(vpsHealthy bool) {
		mc.Results = []checker.Result{
			{Endpoint: endpoints[0], Healthy: vpsHealthy},
			{Endpoint: endpoints[1], Healthy: true},
		}
	}

	setResults(true)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, "192.0.2.10", provider.LastIPs[0])

	setResults(false)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []dns.Target{{Type: "CNAME", Value: "tunnel.example.net"}}, provider.LastTargets)

	// The VPS has to pass two checks before ddup goes back to it
	setResults(true)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 2, provider.CallCount)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 3, provider.CallCount)
	assert.Equal(t, []dns.Target{{Type: "A", Value: "192.0.2.10"}}, provider.LastTargets)
}

func TestHealthChecker_UpdateError_WithCNAME(t *testing.T) {
	endpoint := &config.ConfigEndpoint{Name: "tunnel", CNAME: "tunnel.example.net"}
	provider := dns.NewMockProvider(true)
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    &checker.MockChecker{Domain: "example.com", MaxAttempts: 1, Results: []checker.Result{{Endpoint: endpoint, Healthy: true}}},
				ttl:        60,
				priorities: endpointPriorities([]*config.ConfigEndpoint{endpoint}),
				failedIPs:  make(map[string]int),
				provider:   provider,
			},
		},
	}

	hc.checkAndUpdateDNS(t.Context())
	status := hc.GetDomainStatus("example.com")
	require.NotNil(t, status)
	assert.Equal(t, "Error updating DNS records: mock error", status.Error)
	assert.Equal(t, []dns.Target{{Type: "CNAME", Value: "tunnel.example.net"}}, provider.LastTargets)
}

func TestDomainStatusEndpointJSON(t *testing.T) {
	enc, err := json.Marshal(DomainStatus{
		LastUpdated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Provider:    "cf",
		Endpoints: []DomainStatusEndpoint{
			{Healthy: true, IP: "192.0.2.1", Type: "A"},
			{Healthy: true, IP: "2001:db8::1", Type: "AAAA", Proxied: true},
			{Healthy: false, IP: "tunnel.example.net", Type: "CNAME", Proxied: true, FailureCount: 2},
		},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"lastUpdated": "2026-01-02T03:04:05Z",
		"provider": "cf",
		"endpoints": [
			{"healthy": true, "ip": "192.0.2.1", "type": "A"},
			{"healthy": true, "ip": "2001:db8::1", "type": "AAAA", "proxied": true},
			{"healthy": false, "ip": "tunnel.example.net", "type": "CNAME", "proxied": true, "failureCount": 2}
		]
	}`, string(enc))
}
