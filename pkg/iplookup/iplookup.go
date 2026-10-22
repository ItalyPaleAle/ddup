// Package iplookup finds the public IP address of the network ddup runs in, by asking HTTPS services that return the address of the caller
package iplookup

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout is the time each service is given to answer
	DefaultTimeout = 3 * time.Second

	// Responses larger than this are rejected; a response with an IP address is tiny
	maxResponseSize = 4 << 10

	userAgent = "ddup/1.0 (iplookup)"
)

// Source is a service that returns the IP address of the caller
type Source struct {
	// URL of the service; it must use https
	URL string

	// IP version the service answers for, 4 or 6
	// Requests to the service are made over that version only, so the service sees (and returns) the address of that family
	Family int
}

// Options configures a Lookuper
type Options struct {
	// Time each service is given to answer
	// If not positive, DefaultTimeout is used
	Timeout time.Duration

	// Logger to use
	// By default it's slog's default logger
	Logger *slog.Logger

	// TLS config used to connect to the services
	// By default, the system's root CAs are used
	TLSConfig *tls.Config
}

// Lookuper looks up the public IP address by racing the services in a list of sources
type Lookuper struct {
	sources []Source
	timeout time.Duration
	log     *slog.Logger
	clients map[int]*http.Client

	// Tracks the lookups that continue in the background after the answer has been returned
	wg sync.WaitGroup
}

// New returns a Lookuper that uses the given services
// There are no built-in services: the caller decides which ones are contacted
func New(sources []Source, opts Options) *Lookuper {
	l := &Lookuper{
		sources: sources,
		timeout: DefaultTimeout,
		log:     opts.Logger,
	}
	if opts.Timeout > 0 {
		l.timeout = opts.Timeout
	}

	l.clients = map[int]*http.Client{
		4: newClient("tcp4", opts.TLSConfig),
		6: newClient("tcp6", opts.TLSConfig),
	}
	return l
}

// newClient returns an HTTP client that connects over the given network only ("tcp4" or "tcp6")
func newClient(network string, tlsConfig *tls.Config) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert

	// Every lookup must open a new connection: a pooled connection would keep reporting the address of the network path it was opened on, even after the connection to the internet changes
	transport.DisableKeepAlives = true

	// Without this, a dual-stack service could answer over the other IP version than the one that was asked for
	var dialer net.Dialer
	transport.DialContext = func(ctx context.Context, _ string, addr string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, addr)
	}

	if tlsConfig != nil {
		transport.TLSClientConfig = tlsConfig
	}

	return &http.Client{
		Transport: transport,
		// A redirect could send the request somewhere that was not configured
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Wait blocks until the lookups that were still running in the background when Lookup returned are done
// Each of them is bounded by the timeout and by the context passed to Lookup
func (l *Lookuper) Wait() {
	l.wg.Wait()
}

type result struct {
	source Source
	addr   netip.Addr
	err    error
}

// Lookup returns the public IP address of the given family, 4 or 6
// All the services for that family are asked at the same time and the first valid answer is returned
// The others finish in the background, and a warning is logged if they return a different address
// If all the services fail, the errors are joined
func (l *Lookuper) Lookup(ctx context.Context, family int) (netip.Addr, error) {
	var sources []Source
	for _, s := range l.sources {
		if s.Family == family {
			sources = append(sources, s)
		}
	}
	if len(sources) == 0 {
		return netip.Addr{}, fmt.Errorf("no service is configured for IPv%d", family)
	}

	// The channel is large enough for every answer, so no goroutine ever blocks on it
	results := make(chan result, len(sources))
	for _, s := range sources {
		l.wg.Go(func() {
			addr, err := l.query(ctx, s)
			results <- result{source: s, addr: addr, err: err}
		})
	}

	var errs []error
	for i := range sources {
		r := <-results
		if r.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.source.URL, r.err))
			continue
		}

		// Compare the answers that have not arrived yet with this one
		pending := len(sources) - i - 1
		if pending > 0 {
			l.wg.Go(func() {
				l.compare(ctx, r, results, pending)
			})
		}
		return r.addr, nil
	}

	return netip.Addr{}, fmt.Errorf("IP lookup failed: %w", errors.Join(errs...))
}

// compare waits for the remaining answers and warns if one is different from the one that was used
func (l *Lookuper) compare(ctx context.Context, used result, results <-chan result, pending int) {
	log := l.log
	if log == nil {
		log = slog.Default()
	}

	for range pending {
		r := <-results
		switch {
		case ctx.Err() != nil:
			// The application is shutting down
		case r.err != nil:
			log.DebugContext(ctx, "IP lookup service failed", "source", r.source.URL, "error", r.err)
		case r.addr != used.addr:
			log.WarnContext(ctx, "IP lookup services returned different addresses",
				"used", used.addr,
				"usedSource", used.source.URL,
				"other", r.addr,
				"otherSource", r.source.URL,
			)
		}
	}
}

// query asks a single service for the address
func (l *Lookuper) query(ctx context.Context, source Source) (netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := l.clients[source.Family].Do(req)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return netip.Addr{}, fmt.Errorf("status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("reading response: %w", err)
	}
	if len(body) > maxResponseSize {
		return netip.Addr{}, errors.New("response is too large")
	}

	return ParseResponse(string(body), source.Family)
}

// ParseResponse extracts and validates the IP address from the response of a service
// The response must contain only the IP address
func ParseResponse(body string, family int) (netip.Addr, error) {
	value := strings.TrimSpace(body)

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("response is not an IP address: %q", truncate(value, 64))
	}

	// An IPv4 address that some services return mapped to IPv6
	addr = addr.Unmap()

	switch {
	case family == 4 && !addr.Is4():
		return netip.Addr{}, fmt.Errorf("got %s, but an IPv4 address is expected", addr)
	case family == 6 && !addr.Is6():
		return netip.Addr{}, fmt.Errorf("got %s, but an IPv6 address is expected", addr)
	case !isPublic(addr):
		// A service that returns a loopback, private or link-local address is misconfigured or sits behind a proxy
		// Publishing it would make the domain unreachable, or point it at a private network, so it's treated as an error
		return netip.Addr{}, fmt.Errorf("%s is not a public IP address", addr)
	}

	return addr, nil
}

// cgnatPrefix is the shared address space used by carrier-grade NAT (RFC 6598)
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// isPublic returns false for loopback, private, carrier-grade NAT, link-local, multicast and unspecified addresses, and for addresses with a zone (like fe80::1%eth0)
func isPublic(addr netip.Addr) bool {
	return addr.IsGlobalUnicast() && !addr.IsPrivate() && !cgnatPrefix.Contains(addr) && addr.Zone() == ""
}

// ValidateURL checks that a service URL is an absolute https URL
// Plain http is not accepted, as an answer that is not authenticated could be spoofed, and the address would be published
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("is not a valid URL")
	}
	if u.Scheme != "https" || u.Host == "" {
		return errors.New("must be an absolute https URL")
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
