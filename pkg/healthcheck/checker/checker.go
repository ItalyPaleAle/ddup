package checker

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/iplookup"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
)

const (
	DefaultTimeout  = 3 * time.Second
	DefaultAttempts = 2
)

// Checker performs health checks on configured endpoints
type Checker interface {
	CheckAll(ctx context.Context) []Result
	GetDomain() string
	GetMaxAttempts() int
	GetRecoverAfter() int
}

// Compile time interface check
var _ Checker = (*checker)(nil)

// concrete implementation of the Checker interface
type checker struct {
	domain    string
	endpoints []*config.ConfigEndpoint
	cfg       config.ConfigHealthChecks
	metrics   *appmetrics.AppMetrics
	client    *http.Client

	// Clients used for endpoints that set a custom host over TLS, keyed by host
	hostClientsLock sync.RWMutex
	hostClients     map[string]*http.Client

	// Looks up the IP address of each endpoint that sets `ipLookupFamily`
	// Endpoints with the same list of services share a lookuper
	lookupers map[*config.ConfigEndpoint]*iplookup.Lookuper

	// Last IP address found for each endpoint that looks it up
	lastIPsLock sync.Mutex
	lastIPs     map[*config.ConfigEndpoint]string
}

// Result represents the result of a health check
type Result struct {
	Endpoint *config.ConfigEndpoint
	Healthy  bool
	Error    error
	Duration time.Duration
	// IP address to publish for the endpoint, for endpoints that look up their IP address
	// It's empty for other endpoints, and for those whose lookup has never worked
	Target string
}

// New creates a new health checker
// The lookupers find the IP address of the endpoints that set `ipLookupFamily`, and can be nil if there are none
func New(domain string, endpoints []*config.ConfigEndpoint, healthCheckConfig config.ConfigHealthChecks, metrics *appmetrics.AppMetrics, lookupers map[*config.ConfigEndpoint]*iplookup.Lookuper) *checker {
	// Every check must open a new connection: with keep-alives, a connection that stays busy (checks run every few seconds) is reused forever, so the check keeps hitting the address it first resolved, even after DNS changes or the endpoint is replaced, and it can't notice problems with connecting or with the TLS handshake
	transport := http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert
	transport.DisableKeepAlives = true

	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Set default config value
	if healthCheckConfig.Timeout <= 0 {
		healthCheckConfig.Timeout = DefaultTimeout
	}
	if healthCheckConfig.Attempts <= 0 {
		healthCheckConfig.Attempts = DefaultAttempts
	}

	return &checker{
		domain:    domain,
		endpoints: endpoints,
		cfg:       healthCheckConfig,
		metrics:   metrics,
		client:    client,
		lookupers: lookupers,
	}
}

// CheckAll performs health checks on all configured endpoints concurrently
func (c *checker) CheckAll(ctx context.Context) []Result {
	var wg sync.WaitGroup
	results := make([]Result, len(c.endpoints))

	for i, endpoint := range c.endpoints {
		wg.Add(1)
		go func(i int, endpoint *config.ConfigEndpoint) {
			defer wg.Done()
			results[i] = c.checkEndpoint(ctx, endpoint)

			if c.metrics != nil {
				c.metrics.RecordHealthCheck(c.domain, endpoint.Name, results[i].Healthy)
			}
		}(i, endpoint)
	}

	wg.Wait()
	return results
}

// GetDomain returns the domain this Checker is configured for
func (c *checker) GetDomain() string {
	return c.domain
}

// GetMaxAttempts returns the maximum number attempts the Checker is configured for
func (c *checker) GetMaxAttempts() int {
	return c.cfg.Attempts
}

// GetRecoverAfter returns the number of consecutive successful checks required to re-add a removed endpoint
func (c *checker) GetRecoverAfter() int {
	return max(c.cfg.RecoverAfter, 1)
}

// checkEndpoint finds the IP address of the endpoint (looking it up, if needed), and performs a health check on it
func (c *checker) checkEndpoint(ctx context.Context, endpoint *config.ConfigEndpoint) Result {
	if !endpoint.Dynamic() {
		return c.healthCheck(ctx, endpoint)
	}

	start := time.Now()
	ip, err := c.lookupIP(ctx, endpoint)
	if err != nil {
		// Keep reporting the last IP address we found, so the failure counts against it like a failed health check
		return Result{
			Endpoint: endpoint,
			Target:   c.getLastIP(endpoint),
			Healthy:  false,
			Error:    err,
			Duration: time.Since(start),
		}
	}
	c.setLastIP(endpoint, ip)

	// Without a URL the endpoint is not health-checked, so it's healthy when the lookup works
	res := Result{
		Endpoint: endpoint,
		Healthy:  true,
		Duration: time.Since(start),
	}
	if endpoint.URL != "" {
		res = c.healthCheck(ctx, endpoint)
	}
	res.Target = ip
	return res
}

func (c *checker) lookupIP(ctx context.Context, endpoint *config.ConfigEndpoint) (string, error) {
	lookuper := c.lookupers[endpoint]
	if lookuper == nil {
		return "", errors.New("IP lookup is not configured")
	}

	addr, err := lookuper.Lookup(ctx, endpoint.IPLookupFamily)
	if err != nil {
		return "", fmt.Errorf("looking up the IP address: %w", err)
	}
	return addr.String(), nil
}

func (c *checker) getLastIP(endpoint *config.ConfigEndpoint) string {
	c.lastIPsLock.Lock()
	defer c.lastIPsLock.Unlock()

	return c.lastIPs[endpoint]
}

func (c *checker) setLastIP(endpoint *config.ConfigEndpoint, ip string) {
	c.lastIPsLock.Lock()
	defer c.lastIPsLock.Unlock()

	if c.lastIPs == nil {
		c.lastIPs = make(map[*config.ConfigEndpoint]string)
	}
	c.lastIPs[endpoint] = ip
}

// healthCheck performs a health check on a single endpoint
func (c *checker) healthCheck(ctx context.Context, endpoint *config.ConfigEndpoint) Result {
	start := time.Now()

	// Create a context with timeout for this specific endpoint
	endpointCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()

	// Create HTTP request
	method := c.cfg.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(endpointCtx, method, endpoint.URL, nil)
	if err != nil {
		return Result{
			Endpoint: endpoint,
			Healthy:  false,
			Error:    fmt.Errorf("creating request: %w", err),
			Duration: time.Since(start),
		}
	}

	// Set user agent
	req.Header.Set("User-Agent", "ddup/1.0")

	// If there's a specific host, we need to set it in the request's host
	// For TLS requests, we use a client that sets it for SNI in the TLS handshake to work too
	client := c.client
	if endpoint.Host != "" {
		req.Host = endpoint.Host

		if req.URL.Scheme == "https" {
			client = c.clientForHost(endpoint.Host)
		}
	}

	// Perform the request
	resp, err := client.Do(req)
	if err != nil {
		return Result{
			Endpoint: endpoint,
			Healthy:  false,
			Error:    fmt.Errorf("HTTP request failed: %w", err),
			Duration: time.Since(start),
		}
	}
	_ = resp.Body.Close() //nolint:errcheck

	// Check if status code indicates health
	if !c.cfg.StatusMatches(resp.StatusCode) {
		return Result{
			Endpoint: endpoint,
			Healthy:  false,
			Error:    fmt.Errorf("status code %d", resp.StatusCode),
			Duration: time.Since(start),
		}
	}

	return Result{
		Endpoint: endpoint,
		Healthy:  true,
		Error:    nil,
		Duration: time.Since(start),
	}
}

// clientForHost returns an HTTP client that sends the given host as SNI in TLS handshakes
// Clients are created once per host and shared, and the base client is never modified, so this is safe for concurrent use
func (c *checker) clientForHost(host string) *http.Client {
	// Most calls find an existing client, so check with a read lock first
	c.hostClientsLock.RLock()
	client := c.hostClients[host]
	c.hostClientsLock.RUnlock()
	if client != nil {
		return client
	}

	c.hostClientsLock.Lock()
	defer c.hostClientsLock.Unlock()

	// Check again, as another goroutine may have created the client while we waited for the write lock
	client = c.hostClients[host]
	if client != nil {
		return client
	}

	var transport *http.Transport
	base, ok := c.client.Transport.(*http.Transport)
	if ok {
		transport = base.Clone()
	} else {
		transport = http.DefaultTransport.(*http.Transport).Clone() //nolint:forcetypeassert
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}
	transport.TLSClientConfig.ServerName = host

	client = &http.Client{
		Transport:     transport,
		CheckRedirect: c.client.CheckRedirect,
	}
	if c.hostClients == nil {
		c.hostClients = make(map[string]*http.Client)
	}
	c.hostClients[host] = client
	return client
}
