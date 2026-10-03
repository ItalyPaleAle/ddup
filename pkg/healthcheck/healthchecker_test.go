package healthcheck

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	"github.com/italypaleale/ddup/pkg/notify"
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
	require.ErrorIs(t, idle.ForceCheck(t.Context()), ErrNotRunning)

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
	require.Eventually(t, func() bool { return len(hc.GetDomainStatus("example.com").Endpoints) == 1 }, 5*time.Second, 5*time.Millisecond)

	// A check that ran moments ago is reused, so a forced check returns right away without running another
	reqCtx, reqCancel := context.WithTimeout(ctx, 5*time.Second)
	defer reqCancel()
	require.NoError(t, hc.ForceCheck(reqCtx))
	assert.Equal(t, 1, mockProvider.CallCount)

	// After the minimum interval, a forced check runs for real: here it finds the endpoint down and which is recorded in the state
	time.Sleep(minForceInterval + 100*time.Millisecond)
	reqCtx2, reqCancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer reqCancel2()
	mockChecker.Results = []checker.Result{{Endpoint: ep1, Healthy: false, Error: errors.New("down")}}
	before := hc.GetDomainStatus("example.com").LastUpdated
	require.NoError(t, hc.ForceCheck(reqCtx2))
	st := hc.GetDomainStatus("example.com")
	assert.True(t, st.LastUpdated.After(before), "a forced check updates the domain state")
	assert.Equal(t, 1, st.Endpoints[0].FailureCount)

	// A canceled context returns an error
	canceled, c2 := context.WithCancel(context.Background())
	c2()
	require.Error(t, hc.ForceCheck(canceled))
}

func TestHealthChecker_Webhooks(t *testing.T) {
	events := make(chan notify.Event, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev notify.Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		events <- ev
	}))
	defer srv.Close()

	n, err := notify.New([]config.ConfigWebhook{{Name: "t", URL: srv.URL, Method: "POST", Attempts: 1, Timeout: time.Second}})
	require.NoError(t, err)

	mockProvider := dns.NewMockProvider(false)
	ep1 := &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"}
	ep2 := &config.ConfigEndpoint{Name: "endpoint2", IP: "2.2.2.2"}
	mockChecker := &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 1,
		Results:     []checker.Result{{Endpoint: ep1, Healthy: true}, {Endpoint: ep2, Healthy: true}},
	}
	hc := &HealthChecker{
		notifier: n,
		domainCheckers: map[string]*domainChecker{
			"example.com": {checker: mockChecker, ttl: 60, failedIPs: make(map[string]int), provider: mockProvider},
		},
	}

	next := func() notify.Event {
		n.Wait(5 * time.Second)
		select {
		case ev := <-events:
			return ev
		default:
			return notify.Event{}
		}
	}

	// Startup: records are published
	hc.checkAndUpdateDNS(t.Context())
	ev := next()
	assert.Equal(t, notify.EventDNSUpdated, ev.Type)
	assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, ev.Healthy)

	// Nothing changes: no event
	hc.checkAndUpdateDNS(t.Context())
	assert.Empty(t, next().Type)

	// Everything goes down: one all_unhealthy event, and not repeated on the next cycle
	mockChecker.Results = []checker.Result{{Endpoint: ep1, Healthy: false, Error: errors.New("x")}, {Endpoint: ep2, Healthy: false, Error: errors.New("x")}}
	hc.checkAndUpdateDNS(t.Context())
	ev = next()
	assert.Equal(t, notify.EventAllUnhealthy, ev.Type)
	assert.Len(t, ev.Endpoints, 2)
	hc.checkAndUpdateDNS(t.Context())
	assert.Empty(t, next().Type)

	// Recovery of one endpoint publishes it
	mockChecker.Results[0] = checker.Result{Endpoint: ep1, Healthy: true}
	hc.checkAndUpdateDNS(t.Context())
	ev = next()
	assert.Equal(t, notify.EventDNSUpdated, ev.Type)
	assert.Equal(t, []string{"1.1.1.1"}, ev.Healthy)

	// Provider errors are notified once per distinct error
	mockProvider.ShouldError = true
	mockChecker.Results[1] = checker.Result{Endpoint: ep2, Healthy: true}
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, notify.EventDNSUpdateFailed, next().Type)
	hc.checkAndUpdateDNS(t.Context())
	assert.Empty(t, next().Type)
}

func TestHealthChecker_NoNotificationWhenDNSAlreadyUpToDate(t *testing.T) {
	events := make(chan notify.Event, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev notify.Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		events <- ev
	}))
	defer srv.Close()

	n, err := notify.New([]config.ConfigWebhook{{Name: "t", URL: srv.URL, Method: "POST", Attempts: 1, Timeout: time.Second}})
	require.NoError(t, err)

	// The provider reports that DNS already matched, like on startup after a restart
	mockProvider := dns.NewMockProvider(false)
	mockProvider.NoChange = true
	ep := &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"}
	mockChecker := &checker.MockChecker{Domain: "example.com", MaxAttempts: 2, Results: []checker.Result{{Endpoint: ep, Healthy: true}}}
	hc := &HealthChecker{
		notifier: n,
		domainCheckers: map[string]*domainChecker{
			"example.com": {checker: mockChecker, ttl: 60, failedIPs: make(map[string]int), provider: mockProvider},
		},
	}

	hc.checkAndUpdateDNS(t.Context())
	n.Wait(5 * time.Second)

	assert.Equal(t, 1, mockProvider.CallCount)
	assert.Equal(t, []string{"1.1.1.1"}, hc.domainCheckers["example.com"].healthyIPs, "state is still recorded")
	assert.Empty(t, events, "no event when DNS did not change")

	// A real change carries what DNS held before
	mockProvider.NoChange = false
	mockProvider.Previous = []string{"9.9.9.9"}
	mockChecker.Results = []checker.Result{{Endpoint: ep, Healthy: true}, {Endpoint: &config.ConfigEndpoint{Name: "e2", IP: "2.2.2.2"}, Healthy: true}}
	hc.checkAndUpdateDNS(t.Context())
	n.Wait(5 * time.Second)
	ev := <-events
	assert.Equal(t, notify.EventDNSUpdated, ev.Type)
	assert.Equal(t, []string{"9.9.9.9"}, ev.Previous)
}

func TestHealthChecker_PriorityTiers(t *testing.T) {
	mockProvider := dns.NewMockProvider(false)
	us := &config.ConfigEndpoint{Name: "us", IP: "1.1.1.1"}
	eu := &config.ConfigEndpoint{Name: "eu", IP: "2.2.2.2"}
	tunnel := &config.ConfigEndpoint{Name: "tunnel", CNAME: "tunnel.example.com", Proxied: true, Priority: 1}

	up := func(ep *config.ConfigEndpoint) checker.Result { return checker.Result{Endpoint: ep, Healthy: true} }
	down := func(ep *config.ConfigEndpoint) checker.Result {
		return checker.Result{Endpoint: ep, Healthy: false, Error: errors.New("down")}
	}

	mockChecker := &checker.MockChecker{Domain: "example.com", MaxAttempts: 1}
	hc := &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:   mockChecker,
				ttl:       60,
				failedIPs: make(map[string]int),
				provider:  mockProvider,
				endpoints: map[string]*config.ConfigEndpoint{"1.1.1.1": us, "2.2.2.2": eu, "tunnel.example.com": tunnel},
			},
		},
	}
	dc := hc.domainCheckers["example.com"]

	run := func(results ...checker.Result) {
		t.Helper()
		mockChecker.Results = results
		hc.checkAndUpdateDNS(t.Context())
	}
	published := func() []string {
		var v []string
		for _, tg := range mockProvider.LastTargets {
			v = append(v, tg.Value)
		}
		return v
	}

	// Everything healthy: only the preferred tier is published, the standby is not
	run(up(us), up(eu), up(tunnel))
	assert.ElementsMatch(t, []string{"1.1.1.1", "2.2.2.2"}, published())
	assert.Equal(t, 1, mockProvider.CallCount)
	status := hc.GetDomainStatus("example.com")
	activeByIP := map[string]bool{}
	for _, e := range status.Endpoints {
		activeByIP[e.IP] = e.Active
		if e.IP == "tunnel.example.com" {
			assert.Equal(t, "CNAME", e.Type)
			assert.Equal(t, 1, e.Priority)
			assert.True(t, e.Proxied)
		}
	}
	assert.Equal(t, map[string]bool{"1.1.1.1": true, "2.2.2.2": true, "tunnel.example.com": false}, activeByIP)

	// The standby going down doesn't change what's published, so DNS isn't touched
	run(up(us), up(eu), down(tunnel))
	assert.Equal(t, 1, mockProvider.CallCount)
	run(up(us), up(eu), up(tunnel))
	assert.Equal(t, 1, mockProvider.CallCount)

	// One of the preferred endpoints goes down: the other is still published alone
	run(down(us), up(eu), up(tunnel))
	assert.Equal(t, []string{"2.2.2.2"}, published())
	assert.Equal(t, 2, mockProvider.CallCount)

	// Both preferred endpoints are down: fall back to the proxied CNAME
	run(down(us), down(eu), up(tunnel))
	require.Len(t, mockProvider.LastTargets, 1)
	assert.Equal(t, dns.Target{Value: "tunnel.example.com", Proxied: true}, mockProvider.LastTargets[0])
	assert.Equal(t, 3, mockProvider.CallCount)

	// Recovery of a preferred endpoint goes back to it
	run(up(us), down(eu), up(tunnel))
	assert.Equal(t, []string{"1.1.1.1"}, published())
	assert.Equal(t, 4, mockProvider.CallCount)

	// Everything down: DNS is left alone
	run(down(us), down(eu), down(tunnel))
	assert.Equal(t, 4, mockProvider.CallCount)
	assert.Empty(t, dc.healthyIPs)
}

func TestHealthChecker_TierChangeEvent(t *testing.T) {
	events := make(chan notify.Event, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev notify.Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		events <- ev
	}))
	defer srv.Close()
	n, err := notify.New([]config.ConfigWebhook{{Name: "t", URL: srv.URL, Method: "POST", Attempts: 1, Timeout: time.Second}})
	require.NoError(t, err)

	us := &config.ConfigEndpoint{Name: "us", IP: "1.1.1.1"}
	tunnel := &config.ConfigEndpoint{Name: "tunnel", CNAME: "tunnel.example.com", Proxied: true, Priority: 1}
	mockChecker := &checker.MockChecker{Domain: "example.com", MaxAttempts: 1}
	hc := &HealthChecker{
		notifier: n,
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker: mockChecker, ttl: 60, failedIPs: make(map[string]int), provider: dns.NewMockProvider(false),
				endpoints: map[string]*config.ConfigEndpoint{"1.1.1.1": us, "tunnel.example.com": tunnel},
			},
		},
	}

	mockChecker.Results = []checker.Result{{Endpoint: us, Healthy: true}, {Endpoint: tunnel, Healthy: true}}
	hc.checkAndUpdateDNS(t.Context())
	n.Wait(5 * time.Second)
	<-events

	mockChecker.Results = []checker.Result{{Endpoint: us, Healthy: false, Error: errors.New("down")}, {Endpoint: tunnel, Healthy: true}}
	hc.checkAndUpdateDNS(t.Context())
	n.Wait(5 * time.Second)
	ev := <-events
	assert.Equal(t, notify.EventDNSUpdated, ev.Type)
	assert.Equal(t, []string{"tunnel.example.com"}, ev.Published)
	assert.Equal(t, 1, ev.Tier)
	assert.Equal(t, 0, ev.PreviousTier)
	assert.Contains(t, ev.Subject(), "now points to tunnel.example.com")
	assert.Contains(t, ev.Text(), "Priority: 1 (was 0)")
}
