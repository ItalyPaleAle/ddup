package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
)

// Checker performs health checks on configured endpoints
type Checker struct {
	endpoints []config.ConfigEndpoint
	client    *http.Client
}

// Result represents the result of a health check
type Result struct {
	Endpoint config.ConfigEndpoint
	Healthy  bool
	Error    error
	Duration time.Duration
}

// New creates a new health checker
func New(endpoints []config.ConfigEndpoint) *Checker {
	return &Checker{
		endpoints: endpoints,
		client: &http.Client{
			Timeout: 10 * time.Second, // Default timeout, can be overridden per endpoint
		},
	}
}

// CheckAll performs health checks on all configured endpoints concurrently
func (c *Checker) CheckAll(ctx context.Context) []Result {
	var wg sync.WaitGroup
	results := make([]Result, len(c.endpoints))

	for i, endpoint := range c.endpoints {
		wg.Add(1)
		go func(i int, endpoint config.ConfigEndpoint) {
			defer wg.Done()
			results[i] = c.checkEndpoint(ctx, endpoint)
		}(i, endpoint)
	}

	wg.Wait()
	return results
}

// checkEndpoint performs a health check on a single endpoint
func (c *Checker) checkEndpoint(ctx context.Context, endpoint config.ConfigEndpoint) Result {
	start := time.Now()

	// Create a context with timeout for this specific endpoint
	timeout := endpoint.Timeout
	if timeout == 0 {
		timeout = 5 * time.Second // Default timeout
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

	// Perform the request
	resp, err := c.client.Do(req)
	if err != nil {
		return Result{
			Endpoint: endpoint,
			Healthy:  false,
			Error:    fmt.Errorf("HTTP request failed: %w", err),
			Duration: time.Since(start),
		}
	}
	defer resp.Body.Close() //nolint:errcheck

	// Check if status code indicates health
	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300
	var checkErr error
	if !healthy {
		checkErr = fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return Result{
		Endpoint: endpoint,
		Healthy:  healthy,
		Error:    checkErr,
		Duration: time.Since(start),
	}
}
