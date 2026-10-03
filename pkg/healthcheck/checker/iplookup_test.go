package checker

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

func TestParseLookupResponse(t *testing.T) {
	jsonPattern := regexp.MustCompile(`"ip":\s*"([^"]+)"`)
	tests := []struct {
		name    string
		body    string
		family  int
		pattern *regexp.Regexp
		want    string
		wantErr string
	}{
		{name: "plain IPv4", body: "203.0.113.7\n", want: "203.0.113.7"},
		{name: "plain IPv6 is canonicalized", body: " 2001:0DB8:0:0:0:0:0:1 ", want: "2001:db8::1"},
		{name: "IPv4 mapped to IPv6 is unmapped", body: "::ffff:203.0.113.7", family: 4, want: "203.0.113.7"},
		{name: "JSON with a pattern", body: `{"ip": "203.0.113.7"}`, pattern: jsonPattern, want: "203.0.113.7"},
		{name: "pattern doesn't match", body: `{"addr": "x"}`, pattern: jsonPattern, wantErr: "doesn't match"},
		{name: "not an IP", body: "<html>rate limited</html>", wantErr: "not an IP address"},
		{name: "wrong family 4", body: "2001:db8::1", family: 4, wantErr: "IPv4 address is expected"},
		{name: "wrong family 6", body: "203.0.113.7", family: 6, wantErr: "IPv6 address is expected"},
		{name: "private is rejected", body: "10.0.0.1", wantErr: "not a public IP"},
		{name: "loopback is rejected", body: "127.0.0.1", wantErr: "not a public IP"},
		{name: "unspecified is rejected", body: "0.0.0.0", wantErr: "not a public IP"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseLookupResponse(tc.body, tc.family, tc.pattern)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLookupIP_FallbackAndCache(t *testing.T) {
	lookupCacheTTL = 0
	t.Cleanup(func() { lookupCacheTTL = lookupCacheTTLDefault })

	var failing, working atomic.Int32
	failSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failing.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failSrv.Close()
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		working.Add(1)
		_, _ = w.Write([]byte("203.0.113.9\n"))
	}))
	defer okSrv.Close()

	spec := &config.ConfigIPLookup{URLs: []string{failSrv.URL, okSrv.URL}, Family: 4}
	ip, err := lookupIP(t.Context(), spec, time.Second)
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.9", ip)
	assert.Equal(t, int32(1), failing.Load(), "the first service was tried first")

	// All services fail
	_, err = lookupIP(t.Context(), &config.ConfigIPLookup{URLs: []string{failSrv.URL}}, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status code 503")

	// With the cache on, repeated lookups with the same settings share one call
	lookupCacheTTL = time.Minute
	spec2 := &config.ConfigIPLookup{URLs: []string{okSrv.URL}, Family: 4, Pattern: ""}
	before := working.Load()
	for range 3 {
		_, err = lookupIP(t.Context(), spec2, time.Second)
		require.NoError(t, err)
	}
	assert.Equal(t, before+1, working.Load())
}

func TestCheckEndpoint_Dynamic(t *testing.T) {
	lookupCacheTTL = 0
	t.Cleanup(func() { lookupCacheTTL = lookupCacheTTLDefault })

	var ip atomic.Pointer[string]
	setIP := func(v string) { ip.Store(&v) }
	setIP("203.0.113.1")
	var failLookup atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failLookup.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(*ip.Load()))
	}))
	defer srv.Close()

	c := New("home.example.com", nil, config.ConfigHealthChecks{}, nil)

	t.Run("no URL means healthy when the lookup works", func(t *testing.T) {
		ep := &config.ConfigEndpoint{Name: "home", IPLookup: &config.ConfigIPLookup{URLs: []string{srv.URL}}}
		res := c.checkEndpoint(t.Context(), ep)
		assert.True(t, res.Healthy)
		assert.Equal(t, "203.0.113.1", res.Target)

		// The address follows the lookup
		setIP("203.0.113.2")
		res = c.checkEndpoint(t.Context(), ep)
		assert.Equal(t, "203.0.113.2", res.Target)

		// If the lookup fails, the endpoint is unhealthy but keeps the last address, so failures are counted against it
		failLookup.Store(true)
		res = c.checkEndpoint(t.Context(), ep)
		assert.False(t, res.Healthy)
		require.Error(t, res.Error)
		assert.Equal(t, "203.0.113.2", res.Target)
		failLookup.Store(false)
	})

	t.Run("lookup that never worked has no target", func(t *testing.T) {
		failLookup.Store(true)
		defer failLookup.Store(false)
		ep := &config.ConfigEndpoint{Name: "new", IPLookup: &config.ConfigIPLookup{URLs: []string{srv.URL}}}
		res := c.checkEndpoint(t.Context(), ep)
		assert.False(t, res.Healthy)
		assert.Empty(t, res.Target)
	})

	t.Run("with a URL the endpoint is also health-checked", func(t *testing.T) {
		mockRT := &MockRoundTripper{Response: &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: http.NoBody}}
		c2 := New("home.example.com", nil, config.ConfigHealthChecks{}, nil)
		c2.client.Transport = mockRT
		ep := &config.ConfigEndpoint{Name: "home", URL: "http://example.com/health", IPLookup: &config.ConfigIPLookup{URLs: []string{srv.URL}}}
		res := c2.checkEndpoint(t.Context(), ep)
		assert.False(t, res.Healthy, "the health check failed")
		assert.Equal(t, "203.0.113.2", res.Target, "the target is still the looked-up address")
	})

	t.Run("static endpoints report their target", func(t *testing.T) {
		mockRT := &MockRoundTripper{Response: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody}}
		c3 := New("app.example.com", nil, config.ConfigHealthChecks{}, nil)
		c3.client.Transport = mockRT
		res := c3.checkEndpoint(t.Context(), &config.ConfigEndpoint{URL: "http://example.com/h", CNAME: "tunnel.example.com"})
		assert.Equal(t, "tunnel.example.com", res.Target)
	})
}
