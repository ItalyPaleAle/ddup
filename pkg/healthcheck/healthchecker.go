package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
	"github.com/italypaleale/ddup/pkg/utils"
)

// HealthChecker manages health checking and DNS updates
type HealthChecker struct {
	// Key is domain name
	domainCheckers map[string]*domainChecker
}

// NewHealthChecker creates a new HealthChecker instance
func NewHealthChecker(dnsProviders map[string]dns.Provider, metrics *appmetrics.AppMetrics) (*HealthChecker, error) {
	cfg := config.Get()

	dcs := make(map[string]*domainChecker, len(cfg.Domains))
	for _, d := range cfg.Domains {
		provider, ok := dnsProviders[d.Provider]
		if !ok || provider == nil {
			return nil, fmt.Errorf("domain '%s' references DNS provider '%s' that is not configured", d.RecordName, d.Provider)
		}
		dcs[d.RecordName] = &domainChecker{
			checker:   checker.New(d.RecordName, d.Endpoints, d.HealthChecks, metrics),
			ttl:       d.TTL,
			failedIPs: make(map[string]int, 0),
			provider:  provider,
		}
	}

	return &HealthChecker{
		domainCheckers: dcs,
	}, nil
}

func (hc *HealthChecker) Run(ctx context.Context) error {
	cfg := config.Get()

	slog.InfoContext(ctx, "Health checker started", "interval", cfg.Interval)

	// Run immediately
	hc.checkAndUpdateDNS(ctx)

	// Run on an interval until the context is canceled
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			hc.checkAndUpdateDNS(ctx)
		}
	}
}

// checkAndUpdateDNS performs health checks and updates DNS if needed
func (hc *HealthChecker) checkAndUpdateDNS(ctx context.Context) {
	var err error

	for domainName, dc := range hc.domainCheckers {
		domainLog := slog.With("domain", domainName)

		// Get the list of currently healthy and failed IPs
		// We clone the failed IPs map to prevent concurrent access
		currentHealthyIPs, failedIPs, _, _ := dc.getState()
		failedIPs = maps.Clone(failedIPs)

		// Perform health checks for this domain
		results := dc.checker.CheckAll(ctx)

		// Collect healthy IPs
		newHealthyIPs := make([]string, 0, len(results))
		for _, result := range results {
			ip := result.Endpoint.IP

			// If the endpoint is healthy, save it in the healthy list and remove any record of recent failed attempts
			if result.Healthy {
				domainLog.DebugContext(ctx, "✓ Endpoint is healthy", "endpoint", result.Endpoint.Name, "ip", ip)
				newHealthyIPs = append(newHealthyIPs, ip)
				delete(failedIPs, ip)
				continue
			}

			// Endpoint is unhealthy
			domainLog.WarnContext(ctx, "✗ Endpoint health check failed", "endpoint", result.Endpoint.Name, "ip", ip, "error", result.Error)
			failedIPs[ip]++

			// Prevent overflows
			if failedIPs[ip] < 0 {
				failedIPs[ip] = math.MaxInt
			}

			// If the number of attempts is less than the maximum, we consider the endpoint healthy if it was healthy before
			// This is to allow for retries
			maxAttempts := dc.checker.GetMaxAttempts()
			if failedIPs[ip] < maxAttempts && slices.Contains(currentHealthyIPs, ip) {
				newHealthyIPs = append(newHealthyIPs, ip)
			}
		}

		// Check if healthy IPs have changed
		if !utils.ElementsMatch(currentHealthyIPs, newHealthyIPs) {
			// Update DNS records
			if len(newHealthyIPs) > 0 {
				err = dc.provider.UpdateRecords(ctx, dc.checker.GetDomain(), dc.ttl, newHealthyIPs)
				if err != nil {
					domainLog.ErrorContext(ctx, "Error updating DNS records", "error", err)
					dc.setError("Error updating DNS records: " + err.Error())

					// Continue, so we don't update the cached previous IPs
					continue
				}

				domainLog.InfoContext(ctx, "Updated DNS records", "ips", newHealthyIPs)
			} else {
				domainLog.WarnContext(ctx, "No healthy endpoints found, not updating DNS")
			}
		} else {
			domainLog.DebugContext(ctx, "Healthy IPs unchanged, skipping DNS update", "healthy", newHealthyIPs)
		}

		// Update the stored previous IPs
		dc.setState(newHealthyIPs, failedIPs)
	}
}
