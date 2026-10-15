package healthcheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

func TestHealthChecker_AllHealthy(t *testing.T) {
	// Create mock provider that should not error
	mockProvider := dns.NewMockProvider(false)

	// Create mock endpoints
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
	}

	// Create mock health check results - all healthy
	results := []checker.Result{
		{Endpoint: endpoints[0], Healthy: true},
		{Endpoint: endpoints[1], Healthy: true},
	}

	// Create mock checker
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 2,
		Results:     results,
	}

	// Create the test HealthChecker
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{}, // Start with empty to trigger DNS update
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}

	// Run the check
	hc.checkAndUpdateDNS(t.Context())

	// Verify that healthy IPs were updated
	expectedIPs := []string{"1.1.1.1", "2.2.2.2"}
	actualIPs := hc.domainCheckers["example.com"].healthyIPs

	assert.ElementsMatch(t, expectedIPs, actualIPs, "Healthy IPs should match expected")
	assert.Empty(t, hc.domainCheckers["example.com"].failedIPs, "Expected no failed IPs")

	assert.Equal(t, 1, mockProvider.CallCount)
}

func TestHealthChecker_SomeUnhealthy(t *testing.T) {
	// Create mock provider that should not error
	mockProvider := dns.NewMockProvider(false)

	// Create mock endpoints
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
		{Name: "endpoint3", IP: "3.3.3.3"},
	}

	// Create mock health check results - some unhealthy
	results := []checker.Result{
		{Endpoint: endpoints[0], Healthy: true},
		{Endpoint: endpoints[1], Healthy: false, Error: errors.New("connection failed")},
		{Endpoint: endpoints[2], Healthy: true},
	}

	// Create mock checker
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 2,
		Results:     results,
	}

	// Create the test HealthChecker
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{}, // Start with empty to trigger DNS update
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}

	// Run the check
	hc.checkAndUpdateDNS(t.Context())

	// Verify that only healthy IPs were included
	expectedIPs := []string{"1.1.1.1", "3.3.3.3"}
	actualIPs := hc.domainCheckers["example.com"].healthyIPs

	assert.ElementsMatch(t, expectedIPs, actualIPs, "Healthy IPs should match expected")
	assert.Len(t, hc.domainCheckers["example.com"].failedIPs, 1, "Expected 1 failed IP")
	assert.Equal(t, 1, hc.domainCheckers["example.com"].failedIPs["2.2.2.2"], "Expected failed IP 2.2.2.2 to have 1 attempt")

	assert.Equal(t, 1, mockProvider.CallCount)
}

func TestHealthChecker_RetryLogic(t *testing.T) {
	// Create mock provider that should not error
	mockProvider := dns.NewMockProvider(false)

	// Create mock endpoints
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
	}

	// Create mock health check results - endpoint2 unhealthy
	results := []checker.Result{
		{Endpoint: endpoints[0], Healthy: true},
		{Endpoint: endpoints[1], Healthy: false, Error: errors.New("connection failed")},
	}

	// Create mock checker with max attempts of 3
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 3,
		Results:     results,
	}

	// Create the test HealthChecker with pre-existing healthy IPs (simulating previous state)
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{"1.1.1.1", "2.2.2.2"}, // endpoint2 was previously healthy
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}

	// First check - endpoint2 fails once but should still be considered healthy due to retry logic
	hc.checkAndUpdateDNS(t.Context())

	// Verify that both IPs are still considered healthy (retry logic)
	expectedIPs := []string{"1.1.1.1", "2.2.2.2"}
	actualIPs := hc.domainCheckers["example.com"].healthyIPs

	assert.ElementsMatch(t, expectedIPs, actualIPs, "After first failure: Healthy IPs should match expected")
	assert.Equal(t, 1, hc.domainCheckers["example.com"].failedIPs["2.2.2.2"], "Expected failed IP 2.2.2.2 to have 1 attempt")
	assert.Equal(t, 0, mockProvider.CallCount)

	// Second check - endpoint2 fails again
	hc.checkAndUpdateDNS(t.Context())
	assert.Len(t, hc.domainCheckers["example.com"].healthyIPs, 2, "After second failure: Expected 2 healthy IPs")
	assert.Equal(t, 2, hc.domainCheckers["example.com"].failedIPs["2.2.2.2"], "Expected failed IP 2.2.2.2 to have 2 attempts")
	assert.Equal(t, 0, mockProvider.CallCount)

	// Third check - endpoint2 fails a third time, should now be considered unhealthy
	hc.checkAndUpdateDNS(t.Context())
	assert.Len(t, hc.domainCheckers["example.com"].healthyIPs, 1, "After third failure: Expected 1 healthy IP")
	assert.Equal(t, "1.1.1.1", hc.domainCheckers["example.com"].healthyIPs[0], "Expected remaining healthy IP to be 1.1.1.1")
	assert.Equal(t, 3, hc.domainCheckers["example.com"].failedIPs["2.2.2.2"], "Expected failed IP 2.2.2.2 to have 3 attempts")
	assert.Equal(t, 1, mockProvider.CallCount)
}

func TestHealthChecker_AllUnhealthyNoUpdate(t *testing.T) {
	// Create mock provider that should not error
	mockProvider := dns.NewMockProvider(false)

	// Create mock endpoints
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
	}

	// Create mock health check results - all unhealthy
	results := []checker.Result{
		{Endpoint: endpoints[0], Healthy: false, Error: errors.New("connection failed")},
		{Endpoint: endpoints[1], Healthy: false, Error: errors.New("connection failed")},
	}

	// Create mock checker
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 1, // Max attempts = 1 for immediate failure
		Results:     results,
	}

	// Create the test HealthChecker
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{"1.1.1.1", "2.2.2.2"}, // Previously healthy
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}

	// Run the check
	hc.checkAndUpdateDNS(t.Context())

	// Verify that healthy IPs were updated to empty (no healthy endpoints)
	assert.Empty(t, hc.domainCheckers["example.com"].healthyIPs, "Expected 0 healthy IPs when all endpoints are unhealthy")
	assert.Len(t, hc.domainCheckers["example.com"].failedIPs, 2, "Expected 2 failed IPs")

	// Nothing should have been updated
	assert.Equal(t, 0, mockProvider.CallCount)
}

func TestHealthChecker_DNSProviderError(t *testing.T) {
	// Create mock provider that should error
	mockProvider := dns.NewMockProvider(true)

	// Create mock endpoints
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
	}

	// Create mock health check results - healthy
	results := []checker.Result{
		{Endpoint: endpoints[0], Healthy: true},
	}

	// Create mock checker
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 2,
		Results:     results,
	}

	// Create the test HealthChecker
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{}, // Start with empty to trigger DNS update
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}

	// Run the check
	hc.checkAndUpdateDNS(t.Context())

	// Verify that healthy IPs were NOT updated due to DNS provider error
	assert.Empty(t, hc.domainCheckers["example.com"].healthyIPs, "Expected healthy IPs to remain unchanged due to DNS error")
}

func TestHealthChecker_NoChangeSkipsUpdate(t *testing.T) {
	// Create mock provider that should not error
	mockProvider := dns.NewMockProvider(false)

	// Create mock endpoints
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
	}

	// Create mock health check results - all healthy
	results := []checker.Result{
		{Endpoint: endpoints[0], Healthy: true},
		{Endpoint: endpoints[1], Healthy: true},
	}

	// Create mock checker
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 2,
		Results:     results,
	}

	// Create the test HealthChecker with pre-existing healthy IPs matching the expected result
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{"1.1.1.1", "2.2.2.2"}, // Same as what will be returned
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}

	// Run the check
	hc.checkAndUpdateDNS(t.Context())

	// Verify that healthy IPs remain the same
	expectedIPs := []string{"1.1.1.1", "2.2.2.2"}
	actualIPs := hc.domainCheckers["example.com"].healthyIPs

	assert.Equal(t, 0, mockProvider.CallCount)
	assert.ElementsMatch(t, expectedIPs, actualIPs, "Healthy IPs should remain unchanged")
}

func TestHealthChecker_MultipleDomains(t *testing.T) {
	// Create mock providers
	mockProvider1 := dns.NewMockProvider(false)
	mockProvider2 := dns.NewMockProvider(false)

	// Create mock endpoints for domain 1
	endpoints1 := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
	}

	// Create mock endpoints for domain 2
	endpoints2 := []*config.ConfigEndpoint{
		{Name: "endpoint3", IP: "3.3.3.3"},
	}

	// Create mock health check results for domain 1 - mixed results
	results1 := []checker.Result{
		{Endpoint: endpoints1[0], Healthy: true},
		{Endpoint: endpoints1[1], Healthy: false, Error: errors.New("connection failed")},
	}

	// Create mock health check results for domain 2 - all healthy
	results2 := []checker.Result{
		{Endpoint: endpoints2[0], Healthy: true},
	}

	// Create mock checkers
	mockChecker1 := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 2,
		Results:     results1,
	}

	mockChecker2 := &checker.MockChecker{
		Domain:      "test.com",
		MaxAttempts: 2,
		Results:     results2,
	}

	// Create the test HealthChecker with multiple domain checkers
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker1,
				ttl:        60,
				healthyIPs: []string{},
				failedIPs:  make(map[string]int),
				provider:   mockProvider1,
			},
			"test.com": {
				checker:    mockChecker2,
				ttl:        120,
				healthyIPs: []string{},
				failedIPs:  make(map[string]int),
				provider:   mockProvider2,
			},
		},
	}

	// Run the check
	hc.checkAndUpdateDNS(t.Context())

	// Verify domain 1 results - only healthy endpoint
	expectedIPs1 := []string{"1.1.1.1"}
	actualIPs1 := hc.domainCheckers["example.com"].healthyIPs

	assert.ElementsMatch(t, expectedIPs1, actualIPs1, "Domain 1: Healthy IPs should match expected")
	assert.Equal(t, 1, hc.domainCheckers["example.com"].failedIPs["2.2.2.2"], "Domain 1: Expected failed IP 2.2.2.2 to have 1 attempt")

	expectedIPs2 := []string{"3.3.3.3"}
	actualIPs2 := hc.domainCheckers["test.com"].healthyIPs
	assert.ElementsMatch(t, expectedIPs2, actualIPs2, "Domain 2: Healthy IPs should match expected")
	assert.Empty(t, hc.domainCheckers["test.com"].failedIPs, "Domain 2: Expected no failed IPs")

	assert.Equal(t, 1, mockProvider1.CallCount)
	assert.Equal(t, 1, mockProvider2.CallCount)
}

func TestHealthChecker_RecoverAfter(t *testing.T) {
	mockProvider := dns.NewMockProvider(false)
	endpoints := []*config.ConfigEndpoint{
		{Name: "endpoint1", IP: "1.1.1.1"},
		{Name: "endpoint2", IP: "2.2.2.2"},
	}
	mockChecker := &checker.MockChecker{
		Domain:       "example.com",
		MaxAttempts:  1,
		RecoverAfter: 3,
		Results: []checker.Result{
			{Endpoint: endpoints[0], Healthy: true},
			{Endpoint: endpoints[1], Healthy: false, Error: errors.New("down")},
		},
	}
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:    mockChecker,
				ttl:        60,
				healthyIPs: []string{"1.1.1.1", "2.2.2.2"},
				failedIPs:  make(map[string]int),
				provider:   mockProvider,
			},
		},
	}
	dc := hc.domainCheckers["example.com"]

	// endpoint2 is removed after one failure
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"1.1.1.1"}, dc.healthyIPs)
	assert.Equal(t, 1, mockProvider.CallCount)

	// It comes back up, but is only re-added on the third consecutive success
	mockChecker.Results[1] = checker.Result{Endpoint: endpoints[1], Healthy: true}
	hc.checkAndUpdateDNS(t.Context())
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"1.1.1.1"}, dc.healthyIPs, "still recovering after 2 successes")
	assert.Equal(t, 1, mockProvider.CallCount)

	// A failure resets the counter
	mockChecker.Results[1] = checker.Result{Endpoint: endpoints[1], Healthy: false, Error: errors.New("down")}
	hc.checkAndUpdateDNS(t.Context())
	mockChecker.Results[1] = checker.Result{Endpoint: endpoints[1], Healthy: true}
	hc.checkAndUpdateDNS(t.Context())
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"1.1.1.1"}, dc.healthyIPs, "counter was reset by the failure")

	hc.checkAndUpdateDNS(t.Context())
	assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, dc.healthyIPs)
	assert.Equal(t, 2, mockProvider.CallCount)
	assert.Empty(t, dc.failedIPs)
}

func TestHealthChecker_FirstRunIgnoresRecoverAfter(t *testing.T) {
	mockProvider := dns.NewMockProvider(false)
	ep := &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"}
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:   &checker.MockChecker{Domain: "example.com", MaxAttempts: 2, RecoverAfter: 5, Results: []checker.Result{{Endpoint: ep, Healthy: true}}},
				ttl:       60,
				failedIPs: make(map[string]int),
				provider:  mockProvider,
			},
		},
	}
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"1.1.1.1"}, hc.domainCheckers["example.com"].healthyIPs)
}

func TestHealthChecker_ForceCheck(t *testing.T) {
	// Not running: no Run loop to handle the request
	var idle HealthChecker
	err := idle.ForceCheck(t.Context())
	require.ErrorIs(t, err, ErrNotRunning)

	mockProvider := dns.NewMockProvider(false)
	ep1 := &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"}
	mockChecker := &checker.MockChecker{Domain: "example.com", MaxAttempts: 2, Results: []checker.Result{{Endpoint: ep1, Healthy: true}}}
	hc := &HealthChecker{
		forceCh: make(chan forceRequest, 1),
		domainCheckers: map[string]*domainChecker{
			"example.com": {checker: mockChecker, ttl: 60, failedIPs: make(map[string]int), provider: mockProvider},
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	cfg := config.Get()
	prev := cfg.Interval
	cfg.Interval = time.Hour

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = hc.Run(ctx)
	}()
	t.Cleanup(func() {
		// Stop Run before restoring the shared config
		cancel()
		<-runDone
		cfg.Interval = prev
	})

	// The initial run publishes the endpoint
	require.Eventually(t, func() bool {
		return len(hc.GetDomainStatus("example.com").Endpoints) == 1
	}, 5*time.Second, 5*time.Millisecond)

	// A check that ran moments ago is reused, so a forced check returns right away without running another
	reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
	defer reqCancel()
	err = hc.ForceCheck(reqCtx)
	require.NoError(t, err)
	assert.Equal(t, 1, mockProvider.CallCount)

	// After the minimum interval, a forced check runs for real: here it finds the endpoint down and which is recorded in the state
	time.Sleep(minForceInterval + 100*time.Millisecond)
	reqCtx2, reqCancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer reqCancel2()
	mockChecker.Results = []checker.Result{{Endpoint: ep1, Healthy: false, Error: errors.New("down")}}
	before := hc.GetDomainStatus("example.com").LastUpdated
	err = hc.ForceCheck(reqCtx2)
	require.NoError(t, err)
	st := hc.GetDomainStatus("example.com")
	assert.True(t, st.LastUpdated.After(before), "a forced check updates the domain state")
	assert.Equal(t, 1, st.Endpoints[0].FailureCount)

	// A canceled context returns an error
	canceled, c2 := context.WithCancel(t.Context())
	c2()
	err = hc.ForceCheck(canceled)
	require.Error(t, err)
}

func TestNewHealthChecker(t *testing.T) {
	cfg := config.Get()
	prevDomains := cfg.Domains
	t.Cleanup(func() {
		cfg.Domains = prevDomains
	})

	cfg.Domains = []config.ConfigDomain{
		{
			RecordName:   "a.example.com",
			Provider:     "p1",
			TTL:          60,
			HealthChecks: config.ConfigHealthChecks{Attempts: 3, RecoverAfter: 2},
			Endpoints: []*config.ConfigEndpoint{
				{Name: "primary", URL: "http://192.0.2.1/health", IP: "192.0.2.1"},
				{Name: "backup", URL: "http://192.0.2.2/health", IP: "192.0.2.2", Priority: 1},
			},
		},
		{
			RecordName: "b.example.com",
			Provider:   "p2",
			TTL:        120,
			Endpoints: []*config.ConfigEndpoint{
				{Name: "only", URL: "http://192.0.2.3/health", IP: "192.0.2.3"},
			},
		},
	}

	t.Run("creates a checker for each domain", func(t *testing.T) {
		p1 := dns.NewMockProvider(false)
		p2 := dns.NewMockProvider(false)
		hc, err := NewHealthChecker(map[string]dns.Provider{"p1": p1, "p2": p2}, nil)
		require.NoError(t, err)
		require.Len(t, hc.domainCheckers, 2)
		assert.NotNil(t, hc.forceCh)

		a := hc.domainCheckers["a.example.com"]
		require.NotNil(t, a)
		assert.Same(t, p1, a.provider)
		assert.Equal(t, 60, a.ttl)
		assert.Equal(t, map[string]int{"192.0.2.1": 0, "192.0.2.2": 1}, a.priorities)
		assert.Equal(t, "a.example.com", a.checker.GetDomain())
		assert.Equal(t, 3, a.checker.GetMaxAttempts())
		assert.Equal(t, 2, a.checker.GetRecoverAfter())

		b := hc.domainCheckers["b.example.com"]
		require.NotNil(t, b)
		assert.Same(t, p2, b.provider)
		assert.Equal(t, 120, b.ttl)
		assert.Equal(t, checker.DefaultAttempts, b.checker.GetMaxAttempts())
		assert.Equal(t, 1, b.checker.GetRecoverAfter())

		// Nothing is reported before the first check
		assert.Empty(t, hc.GetDomainStatus("a.example.com").Endpoints)
	})

	t.Run("forced check before Run times out", func(t *testing.T) {
		hc, err := NewHealthChecker(map[string]dns.Provider{"p1": dns.NewMockProvider(false), "p2": dns.NewMockProvider(false)}, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		err = hc.ForceCheck(ctx)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("provider not configured", func(t *testing.T) {
		_, err := NewHealthChecker(map[string]dns.Provider{"p1": dns.NewMockProvider(false)}, nil)
		require.ErrorContains(t, err, "domain 'b.example.com' references DNS provider 'p2' that is not configured")
	})

	t.Run("nil provider", func(t *testing.T) {
		_, err := NewHealthChecker(map[string]dns.Provider{"p1": dns.NewMockProvider(false), "p2": nil}, nil)
		require.ErrorContains(t, err, "DNS provider 'p2' that is not configured")
	})
}

func TestHealthChecker_RunOnInterval(t *testing.T) {
	ep1 := &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"}
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 1,
		Results:     []checker.Result{{Endpoint: ep1, Healthy: false, Error: errors.New("down")}},
	}
	hc := &HealthChecker{
		forceCh: make(chan forceRequest, 1),
		domainCheckers: map[string]*domainChecker{
			"example.com": {checker: mockChecker, ttl: 60, failedIPs: make(map[string]int), provider: dns.NewMockProvider(false)},
		},
	}

	cfg := config.Get()
	prev := cfg.Interval
	cfg.Interval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() {
		runDone <- hc.Run(ctx)
	}()
	t.Cleanup(func() {
		// Stop Run before restoring the shared config
		cancel()
		<-runDone
		cfg.Interval = prev
	})

	// The endpoint fails every check, so its failure count is the number of checks that ran
	require.Eventually(t, func() bool {
		st := hc.GetDomainStatus("example.com")
		return len(st.Endpoints) == 1 && st.Endpoints[0].FailureCount >= 3
	}, 5*time.Second, 5*time.Millisecond)

	// Run returns without error once the context is canceled
	cancel()
	select {
	case err := <-runDone:
		require.NoError(t, err)
		// Put the result back for the cleanup function
		runDone <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was canceled")
	}
}

// newDynamicHC returns a HealthChecker for one domain whose results are set by the test
func newDynamicHC(mockChecker *checker.MockChecker, provider dns.Provider, priorities map[string]int) *HealthChecker {
	return &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"home.example.com": {
				checker:    mockChecker,
				ttl:        60,
				failedIPs:  make(map[string]int),
				priorities: priorities,
				provider:   provider,
			},
		},
	}
}

func TestHealthChecker_DynamicIP(t *testing.T) {
	mockProvider := dns.NewMockProvider(false)
	home := &config.ConfigEndpoint{Name: "home", IPLookupFamily: 4}
	mockChecker := &checker.MockChecker{Domain: "home.example.com", MaxAttempts: 2}
	hc := newDynamicHC(mockChecker, mockProvider, nil)
	dc := hc.domainCheckers["home.example.com"]

	run := func(target string, healthy bool) {
		t.Helper()
		var err error
		if !healthy {
			err = errors.New("lookup failed")
		}
		mockChecker.Results = []checker.Result{{Endpoint: home, Target: target, Healthy: healthy, Error: err}}
		hc.checkAndUpdateDNS(t.Context())
	}

	// The first lookup publishes the address
	run("203.0.113.1", true)
	assert.Equal(t, 1, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.1"}, mockProvider.LastIPs)

	// Nothing changes
	run("203.0.113.1", true)
	assert.Equal(t, 1, mockProvider.CallCount)

	// The address changes: DNS follows
	run("203.0.113.2", true)
	assert.Equal(t, 2, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.2"}, mockProvider.LastIPs)
	assert.Equal(t, []string{"203.0.113.2"}, dc.healthyIPs)
	assert.Equal(t, []DomainStatusEndpoint{{Healthy: true, IP: "203.0.113.2"}}, hc.GetDomainStatus("home.example.com").Endpoints, "the status shows the looked-up address")

	// A failed lookup is reported against the last address, and DNS is left alone while there are retries left
	run("203.0.113.2", false)
	assert.Equal(t, 2, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.2"}, dc.healthyIPs)
	assert.Equal(t, 1, dc.failedIPs["203.0.113.2"])

	// After the maximum number of attempts the address is not healthy, and with no healthy endpoint DNS is left as it is
	run("203.0.113.2", false)
	assert.Empty(t, dc.healthyIPs)
	assert.Equal(t, 2, mockProvider.CallCount)
	assert.Equal(t, []DomainStatusEndpoint{{Healthy: false, IP: "203.0.113.2", FailureCount: 2}}, hc.GetDomainStatus("home.example.com").Endpoints)

	// The lookup works again, with a new address: it's published, and the failures of the old one are forgotten
	run("203.0.113.3", true)
	assert.Equal(t, 3, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.3"}, mockProvider.LastIPs)
	assert.Empty(t, dc.failedIPs)

	// An endpoint that has never found an address is ignored, and DNS is not touched
	other := &config.ConfigEndpoint{Name: "other", IPLookupFamily: 6}
	mockChecker.Results = []checker.Result{{Endpoint: other, Healthy: false, Error: errors.New("lookup failed")}}
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 3, mockProvider.CallCount)
	assert.Empty(t, dc.failedIPs)
}

func TestHealthChecker_DynamicIP_Priorities(t *testing.T) {
	mockProvider := dns.NewMockProvider(false)
	home := &config.ConfigEndpoint{Name: "home", IPLookupFamily: 4}
	backup := &config.ConfigEndpoint{Name: "backup", IP: "198.51.100.1", Priority: 1}
	mockChecker := &checker.MockChecker{Domain: "home.example.com", MaxAttempts: 1}
	hc := newDynamicHC(mockChecker, mockProvider, endpointPriorities([]*config.ConfigEndpoint{home, backup}))
	dc := hc.domainCheckers["home.example.com"]

	mockChecker.Results = []checker.Result{
		{Endpoint: home, Target: "203.0.113.1", Healthy: true},
		{Endpoint: backup, Healthy: true},
	}
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"203.0.113.1"}, mockProvider.LastIPs, "the backup is not published while the lookup endpoint is healthy")

	// The address changes: the new address keeps the priority of the endpoint
	mockChecker.Results[0].Target = "203.0.113.2"
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"203.0.113.2"}, mockProvider.LastIPs)
	assert.Equal(t, 2, mockProvider.CallCount)

	// The lookup fails: fall back to the backup
	mockChecker.Results[0] = checker.Result{Endpoint: home, Target: "203.0.113.2", Healthy: false, Error: errors.New("lookup failed")}
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []string{"198.51.100.1"}, mockProvider.LastIPs)

	// Old addresses are forgotten, so the map doesn't grow
	for _, ip := range []string{"203.0.113.10", "203.0.113.11", "203.0.113.12", "203.0.113.13"} {
		mockChecker.Results[0] = checker.Result{Endpoint: home, Target: ip, Healthy: true}
		hc.checkAndUpdateDNS(t.Context())
	}
	assert.LessOrEqual(t, len(dc.getPriorities()), 3)
	assert.Equal(t, []string{"203.0.113.13"}, mockProvider.LastIPs)
}

func TestHealthChecker_DynamicIP_SameAddressFromTwoEndpoints(t *testing.T) {
	mockProvider := dns.NewMockProvider(false)
	a := &config.ConfigEndpoint{Name: "a", IPLookupFamily: 4}
	b := &config.ConfigEndpoint{Name: "b", IPLookupFamily: 4, URL: "https://home.example.com/health"}
	static := &config.ConfigEndpoint{Name: "static", IP: "203.0.113.1", URL: "https://static.example.com/health"}
	mockChecker := &checker.MockChecker{Domain: "home.example.com", MaxAttempts: 2}
	hc := newDynamicHC(mockChecker, mockProvider, nil)
	dc := hc.domainCheckers["home.example.com"]

	// All resolve to the same address: it must be published once
	mockChecker.Results = []checker.Result{
		{Endpoint: a, Target: "203.0.113.1", Healthy: true},
		{Endpoint: b, Target: "203.0.113.1", Healthy: true},
		{Endpoint: static, Healthy: true},
	}
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 1, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.1"}, mockProvider.LastIPs)
	assert.Equal(t, []string{"203.0.113.1"}, dc.healthyIPs)
	assert.Len(t, hc.GetDomainStatus("home.example.com").Endpoints, 1)

	// If one of the endpoints is healthy, the address is healthy
	mockChecker.Results[0].Healthy = false
	mockChecker.Results[0].Error = errors.New("lookup failed")
	hc.checkAndUpdateDNS(t.Context())
	assert.Empty(t, dc.failedIPs)
}

func TestHealthChecker_DynamicIP_EndToEnd(t *testing.T) {
	var ip atomic.Pointer[string]
	setIP := func(v string) { ip.Store(&v) }
	setIP("203.0.113.1")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(*ip.Load()))
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	lookuper := iplookup.New(
		[]iplookup.Source{{URL: srv.URL, Family: 4}},
		iplookup.Options{TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
	)

	mockProvider := dns.NewMockProvider(false)
	home := &config.ConfigEndpoint{Name: "home", IPLookupFamily: 4}
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"home.example.com": {
				checker:    checker.New("home.example.com", []*config.ConfigEndpoint{home}, config.ConfigHealthChecks{Attempts: 2}, nil, map[*config.ConfigEndpoint]*iplookup.Lookuper{home: lookuper}),
				ttl:        60,
				failedIPs:  make(map[string]int),
				priorities: endpointPriorities([]*config.ConfigEndpoint{home}),
				provider:   mockProvider,
			},
		},
	}

	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 1, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.1"}, mockProvider.LastIPs)

	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 1, mockProvider.CallCount, "the address didn't change")

	setIP("203.0.113.2")
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 2, mockProvider.CallCount)
	assert.Equal(t, []string{"203.0.113.2"}, mockProvider.LastIPs)
	assert.Equal(t, []DomainStatusEndpoint{{Healthy: true, IP: "203.0.113.2"}}, hc.GetDomainStatus("home.example.com").Endpoints)
}

func TestNewHealthChecker_IPLookup(t *testing.T) {
	cfg := config.Get()
	prevDomains := cfg.Domains
	prevURLs := cfg.IPLookupURLs
	t.Cleanup(func() {
		cfg.Domains = prevDomains
		cfg.IPLookupURLs = prevURLs
	})

	cfg.Domains = []config.ConfigDomain{{
		RecordName: "home.example.com",
		Provider:   "p1",
		TTL:        60,
		Endpoints: []*config.ConfigEndpoint{
			{Name: "home", IPLookupFamily: 4},
			{Name: "backup", URL: "http://192.0.2.2/health", IP: "192.0.2.2", Priority: 1},
		},
	}}
	providers := map[string]dns.Provider{"p1": dns.NewMockProvider(false)}

	t.Run("dynamic endpoints are not in the static priorities", func(t *testing.T) {
		cfg.IPLookupURLs = []config.ConfigIPLookupURL{{URL: "https://ip.example.com", Family: 4}}
		hc, err := NewHealthChecker(providers, nil)
		require.NoError(t, err)
		assert.Equal(t, map[string]int{"192.0.2.2": 1}, hc.domainCheckers["home.example.com"].priorities)
	})
}
