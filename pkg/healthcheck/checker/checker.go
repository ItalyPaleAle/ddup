package checker

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
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

	// Last IP address found for each endpoint that looks it up
	lastIPsLock sync.Mutex
	lastIPs     map[*config.ConfigEndpoint]string
}

// Result represents the result of a health check
type Result struct {
	Endpoint *config.ConfigEndpoint
	// What the endpoint publishes: an IP address or a CNAME hostname
	// For endpoints that look up their IP address this is the address that was found, or the last one found if the lookup failed, and it's empty if there's none yet
	Target   string
	Healthy  bool
	Error    error
	Duration time.Duration
}

// New creates a new health checker
func New(domain string, endpoints []*config.ConfigEndpoint, healthCheckConfig config.ConfigHealthChecks, metrics *appmetrics.AppMetrics) *checker {
	client := &http.Client{
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

// checkEndpoint finds the target of the endpoint (looking up its IP address, if needed), and performs a health check on it
func (c *checker) checkEndpoint(ctx context.Context, endpoint *config.ConfigEndpoint) Result {
	if !endpoint.Dynamic() {
		res := c.healthCheck(ctx, endpoint)
		res.Target = endpoint.Target()
		return res
	}

	start := time.Now()
	ip, err := lookupIP(ctx, endpoint.IPLookup, c.cfg.Timeout)
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
	res := Result{Endpoint: endpoint, Healthy: true, Duration: time.Since(start)}
	if endpoint.URL != "" {
		res = c.healthCheck(ctx, endpoint)
	}
	res.Target = ip
	return res
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
