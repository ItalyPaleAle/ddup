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

func TestGetDomainStatus(t *testing.T) {
	tests := []struct {
		name       string
		healthyIPs []string
		failedIPs  map[string]int
		expected   []DomainStatusEndpoint
	}{
		{
			name:       "all healthy",
			healthyIPs: []string{"1.1.1.1", "2.2.2.2"},
			failedIPs:  map[string]int{},
			expected: []DomainStatusEndpoint{
				{Healthy: true, IP: "1.1.1.1"},
				{Healthy: true, IP: "2.2.2.2"},
			},
		},
		{
			name:       "endpoint being retried is still healthy",
			healthyIPs: []string{"1.1.1.1", "2.2.2.2"},
			failedIPs:  map[string]int{"2.2.2.2": 1},
			expected: []DomainStatusEndpoint{
				{Healthy: true, IP: "1.1.1.1"},
				{Healthy: true, IP: "2.2.2.2", FailureCount: 1},
			},
		},
		{
			name:       "endpoint that reached max attempts is unhealthy",
			healthyIPs: []string{"1.1.1.1"},
			failedIPs:  map[string]int{"2.2.2.2": 2},
			expected: []DomainStatusEndpoint{
				{Healthy: true, IP: "1.1.1.1"},
				{Healthy: false, IP: "2.2.2.2", FailureCount: 2},
			},
		},
		{
			// Before the fix, this endpoint was missing from the status until it reached max attempts
			name:       "endpoint that was never healthy is unhealthy before max attempts",
			healthyIPs: []string{"1.1.1.1"},
			failedIPs:  map[string]int{"2.2.2.2": 1},
			expected: []DomainStatusEndpoint{
				{Healthy: true, IP: "1.1.1.1"},
				{Healthy: false, IP: "2.2.2.2", FailureCount: 1},
			},
		},
		{
			name:       "no healthy endpoints",
			healthyIPs: nil,
			failedIPs:  map[string]int{"1.1.1.1": 5, "2.2.2.2": 1},
			expected: []DomainStatusEndpoint{
				{Healthy: false, IP: "1.1.1.1", FailureCount: 5},
				{Healthy: false, IP: "2.2.2.2", FailureCount: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hc := &HealthChecker{
				domainCheckers: map[string]*domainChecker{
					"example.com": {
						checker:    &checker.MockChecker{Domain: "example.com", MaxAttempts: 2},
						healthyIPs: tc.healthyIPs,
						failedIPs:  tc.failedIPs,
						provider:   dns.NewMockProvider(false),
					},
				},
			}

			status := hc.GetDomainStatus("example.com")
			require.NotNil(t, status)
			assert.ElementsMatch(t, tc.expected, status.Endpoints)
			assert.Equal(t, "mock", status.Provider)
		})
	}

	t.Run("unknown domain", func(t *testing.T) {
		hc := &HealthChecker{domainCheckers: map[string]*domainChecker{}}
		assert.Nil(t, hc.GetDomainStatus("missing.example.com"))
	})

	t.Run("domain not checked yet", func(t *testing.T) {
		hc := &HealthChecker{
			domainCheckers: map[string]*domainChecker{
				"example.com": {checker: &checker.MockChecker{MaxAttempts: 2}, provider: dns.NewMockProvider(false)},
			},
		}

		// The dashboard reads the length of the endpoints array, so it must not be null
		enc, err := json.Marshal(hc.GetDomainStatus("example.com"))
		require.NoError(t, err)
		assert.Contains(t, string(enc), `"endpoints":[]`)
	})
}

func TestGetDomainStatus_AfterChecks(t *testing.T) {
	ep1 := &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"}
	ep2 := &config.ConfigEndpoint{Name: "endpoint2", IP: "2.2.2.2"}
	healthy := func(ep *config.ConfigEndpoint) checker.Result {
		return checker.Result{Endpoint: ep, Healthy: true}
	}
	unhealthy := func(ep *config.ConfigEndpoint) checker.Result {
		return checker.Result{Endpoint: ep, Healthy: false, Error: errors.New("down")}
	}

	newHC := func(mc *checker.MockChecker, priorities map[string]int) *HealthChecker {
		return &HealthChecker{
			domainCheckers: map[string]*domainChecker{
				"example.com": {
					checker:    mc,
					ttl:        60,
					failedIPs:  make(map[string]int),
					priorities: priorities,
					provider:   dns.NewMockProvider(false),
				},
			},
		}
	}

	t.Run("endpoint failing at startup is listed", func(t *testing.T) {
		mc := &checker.MockChecker{Domain: "example.com", MaxAttempts: 3, Results: []checker.Result{healthy(ep1), unhealthy(ep2)}}
		hc := newHC(mc, nil)

		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []DomainStatusEndpoint{
			{Healthy: true, IP: "1.1.1.1"},
			{Healthy: false, IP: "2.2.2.2", FailureCount: 1},
		}, withoutTimes(hc.GetDomainStatus("example.com").Endpoints))

		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []DomainStatusEndpoint{
			{Healthy: true, IP: "1.1.1.1"},
			{Healthy: false, IP: "2.2.2.2", FailureCount: 2},
		}, withoutTimes(hc.GetDomainStatus("example.com").Endpoints))
	})

	t.Run("recovering endpoint stays unhealthy until it is added back", func(t *testing.T) {
		mc := &checker.MockChecker{Domain: "example.com", MaxAttempts: 1, RecoverAfter: 2, Results: []checker.Result{healthy(ep1), healthy(ep2)}}
		hc := newHC(mc, nil)
		hc.checkAndUpdateDNS(t.Context())

		// The endpoint fails and is removed
		mc.Results = []checker.Result{healthy(ep1), unhealthy(ep2)}
		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []DomainStatusEndpoint{
			{Healthy: true, IP: "1.1.1.1"},
			{Healthy: false, IP: "2.2.2.2", FailureCount: 1},
		}, withoutTimes(hc.GetDomainStatus("example.com").Endpoints))

		// First successful check: still recovering
		mc.Results = []checker.Result{healthy(ep1), healthy(ep2)}
		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []DomainStatusEndpoint{
			{Healthy: true, IP: "1.1.1.1"},
			{Healthy: false, IP: "2.2.2.2", FailureCount: 1},
		}, withoutTimes(hc.GetDomainStatus("example.com").Endpoints))

		// Second successful check: added back
		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []DomainStatusEndpoint{
			{Healthy: true, IP: "1.1.1.1"},
			{Healthy: true, IP: "2.2.2.2"},
		}, withoutTimes(hc.GetDomainStatus("example.com").Endpoints))
	})

	t.Run("standby endpoint is healthy even if not published", func(t *testing.T) {
		mc := &checker.MockChecker{Domain: "example.com", MaxAttempts: 2, Results: []checker.Result{healthy(ep1), healthy(ep2)}}
		hc := newHC(mc, map[string]int{"1.1.1.1": 0, "2.2.2.2": 1})
		mockProvider := dns.NewMockProvider(false)
		hc.domainCheckers["example.com"].provider = mockProvider

		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"1.1.1.1"}, mockProvider.LastIPs)
		assert.ElementsMatch(t, []DomainStatusEndpoint{
			{Healthy: true, IP: "1.1.1.1"},
			{Healthy: true, IP: "2.2.2.2"},
		}, withoutTimes(hc.GetDomainStatus("example.com").Endpoints))
	})

	t.Run("DNS error is reported", func(t *testing.T) {
		mc := &checker.MockChecker{Domain: "example.com", MaxAttempts: 2, Results: []checker.Result{healthy(ep1)}}
		hc := newHC(mc, nil)
		hc.domainCheckers["example.com"].provider = dns.NewMockProvider(true)

		before := time.Now()
		hc.checkAndUpdateDNS(t.Context())
		status := hc.GetDomainStatus("example.com")
		assert.Contains(t, status.Error, "mock error")
		assert.False(t, status.LastUpdated.Before(before))
	})
}

// withoutTimes clears the fields about state changes, which since_test.go covers
func withoutTimes(endpoints []DomainStatusEndpoint) []DomainStatusEndpoint {
	res := make([]DomainStatusEndpoint, len(endpoints))
	for i, e := range endpoints {
		e.Since = nil
		e.SinceStart = false
		e.LastFailure = nil
		res[i] = e
	}
	return res
}

func TestGetAllDomainsStatus(t *testing.T) {
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"a.example.com": {
				checker:    &checker.MockChecker{MaxAttempts: 2},
				healthyIPs: []string{"1.1.1.1"},
				provider:   dns.NewMockProvider(false),
			},
			"b.example.com": {
				checker:   &checker.MockChecker{MaxAttempts: 2},
				failedIPs: map[string]int{"2.2.2.2": 3},
				lastError: "Error updating DNS records: boom",
				provider:  dns.NewMockProvider(false),
			},
		},
	}

	all := hc.GetAllDomainsStatus()
	require.Len(t, all, 2)
	assert.Equal(t, []DomainStatusEndpoint{{Healthy: true, IP: "1.1.1.1"}}, all["a.example.com"].Endpoints)
	assert.Empty(t, all["a.example.com"].Error)
	assert.Equal(t, []DomainStatusEndpoint{{Healthy: false, IP: "2.2.2.2", FailureCount: 3}}, all["b.example.com"].Endpoints)
	assert.Equal(t, "Error updating DNS records: boom", all["b.example.com"].Error)
}

// The dashboard depends on these field names: see the DomainStatus interface in dashboard/src/components/DomainMonitorDashboard.tsx
func TestDomainStatusJSON(t *testing.T) {
	status := DomainStatus{
		LastUpdated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Provider:    "cf",
		Error:       "boom",
		Endpoints: []DomainStatusEndpoint{
			{Healthy: true, IP: "192.0.2.1"},
			{Healthy: false, IP: "2001:db8::1", FailureCount: 3},
		},
	}
	enc, err := json.Marshal(status)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"lastUpdated": "2026-01-02T03:04:05Z",
		"provider": "cf",
		"error": "boom",
		"endpoints": [
			{"healthy": true, "ip": "192.0.2.1"},
			{"healthy": false, "ip": "2001:db8::1", "failureCount": 3}
		]
	}`, string(enc))

	// Optional fields are omitted when empty
	enc, err = json.Marshal(DomainStatus{Provider: "cf", Endpoints: []DomainStatusEndpoint{}})
	require.NoError(t, err)
	assert.JSONEq(t, `{"lastUpdated": "0001-01-01T00:00:00Z", "provider": "cf", "endpoints": []}`, string(enc))
}
