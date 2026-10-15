package checker

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

// MockRoundTripper is a mock implementation of http.RoundTripper for testing
// Note: use this with HTTP endpoints too, not HTTPS
type MockRoundTripper struct {
	Response        *http.Response
	Error           error
	CapturedRequest *http.Request
	RoundTripFunc   func(req *http.Request) (*http.Response, error)
}

func (m *MockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture the request for inspection
	m.CapturedRequest = req

	// Use custom function if provided
	if m.RoundTripFunc != nil {
		return m.RoundTripFunc(req)
	}

	if m.Error != nil {
		return nil, m.Error
	}
	return m.Response, nil
}

// Helper function to create a test checker with custom HTTP client
func newTestChecker(client *http.Client) *checker {
	return &checker{
		domain: "test.example.com",
		cfg: config.ConfigHealthChecks{
			Timeout:  3 * time.Second,
			Attempts: 2,
		},
		client: client,
	}
}

func TestCheckEndpoint_Success(t *testing.T) {
	// Create a mock response with 200 status
	mockResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}

	// Create mock round tripper
	mockRT := &MockRoundTripper{
		Response: mockResponse,
		Error:    nil,
	}

	// Create HTTP client with mock transport
	client := &http.Client{
		Transport: mockRT,
	}

	// Create checker with mock client
	checker := newTestChecker(client)

	// Create test endpoint
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "http://example.com/health",
		IP:   "1.1.1.1",
		Host: "",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.True(t, result.Healthy, "Endpoint should be healthy")
	require.NoError(t, result.Error, "No error should be returned")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
}

func TestCheckEndpoint_HTTPError(t *testing.T) {
	// Create mock round tripper that returns an error
	mockRT := &MockRoundTripper{
		Response: nil,
		Error:    errors.New("connection refused"),
	}

	// Create HTTP client with mock transport
	client := &http.Client{
		Transport: mockRT,
	}

	// Create checker with mock client
	checker := newTestChecker(client)

	// Create test endpoint
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "http://example.com/health",
		IP:   "1.1.1.1",
		Host: "",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.False(t, result.Healthy, "Endpoint should be unhealthy")
	require.Error(t, result.Error, "Error should be returned")
	require.ErrorContains(t, result.Error, "HTTP request failed", "Error should mention HTTP request failure")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
}

func TestCheckEndpoint_BadStatusCode(t *testing.T) {
	// Create a mock response with 500 status
	mockResponse := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}

	// Create mock round tripper
	mockRT := &MockRoundTripper{
		Response: mockResponse,
		Error:    nil,
	}

	// Create HTTP client with mock transport
	client := &http.Client{
		Transport: mockRT,
	}

	// Create checker with mock client
	checker := newTestChecker(client)

	// Create test endpoint
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "http://example.com/health",
		IP:   "1.1.1.1",
		Host: "",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.False(t, result.Healthy, "Endpoint should be unhealthy")
	require.Error(t, result.Error, "Error should be returned")
	require.ErrorContains(t, result.Error, "status code 500", "Error should mention status code")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
}

func TestCheckEndpoint_RedirectStatusCode(t *testing.T) {
	// Create a mock response with 302 status (redirect)
	mockResponse := &http.Response{
		StatusCode: http.StatusFound,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}

	// Create mock round tripper
	mockRT := &MockRoundTripper{
		Response: mockResponse,
		Error:    nil,
	}

	// Create HTTP client with mock transport
	client := &http.Client{
		Transport: mockRT,
	}

	// Create checker with mock client
	checker := newTestChecker(client)

	// Create test endpoint
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "http://example.com/health",
		IP:   "1.1.1.1",
		Host: "",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.False(t, result.Healthy, "Endpoint should be unhealthy for redirect")
	require.Error(t, result.Error, "Error should be returned")
	require.ErrorContains(t, result.Error, "status code 302", "Error should mention status code")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
}

func TestCheckEndpoint_InvalidURL(t *testing.T) {
	// Create HTTP client (won't be used due to invalid URL)
	client := &http.Client{}

	// Create checker with client
	checker := newTestChecker(client)

	// Create test endpoint with invalid URL
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "://invalid-url",
		IP:   "1.1.1.1",
		Host: "",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.False(t, result.Healthy, "Endpoint should be unhealthy")
	require.Error(t, result.Error, "Error should be returned")
	require.ErrorContains(t, result.Error, "creating request", "Error should mention request creation failure")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
}

func TestCheckEndpoint_WithCustomHost(t *testing.T) {
	// Create a mock response with 200 status
	mockResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}

	// Create mock round tripper
	mockRT := &MockRoundTripper{
		Response: mockResponse,
		Error:    nil,
	}

	// Create HTTP client with mock transport
	client := &http.Client{
		Transport: mockRT,
	}

	// Create checker with mock client
	checker := newTestChecker(client)

	// Create test endpoint with custom host
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "http://1.1.1.1/health",
		IP:   "1.1.1.1",
		Host: "example.com",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.True(t, result.Healthy, "Endpoint should be healthy")
	require.NoError(t, result.Error, "No error should be returned")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")

	// Verify that the Host header was set correctly
	assert.NotNil(t, mockRT.CapturedRequest, "Request should have been captured")
	assert.Equal(t, "example.com", mockRT.CapturedRequest.Host, "Host header should be set to custom host")
	assert.Equal(t, "ddup/1.0", mockRT.CapturedRequest.Header.Get("User-Agent"), "User-Agent should be set")
}

func TestCheckEndpoint_ContextTimeout(t *testing.T) {
	// Create mock round tripper that simulates a slow response
	mockRT := &MockRoundTripper{
		Response: nil,
		Error:    context.DeadlineExceeded,
	}

	// Create HTTP client with mock transport
	client := &http.Client{
		Transport: mockRT,
	}

	// Create checker with very short timeout
	checker := &checker{
		domain:    "test.example.com",
		endpoints: nil,
		cfg: config.ConfigHealthChecks{
			Timeout:  1 * time.Millisecond, // Very short timeout
			Attempts: 2,
		},
		metrics: nil,
		client:  client,
	}

	// Create test endpoint
	endpoint := &config.ConfigEndpoint{
		Name: "test-endpoint",
		URL:  "http://example.com/health",
		IP:   "1.1.1.1",
		Host: "",
	}

	// Perform health check
	result := checker.checkEndpoint(t.Context(), endpoint)

	// Verify results
	assert.False(t, result.Healthy, "Endpoint should be unhealthy due to timeout")
	require.Error(t, result.Error, "Error should be returned")
	require.ErrorContains(t, result.Error, "HTTP request failed", "Error should mention HTTP request failure")
	assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
	assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
}

func TestCheckEndpoint_SuccessStatusCodes(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		shouldPass bool
	}{
		{"Status 200", 200, true},
		{"Status 201", 201, true},
		{"Status 299", 299, true},
		{"Status 199", 199, false},
		{"Status 300", 300, false},
		{"Status 400", 400, false},
		{"Status 500", 500, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a mock response with test status code
			mockResponse := &http.Response{
				StatusCode: tc.statusCode,
				Header:     make(http.Header),
				Body:       http.NoBody,
			}

			// Create mock round tripper
			mockRT := &MockRoundTripper{
				Response: mockResponse,
				Error:    nil,
			}

			// Create HTTP client with mock transport
			client := &http.Client{
				Transport: mockRT,
			}

			// Create checker with mock client
			checker := newTestChecker(client)

			// Create test endpoint
			endpoint := &config.ConfigEndpoint{
				Name: "test-endpoint",
				URL:  "http://example.com/health",
				IP:   "1.1.1.1",
				Host: "",
			}

			// Perform health check
			result := checker.checkEndpoint(t.Context(), endpoint)

			// Verify results
			if tc.shouldPass {
				assert.True(t, result.Healthy, "Endpoint should be healthy for status %d", tc.statusCode)
				require.NoError(t, result.Error, "No error should be returned for status %d", tc.statusCode)
			} else {
				assert.False(t, result.Healthy, "Endpoint should be unhealthy for status %d", tc.statusCode)
				require.Error(t, result.Error, "Error should be returned for status %d", tc.statusCode)
				require.ErrorContains(t, result.Error, "status code", "Error should mention status code")
			}

			assert.Equal(t, endpoint, result.Endpoint, "Endpoint should match")
			assert.Greater(t, result.Duration, time.Duration(0), "Duration should be greater than 0")
		})
	}
}

func TestCheckEndpoint_ExpectStatusAndMethod(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.ConfigHealthChecks
		code    int
		healthy bool
		method  string
	}{
		{name: "default accepts 200", code: 200, healthy: true, method: http.MethodGet},
		{name: "default rejects 418", code: 418, healthy: false, method: http.MethodGet},
		{name: "default rejects redirects", code: 302, healthy: false, method: http.MethodGet},
		{name: "custom code", cfg: config.ConfigHealthChecks{ExpectStatus: "418"}, code: 418, healthy: true, method: http.MethodGet},
		{name: "custom code rejects 200", cfg: config.ConfigHealthChecks{ExpectStatus: "418"}, code: 200, healthy: false, method: http.MethodGet},
		{name: "2xx class", cfg: config.ConfigHealthChecks{ExpectStatus: "2xx"}, code: 204, healthy: true, method: http.MethodGet},
		{name: "HEAD", cfg: config.ConfigHealthChecks{Method: "HEAD", ExpectStatus: "418"}, code: 418, healthy: true, method: http.MethodHead},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockRT := &MockRoundTripper{
				Response: &http.Response{
					StatusCode: tc.code,
					Header:     make(http.Header),
					Body:       http.NoBody,
				},
			}
			c := New("test.example.com", nil, tc.cfg, nil, nil)
			c.client.Transport = mockRT

			result := c.checkEndpoint(t.Context(), &config.ConfigEndpoint{URL: "http://example.com/health", IP: "1.1.1.1"})
			assert.Equal(t, tc.healthy, result.Healthy)
			assert.Equal(t, tc.method, mockRT.CapturedRequest.Method)
		})
	}
}

func TestClientForHost_ConcurrentAndCached(t *testing.T) {
	c := New("test.example.com", nil, config.ConfigHealthChecks{}, nil, nil)
	baseTransport, ok := c.client.Transport.(*http.Transport)
	require.True(t, ok)
	baseServerName := ""
	if baseTransport.TLSClientConfig != nil {
		baseServerName = baseTransport.TLSClientConfig.ServerName
	}

	var wg sync.WaitGroup
	clients := make([]*http.Client, 20)
	for i := range clients {
		wg.Go(func() {
			clients[i] = c.clientForHost("app.example.com")
		})
	}
	wg.Wait()

	for _, cl := range clients {
		assert.Same(t, clients[0], cl, "clients should be cached per host")
	}
	assert.Same(t, baseTransport, c.client.Transport, "base client must not be replaced")
	if baseTransport.TLSClientConfig != nil {
		assert.Equal(t, baseServerName, baseTransport.TLSClientConfig.ServerName, "base client must not be modified")
	}

	tr, ok := clients[0].Transport.(*http.Transport)
	require.True(t, ok)
	assert.NotSame(t, baseTransport, tr)
	assert.Equal(t, "app.example.com", tr.TLSClientConfig.ServerName)
	assert.NotSame(t, clients[0], c.clientForHost("other.example.com"))
}

func TestCheckEndpoint_NewConnectionEveryCheck(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	defer srv.Close()

	c := New("test.example.com", nil, config.ConfigHealthChecks{}, nil, nil)
	endpoint := &config.ConfigEndpoint{
		URL: srv.URL,
		IP: "1.1.1.1",
	}

	// A reused connection would keep talking to the same address forever, even after DNS changes
	for range 3 {
		result := c.checkEndpoint(t.Context(), endpoint)
		require.True(t, result.Healthy)
	}
	assert.Equal(t, int32(3), conns.Load(), "each check opens its own connection")

	// Clients for endpoints with a custom host are built from the same transport
	transport, ok := c.clientForHost("custom.example.com").Transport.(*http.Transport)
	require.True(t, ok)
	assert.True(t, transport.DisableKeepAlives)
}

func TestNew(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		c := New("example.com", nil, config.ConfigHealthChecks{}, nil, nil)
		assert.Equal(t, "example.com", c.GetDomain())
		assert.Equal(t, DefaultAttempts, c.GetMaxAttempts())
		assert.Equal(t, DefaultTimeout, c.cfg.Timeout)
		assert.Equal(t, 1, c.GetRecoverAfter())
	})

	t.Run("negative values use the defaults", func(t *testing.T) {
		c := New("example.com", nil, config.ConfigHealthChecks{Timeout: -time.Second, Attempts: -1, RecoverAfter: -1}, nil, nil)
		assert.Equal(t, DefaultAttempts, c.GetMaxAttempts())
		assert.Equal(t, DefaultTimeout, c.cfg.Timeout)
		assert.Equal(t, 1, c.GetRecoverAfter())
	})

	t.Run("configured values", func(t *testing.T) {
		c := New("example.com", nil, config.ConfigHealthChecks{Timeout: 5 * time.Second, Attempts: 4, RecoverAfter: 3}, nil, nil)
		assert.Equal(t, 4, c.GetMaxAttempts())
		assert.Equal(t, 5*time.Second, c.cfg.Timeout)
		assert.Equal(t, 3, c.GetRecoverAfter())
	})
}

func TestCheckAll(t *testing.T) {
	t.Run("checks all endpoints concurrently", func(t *testing.T) {
		const numEndpoints = 3
		arrived := make(chan struct{}, numEndpoints)
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			arrived <- struct{}{}

			// Hold every request until all of them arrived, which only happens if they are made concurrently
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}

			if r.URL.Path == "/fail" {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctx := t.Context()
		go func() {
			for range numEndpoints {
				select {
				case <-arrived:
				case <-ctx.Done():
					return
				}
			}
			close(release)
		}()

		endpoints := []*config.ConfigEndpoint{
			{Name: "ok1", URL: srv.URL + "/ok", IP: "192.0.2.1"},
			{Name: "fail", URL: srv.URL + "/fail", IP: "192.0.2.2"},
			{Name: "ok2", URL: srv.URL + "/ok", IP: "192.0.2.3"},
		}
		c := New("example.com", endpoints, config.ConfigHealthChecks{Timeout: 2 * time.Second}, nil, nil)

		results := c.CheckAll(t.Context())
		require.Len(t, results, numEndpoints)

		// Results are in the same order as the endpoints
		for i, res := range results {
			assert.Same(t, endpoints[i], res.Endpoint)
		}
		assert.True(t, results[0].Healthy)
		require.NoError(t, results[0].Error)
		assert.False(t, results[1].Healthy)
		require.ErrorContains(t, results[1].Error, "status code 503")
		assert.True(t, results[2].Healthy)
		require.NoError(t, results[2].Error)
	})

	t.Run("no endpoints", func(t *testing.T) {
		c := New("example.com", nil, config.ConfigHealthChecks{}, nil, nil)
		assert.Empty(t, c.CheckAll(t.Context()))
	})
}

// newLookupServer starts a service that returns the address in ip, or fails while fail is true
func newLookupServer(t *testing.T, ip *atomic.Pointer[string], fail *atomic.Bool) (*httptest.Server, *tls.Config) {
	t.Helper()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(*ip.Load()))
	}))
	t.Cleanup(srv.Close)

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

// lookupersFor returns the lookupers map that gives the same lookuper to all the endpoints
func lookupersFor(l *iplookup.Lookuper, eps ...*config.ConfigEndpoint) map[*config.ConfigEndpoint]*iplookup.Lookuper {
	res := make(map[*config.ConfigEndpoint]*iplookup.Lookuper, len(eps))
	for _, ep := range eps {
		res[ep] = l
	}
	return res
}

func TestCheckEndpoint_Dynamic(t *testing.T) {
	var ip atomic.Pointer[string]
	setIP := func(v string) { ip.Store(&v) }
	setIP("203.0.113.1")
	var fail atomic.Bool
	srv, tlsConfig := newLookupServer(t, &ip, &fail)
	lookuper := iplookup.New([]iplookup.Source{{URL: srv.URL, Family: 4}}, iplookup.Options{TLSConfig: tlsConfig})

	t.Run("without a URL the endpoint is healthy when the lookup works", func(t *testing.T) {
		ep := &config.ConfigEndpoint{Name: "home", IPLookupFamily: 4}
		c := New("home.example.com", nil, config.ConfigHealthChecks{}, nil, lookupersFor(lookuper, ep))

		res := c.checkEndpoint(t.Context(), ep)
		assert.True(t, res.Healthy)
		require.NoError(t, res.Error)
		assert.Equal(t, "203.0.113.1", res.Target)

		// The address follows the lookup
		setIP("203.0.113.2")
		res = c.checkEndpoint(t.Context(), ep)
		assert.True(t, res.Healthy)
		assert.Equal(t, "203.0.113.2", res.Target)

		// If the lookup fails, the endpoint is unhealthy but keeps the last address, so the failure is counted against it
		fail.Store(true)
		defer fail.Store(false)
		res = c.checkEndpoint(t.Context(), ep)
		assert.False(t, res.Healthy)
		require.ErrorContains(t, res.Error, "IP lookup failed")
		assert.Equal(t, "203.0.113.2", res.Target)
	})

	t.Run("a lookup that never worked has no target", func(t *testing.T) {
		fail.Store(true)
		defer fail.Store(false)

		ep := &config.ConfigEndpoint{Name: "new", IPLookupFamily: 4}
		c := New("home.example.com", nil, config.ConfigHealthChecks{}, nil, lookupersFor(lookuper, ep))
		res := c.checkEndpoint(t.Context(), ep)
		assert.False(t, res.Healthy)
		assert.Empty(t, res.Target)
	})

	t.Run("without a lookuper the endpoint is unhealthy", func(t *testing.T) {
		c := New("home.example.com", nil, config.ConfigHealthChecks{}, nil, nil)
		res := c.checkEndpoint(t.Context(), &config.ConfigEndpoint{Name: "home", IPLookupFamily: 4})
		assert.False(t, res.Healthy)
		require.ErrorContains(t, res.Error, "IP lookup is not configured")
	})

	t.Run("with a URL the endpoint is also health-checked", func(t *testing.T) {
		setIP("203.0.113.2")
		ep := &config.ConfigEndpoint{Name: "home", URL: "http://example.com/health", IPLookupFamily: 4}
		c := New("home.example.com", nil, config.ConfigHealthChecks{}, nil, lookupersFor(lookuper, ep))
		mockRT := &MockRoundTripper{Response: &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: http.NoBody}}
		c.client.Transport = mockRT

		res := c.checkEndpoint(t.Context(), ep)
		assert.False(t, res.Healthy, "the health check failed")
		assert.Equal(t, "203.0.113.2", res.Target, "the target is still the looked-up address")

		mockRT.Response = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}
		res = c.checkEndpoint(t.Context(), ep)
		assert.True(t, res.Healthy)
		assert.Equal(t, "203.0.113.2", res.Target)
	})

	t.Run("static endpoints have no target", func(t *testing.T) {
		c := New("app.example.com", nil, config.ConfigHealthChecks{}, nil, nil)
		mockRT := &MockRoundTripper{Response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}}
		c.client.Transport = mockRT

		res := c.checkEndpoint(t.Context(), &config.ConfigEndpoint{URL: "http://example.com/h", IP: "192.0.2.1"})
		assert.True(t, res.Healthy)
		assert.Empty(t, res.Target)
	})

	t.Run("CheckAll runs lookups concurrently with the other checks", func(t *testing.T) {
		setIP("203.0.113.3")
		eps := []*config.ConfigEndpoint{
			{Name: "a", IPLookupFamily: 4},
			{Name: "b", IPLookupFamily: 4},
		}
		c := New("home.example.com", eps, config.ConfigHealthChecks{Timeout: time.Second}, nil, lookupersFor(lookuper, eps...))

		results := c.CheckAll(t.Context())
		require.Len(t, results, 2)
		for _, r := range results {
			assert.True(t, r.Healthy)
			assert.Equal(t, "203.0.113.3", r.Target)
		}
	})
}
