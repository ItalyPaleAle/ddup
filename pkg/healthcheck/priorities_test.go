package healthcheck

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
)

func TestSelectLowestPriority(t *testing.T) {
	priorities := map[string]int{
		"1.1.1.1": 0,
		"2.2.2.2": 0,
		"3.3.3.3": 1,
		"4.4.4.4": 1,
		"5.5.5.5": 2,
	}

	tests := []struct {
		name     string
		healthy  []string
		expected []string
	}{
		{name: "nothing healthy", healthy: []string{}, expected: []string{}},
		{name: "single endpoint", healthy: []string{"1.1.1.1"}, expected: []string{"1.1.1.1"}},
		{name: "single tier", healthy: []string{"1.1.1.1", "2.2.2.2"}, expected: []string{"1.1.1.1", "2.2.2.2"}},
		{name: "preferred tier wins", healthy: []string{"1.1.1.1", "3.3.3.3", "5.5.5.5"}, expected: []string{"1.1.1.1"}},
		{name: "equal priorities are published together", healthy: []string{"3.3.3.3", "4.4.4.4", "5.5.5.5"}, expected: []string{"3.3.3.3", "4.4.4.4"}},
		{name: "fallback when the preferred tier is unhealthy", healthy: []string{"3.3.3.3", "5.5.5.5"}, expected: []string{"3.3.3.3"}},
		{name: "fallback to the last tier", healthy: []string{"5.5.5.5"}, expected: []string{"5.5.5.5"}},
		{name: "order of the input does not matter", healthy: []string{"5.5.5.5", "4.4.4.4", "2.2.2.2", "3.3.3.3"}, expected: []string{"2.2.2.2"}},
		{name: "unknown endpoints count as priority 0", healthy: []string{"9.9.9.9", "3.3.3.3"}, expected: []string{"9.9.9.9"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, selectLowestPriority(priorities, tc.healthy))
		})
	}

	t.Run("no priorities configured", func(t *testing.T) {
		assert.Equal(t, []string{"1.1.1.1", "3.3.3.3"}, selectLowestPriority(nil, []string{"1.1.1.1", "3.3.3.3"}))
	})
}

func TestEndpointPriorities(t *testing.T) {
	res := endpointPriorities([]*config.ConfigEndpoint{
		{IP: "1.1.1.1"},
		{IP: "2.2.2.2", Priority: 3},
	})
	assert.Equal(t, map[string]int{"1.1.1.1": 0, "2.2.2.2": 3}, res)
}

func TestHealthChecker_Priorities(t *testing.T) {
	down := errors.New("down")

	newTest := func(maxAttempts, recoverAfter int) (*HealthChecker, *domainChecker, *checker.MockChecker, *dns.MockProvider, []*config.ConfigEndpoint) {
		provider := dns.NewMockProvider(false)
		endpoints := []*config.ConfigEndpoint{
			{Name: "primary", IP: "1.1.1.1"},
			{Name: "primary2", IP: "2.2.2.2"},
			{Name: "backup", IP: "3.3.3.3", Priority: 1},
		}
		mc := &checker.MockChecker{
			Domain:       "example.com",
			MaxAttempts:  maxAttempts,
			RecoverAfter: recoverAfter,
		}
		dc := &domainChecker{
			checker:    mc,
			ttl:        60,
			priorities: endpointPriorities(endpoints),
			failedIPs:  make(map[string]int),
			provider:   provider,
		}
		hc := &HealthChecker{
			domainCheckers: map[string]*domainChecker{"example.com": dc},
		}
		return hc, dc, mc, provider, endpoints
	}

	setResults := func(mc *checker.MockChecker, endpoints []*config.ConfigEndpoint, healthy ...bool) {
		mc.Results = make([]checker.Result, len(endpoints))
		for i, ep := range endpoints {
			mc.Results[i] = checker.Result{Endpoint: ep, Healthy: healthy[i]}
			if !healthy[i] {
				mc.Results[i].Error = down
			}
		}
	}

	t.Run("publishes only the preferred tier", func(t *testing.T) {
		hc, dc, mc, provider, endpoints := newTest(1, 0)
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())

		assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, provider.LastIPs)
		assert.Equal(t, 1, provider.CallCount)

		// All healthy endpoints are still tracked, including the standby one
		assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}, dc.healthyIPs)
	})

	t.Run("fails over and goes back", func(t *testing.T) {
		hc, _, mc, provider, endpoints := newTest(1, 0)
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())

		// A single preferred endpoint going down does not change the tier
		setResults(mc, endpoints, true, false, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"1.1.1.1"}, provider.LastIPs)
		assert.Equal(t, 2, provider.CallCount)

		// The whole preferred tier is down
		setResults(mc, endpoints, false, false, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"3.3.3.3"}, provider.LastIPs)
		assert.Equal(t, 3, provider.CallCount)

		// The standby endpoint going down and back up does not change what is published while the preferred tier is healthy
		setResults(mc, endpoints, false, true, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"2.2.2.2"}, provider.LastIPs)
		assert.Equal(t, 4, provider.CallCount)
		setResults(mc, endpoints, false, true, false)
		hc.checkAndUpdateDNS(t.Context())
		setResults(mc, endpoints, false, true, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, 4, provider.CallCount, "no DNS update when only the standby endpoint changes")

		// Everything is back
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, provider.LastIPs)
		assert.Equal(t, 5, provider.CallCount)
	})

	t.Run("retries delay the failover", func(t *testing.T) {
		hc, _, mc, provider, endpoints := newTest(2, 0)
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())

		// The preferred tier fails once: still within attempts, so it stays published
		setResults(mc, endpoints, false, false, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, provider.LastIPs)
		assert.Equal(t, 1, provider.CallCount)

		// A second failure exhausts the attempts
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"3.3.3.3"}, provider.LastIPs)
		assert.Equal(t, 2, provider.CallCount)
	})

	t.Run("recoverAfter delays going back to the preferred tier", func(t *testing.T) {
		hc, dc, mc, provider, endpoints := newTest(1, 3)
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())

		setResults(mc, endpoints, false, false, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"3.3.3.3"}, provider.LastIPs)
		assert.Equal(t, 2, provider.CallCount)

		// The preferred endpoints are up again but must pass 3 consecutive checks, so the backup stays published
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"3.3.3.3"}, provider.LastIPs)
		assert.Equal(t, []string{"3.3.3.3"}, dc.healthyIPs)
		assert.Equal(t, 2, provider.CallCount)

		hc.checkAndUpdateDNS(t.Context())
		assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, provider.LastIPs)
		assert.Equal(t, 3, provider.CallCount)
	})

	t.Run("recoverAfter applies to each endpoint of the preferred tier", func(t *testing.T) {
		hc, _, mc, provider, endpoints := newTest(1, 2)
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())

		setResults(mc, endpoints, false, false, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"3.3.3.3"}, provider.LastIPs)

		// Only one preferred endpoint comes back; it is enough to leave the backup once it passes recoverAfter
		setResults(mc, endpoints, true, false, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"3.3.3.3"}, provider.LastIPs)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"1.1.1.1"}, provider.LastIPs)
	})

	t.Run("nothing healthy leaves DNS unchanged", func(t *testing.T) {
		hc, dc, mc, provider, endpoints := newTest(1, 0)
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, 1, provider.CallCount)

		setResults(mc, endpoints, false, false, false)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, 1, provider.CallCount, "no DNS update when nothing is healthy")
		assert.Empty(t, dc.healthyIPs)

		// The preferred tier recovers: it's published again
		setResults(mc, endpoints, false, true, false)
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, []string{"2.2.2.2"}, provider.LastIPs)
		assert.Equal(t, 2, provider.CallCount)
	})

	t.Run("a failed DNS update is retried", func(t *testing.T) {
		hc, _, mc, provider, endpoints := newTest(1, 0)
		provider.ShouldError = true
		setResults(mc, endpoints, true, true, true)
		hc.checkAndUpdateDNS(t.Context())
		hc.checkAndUpdateDNS(t.Context())
		assert.Equal(t, 2, provider.CallCount)
	})
}
