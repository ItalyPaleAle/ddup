package healthcheck

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

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

func TestDedupeResults(t *testing.T) {
	a := &config.ConfigEndpoint{Name: "a", IPLookupFamily: 4}
	b := &config.ConfigEndpoint{Name: "b", IPLookupFamily: 4}
	static := &config.ConfigEndpoint{Name: "static", IP: "198.51.100.1"}
	noAddr := &config.ConfigEndpoint{Name: "none", IPLookupFamily: 6}

	in := []checker.Result{
		{Endpoint: a, Target: "203.0.113.1", Healthy: false},
		{Endpoint: b, Target: "203.0.113.1", Healthy: true},
		{Endpoint: static, Healthy: true},
		{Endpoint: noAddr},
		{Endpoint: noAddr},
	}
	out := dedupeResults(in)
	require.Len(t, out, 4)
	assert.Equal(t, "b", out[0].Endpoint.Name, "a healthy result wins over an unhealthy one")
	assert.Equal(t, "static", out[1].Endpoint.Name)
	assert.Equal(t, "none", out[2].Endpoint.Name, "results without an address are kept")

	assert.Equal(t, "203.0.113.1", resultIP(in[0]))
	assert.Equal(t, "198.51.100.1", resultIP(in[2]))
	assert.Empty(t, resultIP(in[3]))
}

// Uses the real checker and a real lookup against a test service: the address changes between cycles and DNS follows
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
		iplookup.WithTLSConfig(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}),
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

	t.Run("invalid pattern", func(t *testing.T) {
		cfg.IPLookupURLs = []config.ConfigIPLookupURL{{URL: "https://ip.example.com", Family: 4, Pattern: "("}}
		_, err := NewHealthChecker(providers, nil)
		require.ErrorContains(t, err, "domain 'home.example.com': endpoint 'home': entry 0 in ipLookupURLs has an invalid pattern")
	})
}
