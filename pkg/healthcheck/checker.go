package healthcheck

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
)

const DefaultTimeout = 5 * time.Second

// Checker performs health checks on configured endpoints
type Checker struct {
	endpoints []*config.ConfigEndpoint
	client    *http.Client
}

// Result represents the result of a health check
type Result struct {
	Endpoint *config.ConfigEndpoint
	Healthy  bool
	Error    error
	Duration time.Duration
}

// New creates a new health checker
func New(endpoints []*config.ConfigEndpoint) *Checker {
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &Checker{
		endpoints: endpoints,
		client:    client,
	}
}

// CheckAll performs health checks on all configured endpoints concurrently
func (c *Checker) CheckAll(ctx context.Context) []Result {
	var wg sync.WaitGroup
	results := make([]Result, len(c.endpoints))

	for i, endpoint := range c.endpoints {
		wg.Add(1)
		go func(i int, endpoint *config.ConfigEndpoint) {
			defer wg.Done()
			results[i] = c.checkEndpoint(ctx, endpoint)
		}(i, endpoint)
	}

	wg.Wait()
	return results
}

// checkEndpoint performs a health check on a single endpoint
func (c *Checker) checkEndpoint(ctx context.Context, endpoint *config.ConfigEndpoint) Result {
	start := time.Now()

	// Create a context with timeout for this specific endpoint
	timeout := endpoint.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	endpointCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create HTTP request
	req, err := http.NewRequestWithContext(endpointCtx, http.MethodGet, endpoint.URL, nil)
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
	// For TLS requests, we set it the TLS client for SNI in the TLS handshake to work too
	client := c.client
	if endpoint.Host != "" {
		req.Host = endpoint.Host

		if req.URL.Scheme == "https" {
			var transport *http.Transport
			if client.Transport != nil {
				transport = client.Transport.(*http.Transport).Clone()
				if transport.TLSClientConfig == nil {
					transport.TLSClientConfig = &tls.Config{}
				}
				transport.TLSClientConfig.ServerName = endpoint.Host
			} else {
				transport = &http.Transport{
					TLSClientConfig: &tls.Config{
						ServerName: endpoint.Host,
					},
				}
			}
			client.Transport = transport
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
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
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
