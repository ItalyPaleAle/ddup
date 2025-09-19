package healthcheck

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
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
