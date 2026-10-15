package healthcheck

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	"github.com/italypaleale/ddup/pkg/iplookup"
)

// testService is a lookup service that returns a fixed address and counts the requests it gets
type testService struct {
	URL  string
	hits atomic.Int32
}

// newTestService starts a service that answers with the given address after the given delay
// All the services started by httptest share the same certificate, so the returned options trust every one of them
func newTestService(t *testing.T, ip string, delay time.Duration) (*testService, iplookup.Options) {
	t.Helper()

	svc := &testService{}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		svc.hits.Add(1)
		time.Sleep(delay)
		_, _ = w.Write([]byte(ip))
	}))
	t.Cleanup(srv.Close)
	svc.URL = srv.URL

	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return svc, iplookup.Options{TLSConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
}

func TestBuildLookupers(t *testing.T) {
	t.Run("an endpoint without a list uses the top-level one", func(t *testing.T) {
		v4, opts := newTestService(t, "203.0.113.1", 0)
		v6, _ := newTestService(t, "2001:db8::1", 0)
		cfg := &config.Config{IPLookupURLs: []config.ConfigIPLookupURL{
			{URL: v4.URL, Family: 4},
			{URL: v6.URL, Family: 6},
		}}
		ep := &config.ConfigEndpoint{Name: "home", IPLookupFamily: 4}

		lookupers := buildLookupers(cfg, config.ConfigDomain{Endpoints: []*config.ConfigEndpoint{ep}}, opts)

		addr, err := lookupers[ep].Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String())
		lookupers[ep].Wait()
		assert.EqualValues(t, 1, v4.hits.Load())
		assert.Zero(t, v6.hits.Load(), "the service for the other family is not contacted")
	})

	t.Run("the list of an endpoint replaces the top-level one", func(t *testing.T) {
		shared, opts := newTestService(t, "203.0.113.1", 0)
		own, _ := newTestService(t, "203.0.113.2", 0)
		cfg := &config.Config{IPLookupURLs: []config.ConfigIPLookupURL{{URL: shared.URL, Family: 4}}}
		ep := &config.ConfigEndpoint{
			Name:           "home",
			IPLookupFamily: 4,
			IPLookupURLs:   []config.ConfigIPLookupURL{{URL: own.URL}},
		}

		lookupers := buildLookupers(cfg, config.ConfigDomain{Endpoints: []*config.ConfigEndpoint{ep}}, opts)

		addr, err := lookupers[ep].Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.2", addr.String())
		lookupers[ep].Wait()
		assert.EqualValues(t, 1, own.hits.Load())
		assert.Zero(t, shared.hits.Load(), "the top-level services are not contacted")
	})

	t.Run("the services of an endpoint are raced and a different answer is reported", func(t *testing.T) {
		fast, opts := newTestService(t, "203.0.113.1", 0)
		slow, _ := newTestService(t, "203.0.113.2", 100*time.Millisecond)
		ep := &config.ConfigEndpoint{
			Name:           "home",
			IPLookupFamily: 4,
			IPLookupURLs:   []config.ConfigIPLookupURL{{URL: slow.URL, Family: 4}, {URL: fast.URL}},
		}

		var buf syncBuffer
		log := slog.New(slog.NewTextHandler(&buf, nil))
		opts.Logger = log
		lookupers := buildLookupers(&config.Config{}, config.ConfigDomain{Endpoints: []*config.ConfigEndpoint{ep}}, opts)

		addr, err := lookupers[ep].Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String(), "the first answer wins")

		lookupers[ep].Wait()
		assert.EqualValues(t, 1, fast.hits.Load())
		assert.EqualValues(t, 1, slow.hits.Load())
		assert.Contains(t, buf.String(), "IP lookup services returned different addresses")
	})

	t.Run("endpoints with different lists in one domain use their own services", func(t *testing.T) {
		a, opts := newTestService(t, "203.0.113.1", 0)
		b, _ := newTestService(t, "203.0.113.2", 0)
		epA := &config.ConfigEndpoint{Name: "a", IPLookupFamily: 4, IPLookupURLs: []config.ConfigIPLookupURL{{URL: a.URL}}}
		epB := &config.ConfigEndpoint{Name: "b", IPLookupFamily: 4, IPLookupURLs: []config.ConfigIPLookupURL{{URL: b.URL}}}

		lookupers := buildLookupers(&config.Config{}, config.ConfigDomain{Endpoints: []*config.ConfigEndpoint{epA, epB}}, opts)
		assert.NotSame(t, lookupers[epA], lookupers[epB])

		addr, err := lookupers[epA].Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String())
		addr, err = lookupers[epB].Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.2", addr.String())

		assert.EqualValues(t, 1, a.hits.Load())
		assert.EqualValues(t, 1, b.hits.Load())
	})

	t.Run("identical lists share a lookuper", func(t *testing.T) {
		svc, opts := newTestService(t, "203.0.113.1", 0)
		cfg := &config.Config{IPLookupURLs: []config.ConfigIPLookupURL{{URL: svc.URL, Family: 4}}}
		eps := []*config.ConfigEndpoint{
			{Name: "top-1", IPLookupFamily: 4},
			{Name: "top-2", IPLookupFamily: 4},
			{Name: "own", IPLookupFamily: 4, IPLookupURLs: []config.ConfigIPLookupURL{{URL: svc.URL, Family: 4}}},
		}

		lookupers := buildLookupers(cfg, config.ConfigDomain{Endpoints: eps}, opts)
		assert.Same(t, lookupers[eps[0]], lookupers[eps[1]])
		assert.Same(t, lookupers[eps[0]], lookupers[eps[2]], "the same services give the same lookuper, wherever they are set")
	})

	t.Run("no request is made when no endpoint looks up its address", func(t *testing.T) {
		svc, opts := newTestService(t, "203.0.113.1", 0)
		cfg := &config.Config{IPLookupURLs: []config.ConfigIPLookupURL{{URL: svc.URL, Family: 4}}}

		health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		t.Cleanup(health.Close)
		ep := &config.ConfigEndpoint{Name: "static", URL: health.URL, IP: "192.0.2.1"}
		d := config.ConfigDomain{
			RecordName:   "app.example.com",
			Endpoints:    []*config.ConfigEndpoint{ep},
			HealthChecks: config.ConfigHealthChecks{Timeout: time.Second},
		}

		lookupers := buildLookupers(cfg, d, opts)
		assert.Empty(t, lookupers)

		results := checker.New(d.RecordName, d.Endpoints, d.HealthChecks, nil, lookupers).CheckAll(t.Context())
		require.Len(t, results, 1)
		assert.True(t, results[0].Healthy)
		assert.Zero(t, svc.hits.Load())
	})
}

// syncBuffer is a bytes.Buffer that can be written to from many goroutines
type syncBuffer struct {
	lock sync.Mutex
	buf  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck // Passes through the error of the buffer, which is always nil
}

func (b *syncBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.String()
}
