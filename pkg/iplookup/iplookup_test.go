package iplookup

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Addresses in these tests are from the ranges reserved for documentation

// syncBuffer is a bytes.Buffer that can be written to by multiple goroutines
type syncBuffer struct {
	lock sync.Mutex
	buf  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck
}

func (b *syncBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.String()
}

func newLogger(buf *syncBuffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// answer returns a handler that responds with the body
func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}
}

// slowAnswer returns a handler that responds with the body after a delay, unless the client goes away first
func slowAnswer(body string, delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
			_, _ = w.Write([]byte(body))
		case <-r.Context().Done():
		}
	}
}

// startServers starts TLS servers, and returns them with a TLS config that trusts all of them
func startServers(t *testing.T, handlers ...http.Handler) ([]*httptest.Server, *tls.Config) {
	t.Helper()

	pool := x509.NewCertPool()
	servers := make([]*httptest.Server, len(handlers))
	for i, h := range handlers {
		srv := httptest.NewTLSServer(h)
		t.Cleanup(srv.Close)
		pool.AddCert(srv.Certificate())
		servers[i] = srv
	}
	return servers, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
}

func sources4(servers ...*httptest.Server) []Source {
	res := make([]Source, len(servers))
	for i, srv := range servers {
		res[i] = Source{URL: srv.URL, Family: 4}
	}
	return res
}

func TestLookup_SingleSource(t *testing.T) {
	jsonPattern := regexp.MustCompile(`"ip":\s*"([^"]+)"`)
	tests := []struct {
		name    string
		body    string
		pattern *regexp.Regexp
		want    string
		wantErr string
	}{
		{name: "plain address", body: "203.0.113.7", want: "203.0.113.7"},
		{name: "trailing newline", body: "203.0.113.7\n", want: "203.0.113.7"},
		{name: "mapped IPv6 is unmapped", body: "::ffff:203.0.113.7", want: "203.0.113.7"},
		{name: "pattern", body: `{"ip": "203.0.113.7"}`, pattern: jsonPattern, want: "203.0.113.7"},
		{name: "pattern doesn't match", body: `{"addr": "203.0.113.7"}`, pattern: jsonPattern, wantErr: "response doesn't match the pattern"},
		{name: "JSON without a pattern", body: `{"ip": "203.0.113.7"}`, wantErr: "response is not an IP address"},
		{name: "garbage", body: "hello", wantErr: "response is not an IP address"},
		{name: "HTML", body: "<html><body>Rate limited</body></html>", wantErr: "response is not an IP address"},
		{name: "empty", body: "", wantErr: "response is not an IP address"},
		{name: "hostname", body: "example.com", wantErr: "response is not an IP address"},
		{name: "wrong family", body: "2001:db8::1", wantErr: "got 2001:db8::1, but an IPv4 address is expected"},
		{name: "loopback", body: "127.0.0.1", wantErr: "127.0.0.1 is not a public IP address"},
		{name: "private", body: "192.168.1.10", wantErr: "192.168.1.10 is not a public IP address"},
		{name: "private 10/8", body: "10.0.0.1", wantErr: "10.0.0.1 is not a public IP address"},
		{name: "link-local", body: "169.254.1.1", wantErr: "169.254.1.1 is not a public IP address"},
		{name: "unspecified", body: "0.0.0.0", wantErr: "0.0.0.0 is not a public IP address"},
		{name: "multicast", body: "224.0.0.1", wantErr: "224.0.0.1 is not a public IP address"},
		{name: "oversized", body: strings.Repeat("1", maxResponseSize+1), wantErr: "response is too large"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			servers, tlsConfig := startServers(t, answer(tc.body))
			src := sources4(servers...)
			src[0].Pattern = tc.pattern
			l := New(src, WithTLSConfig(tlsConfig))

			addr, err := l.Lookup(t.Context(), 4)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, addr.String())
		})
	}
}

func TestLookup_RequestErrors(t *testing.T) {
	t.Run("status code", func(t *testing.T) {
		servers, tlsConfig := startServers(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("203.0.113.7"))
		}))
		l := New(sources4(servers...), WithTLSConfig(tlsConfig))

		_, err := l.Lookup(t.Context(), 4)
		require.ErrorContains(t, err, "status code 503")
	})

	t.Run("redirects are not followed", func(t *testing.T) {
		var hits atomic.Int32
		var targetURL atomic.Value
		servers, tlsConfig := startServers(t,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				_, _ = w.Write([]byte("203.0.113.7"))
			}),
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target, _ := targetURL.Load().(string)
				http.Redirect(w, r, target, http.StatusFound)
			}),
		)
		targetURL.Store(servers[0].URL)

		l := New(sources4(servers[1]), WithTLSConfig(tlsConfig))
		_, err := l.Lookup(t.Context(), 4)
		require.ErrorContains(t, err, "status code 302")
		assert.Zero(t, hits.Load(), "the redirect target must not be called")
	})

	t.Run("timeout", func(t *testing.T) {
		servers, tlsConfig := startServers(t, slowAnswer("203.0.113.7", 10*time.Second))
		l := New(sources4(servers...), WithTLSConfig(tlsConfig), WithTimeout(100*time.Millisecond))

		start := time.Now()
		_, err := l.Lookup(t.Context(), 4)
		require.Error(t, err)
		require.ErrorContains(t, err, "deadline exceeded")
		assert.Less(t, time.Since(start), 5*time.Second)
	})

	t.Run("context is respected", func(t *testing.T) {
		servers, tlsConfig := startServers(t, slowAnswer("203.0.113.7", 10*time.Second))
		l := New(sources4(servers...), WithTLSConfig(tlsConfig), WithTimeout(time.Minute))

		ctx, cancel := context.WithCancel(t.Context())
		time.AfterFunc(100*time.Millisecond, cancel)
		start := time.Now()
		_, err := l.Lookup(ctx, 4)
		require.ErrorContains(t, err, "context canceled")
		assert.Less(t, time.Since(start), 5*time.Second)
	})

	t.Run("no service for the family", func(t *testing.T) {
		servers, tlsConfig := startServers(t, answer("203.0.113.7"))
		l := New(sources4(servers...), WithTLSConfig(tlsConfig))

		_, err := l.Lookup(t.Context(), 6)
		require.ErrorContains(t, err, "no service is configured for IPv6")
	})

	t.Run("the connection uses the IP version of the source", func(t *testing.T) {
		// The server listens on IPv4 only, so a source for IPv6 can't reach it
		servers, tlsConfig := startServers(t, answer("2001:db8::1"))
		l := New([]Source{{URL: servers[0].URL, Family: 6}}, WithTLSConfig(tlsConfig))

		_, err := l.Lookup(t.Context(), 6)
		require.ErrorContains(t, err, "request failed")
	})

	t.Run("IPv6 source", func(t *testing.T) {
		var lc net.ListenConfig
		listener, err := lc.Listen(t.Context(), "tcp6", "[::1]:0")
		if err != nil {
			t.Skip("IPv6 is not available")
		}
		srv := httptest.NewUnstartedServer(answer("2001:db8::1"))
		srv.Listener = listener
		srv.StartTLS()
		t.Cleanup(srv.Close)
		pool := x509.NewCertPool()
		pool.AddCert(srv.Certificate())

		l := New([]Source{{URL: srv.URL, Family: 6}}, WithTLSConfig(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}))
		addr, err := l.Lookup(t.Context(), 6)
		require.NoError(t, err)
		assert.Equal(t, "2001:db8::1", addr.String())
	})
}

func TestLookup_Race(t *testing.T) {
	t.Run("the first valid answer is used and the slow one does not delay it", func(t *testing.T) {
		servers, tlsConfig := startServers(t,
			slowAnswer("203.0.113.1", 800*time.Millisecond),
			answer("203.0.113.1"),
		)
		var logs syncBuffer
		l := New(sources4(servers...), WithTLSConfig(tlsConfig), WithLogger(newLogger(&logs)))

		start := time.Now()
		addr, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String())
		assert.Less(t, time.Since(start), 600*time.Millisecond, "must not wait for the slow service")

		l.Wait()
		assert.NotContains(t, logs.String(), "level=WARN", "the two answers match")
	})

	t.Run("a different answer is logged as a warning", func(t *testing.T) {
		servers, tlsConfig := startServers(t,
			answer("203.0.113.1"),
			slowAnswer("198.51.100.2", 200*time.Millisecond),
		)
		var logs syncBuffer
		l := New(sources4(servers...), WithTLSConfig(tlsConfig), WithLogger(newLogger(&logs)))

		addr, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String())

		l.Wait()
		out := logs.String()
		assert.Contains(t, out, "level=WARN")
		assert.Contains(t, out, "IP lookup services returned different addresses")
		assert.Contains(t, out, "used=203.0.113.1")
		assert.Contains(t, out, "other=198.51.100.2")
	})

	t.Run("one service failing falls back to the other", func(t *testing.T) {
		servers, tlsConfig := startServers(t,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}),
			slowAnswer("203.0.113.1", 100*time.Millisecond),
		)
		var logs syncBuffer
		l := New(sources4(servers...), WithTLSConfig(tlsConfig), WithLogger(newLogger(&logs)))

		addr, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String())
		l.Wait()
		assert.NotContains(t, logs.String(), "level=WARN")
	})

	t.Run("a service that answers with garbage is not the winner", func(t *testing.T) {
		servers, tlsConfig := startServers(t,
			answer("<html>blocked</html>"),
			slowAnswer("203.0.113.1", 100*time.Millisecond),
		)
		l := New(sources4(servers...), WithTLSConfig(tlsConfig))

		addr, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
		assert.Equal(t, "203.0.113.1", addr.String())
	})

	t.Run("all services failing is an error", func(t *testing.T) {
		servers, tlsConfig := startServers(t,
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
			}),
			answer("not an address"),
		)
		l := New(sources4(servers...), WithTLSConfig(tlsConfig))

		_, err := l.Lookup(t.Context(), 4)
		require.ErrorContains(t, err, "IP lookup failed")
		require.ErrorContains(t, err, "status code 502")
		require.ErrorContains(t, err, "response is not an IP address")
	})

	t.Run("only the services for the family are asked", func(t *testing.T) {
		var hits atomic.Int32
		servers, tlsConfig := startServers(t, answer("203.0.113.1"), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
		}))
		l := New([]Source{
			{URL: servers[0].URL, Family: 4},
			{URL: servers[1].URL, Family: 6},
		}, WithTLSConfig(tlsConfig))

		_, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
		l.Wait()
		assert.Zero(t, hits.Load())
	})
}

func TestLookup_NoLeakedGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()

	servers, tlsConfig := startServers(t,
		answer("203.0.113.1"),
		slowAnswer("203.0.113.1", 10*time.Second),
		slowAnswer("203.0.113.1", 10*time.Second),
	)
	l := New(sources4(servers...), WithTLSConfig(tlsConfig), WithTimeout(time.Minute))

	// The fast service wins, and the context is canceled while the others are still running in the background
	ctx, cancel := context.WithCancel(t.Context())
	_, err := l.Lookup(ctx, 4)
	require.NoError(t, err)
	cancel()

	done := make(chan struct{})
	go func() {
		l.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("background lookups did not stop when the context was canceled")
	}

	// A canceled context also stops lookups that have not returned yet
	ctx2, cancel2 := context.WithCancel(t.Context())
	cancel2()
	_, err = l.Lookup(ctx2, 4)
	require.Error(t, err)
	l.Wait()

	for _, srv := range servers {
		srv.CloseClientConnections()
	}
	assert.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= before+len(servers)*2
	}, 5*time.Second, 50*time.Millisecond, "goroutines should not accumulate")
}

func TestLookup_NewConnectionEveryTime(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(answer("203.0.113.1"))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())

	l := New([]Source{{URL: srv.URL, Family: 4}}, WithTLSConfig(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}))
	for range 3 {
		_, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
	}
	assert.Equal(t, int32(3), conns.Load(), "a reused connection would keep reporting the address of the old network path")
}

func TestLookup_SecretsAreRedacted(t *testing.T) {
	const secret = "s3cret-token"

	t.Run("in errors", func(t *testing.T) {
		servers, tlsConfig := startServers(t, answer("not an address"))
		srv := servers[0]
		// The URL with a token in the userinfo and in the query
		withSecret := strings.Replace(srv.URL, "https://", "https://user:"+secret+"@", 1) + "/ip?token=" + secret
		l := New([]Source{{URL: withSecret, Family: 4}}, WithTLSConfig(tlsConfig))

		_, err := l.Lookup(t.Context(), 4)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secret)
		assert.Contains(t, err.Error(), srv.URL+"/ip")
	})

	t.Run("in connection errors", func(t *testing.T) {
		servers, tlsConfig := startServers(t, answer("203.0.113.1"))
		srv := servers[0]
		url := srv.URL + "/ip?token=" + secret
		srv.Close()

		l := New([]Source{{URL: url, Family: 4}}, WithTLSConfig(tlsConfig))
		_, err := l.Lookup(t.Context(), 4)
		require.Error(t, err)
		assert.NotContains(t, err.Error(), secret, "net/http puts the URL in its errors")
	})

	t.Run("in logs", func(t *testing.T) {
		servers, tlsConfig := startServers(t, answer("203.0.113.1"), slowAnswer("198.51.100.2", 100*time.Millisecond))
		var logs syncBuffer
		l := New([]Source{
			{URL: servers[0].URL + "/ip?token=" + secret, Family: 4},
			{URL: strings.Replace(servers[1].URL, "https://", "https://user:"+secret+"@", 1), Family: 4},
		}, WithTLSConfig(tlsConfig), WithLogger(newLogger(&logs)))

		_, err := l.Lookup(t.Context(), 4)
		require.NoError(t, err)
		l.Wait()
		assert.Contains(t, logs.String(), "level=WARN")
		assert.NotContains(t, logs.String(), secret)
	})
}

func TestParseResponse(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		family  int
		want    string
		wantErr string
	}{
		{name: "IPv6 is canonicalized", body: " 2001:0DB8:0:0:0:0:0:1 ", family: 6, want: "2001:db8::1"},
		{name: "wrong family 6", body: "203.0.113.7", family: 6, wantErr: "got 203.0.113.7, but an IPv6 address is expected"},
		{name: "mapped IPv4 is not IPv6", body: "::ffff:203.0.113.7", family: 6, wantErr: "an IPv6 address is expected"},
		{name: "IPv6 loopback", body: "::1", family: 6, wantErr: "is not a public IP address"},
		{name: "IPv6 unique local", body: "fd00::1", family: 6, wantErr: "is not a public IP address"},
		{name: "IPv6 link-local", body: "fe80::1", family: 6, wantErr: "is not a public IP address"},
		{name: "IPv6 with a zone", body: "fe80::1%eth0", family: 6, wantErr: "is not a public IP address"},
		{name: "IPv6 unspecified", body: "::", family: 6, wantErr: "is not a public IP address"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, err := ParseResponse(tc.body, tc.family, nil)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, addr.String())
		})
	}
}

func TestValidateURL(t *testing.T) {
	require.NoError(t, ValidateURL("https://ip.example.com"))
	require.NoError(t, ValidateURL("https://ip.example.com:8443/ip?token=x"))
	require.ErrorContains(t, ValidateURL("http://ip.example.com"), "must be an absolute https URL")
	require.ErrorContains(t, ValidateURL("/ip"), "must be an absolute https URL")
	require.ErrorContains(t, ValidateURL("ftp://example.com"), "must be an absolute https URL")
	require.ErrorContains(t, ValidateURL("https://"), "must be an absolute https URL")
}

func TestRedactURL(t *testing.T) {
	assert.Equal(t, "https://ip.example.com/path", RedactURL("https://user:pass@ip.example.com/path?token=abc#frag"))
	assert.Equal(t, "https://ip.example.com", RedactURL("https://ip.example.com"))
	assert.Equal(t, "(invalid URL)", RedactURL("::not a url"))
	assert.Equal(t, "(invalid URL)", RedactURL("no-host"))
}
