package checker

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

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
	lookuper := iplookup.New([]iplookup.Source{{URL: srv.URL, Family: 4}}, iplookup.WithTLSConfig(tlsConfig))

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
