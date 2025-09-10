package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
	"github.com/italypaleale/ddup/pkg/utils"
)

// HealthChecker manages health checking and DNS updates
type HealthChecker struct {
	domainCheckers []domainChecker
}

type domainChecker struct {
	checker    healthcheck.Checker
	ttl        int
	healthyIPs []string
	failedIPs  map[string]int
	provider   dns.Provider
}

// NewHealthChecker creates a new HealthChecker instance
func NewHealthChecker(dnsProviders map[string]dns.Provider, metrics *appmetrics.AppMetrics) (*HealthChecker, error) {
	cfg := config.Get()

	dcs := make([]domainChecker, len(cfg.Domains))
	for i, d := range cfg.Domains {
		provider, ok := dnsProviders[d.Provider]
		if !ok || provider == nil {
			return nil, fmt.Errorf("domain '%s' references DNS provider '%s' that is not configured", d.RecordName, d.Provider)
		}
		dcs[i] = domainChecker{
			checker:   healthcheck.New(d.RecordName, d.Endpoints, d.HealthChecks, metrics),
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
	log := utils.LogFromContext(ctx)

	log.InfoContext(ctx, "Health checker started", "interval", cfg.Interval)

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

	log := utils.LogFromContext(ctx)

	for i := range hc.domainCheckers {
		dc := &hc.domainCheckers[i]
		domainLog := log.With("domain", dc.checker.GetDomain())

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
				delete(dc.failedIPs, ip)
				continue
			}

			// Endpoint is unhealthy
			domainLog.WarnContext(ctx, "✗ Endpoint health check failed", "endpoint", result.Endpoint.Name, "ip", ip, "error", result.Error)
			maxAttempts := dc.checker.GetMaxAttempts()
			dc.failedIPs[ip]++

			if dc.failedIPs[ip] >= maxAttempts {
				// Limit to the max attempts number to prevent overflowing
				dc.failedIPs[ip] = maxAttempts
				continue
			}

			// If the number of attempts is less than the maximum, we consider the endpoint healthy if it was healthy before
			// This is to allow for retries
			if slices.Contains(dc.healthyIPs, ip) {
				newHealthyIPs = append(newHealthyIPs, ip)
			}
		}

		// Check if healthy IPs have changed
		if !utils.ElementsMatch(dc.healthyIPs, newHealthyIPs) {
			// Update DNS records
			if len(newHealthyIPs) > 0 {
				err = dc.provider.UpdateRecords(ctx, dc.checker.GetDomain(), dc.ttl, newHealthyIPs)
				if err != nil {
					domainLog.ErrorContext(ctx, "Error updating DNS records", "error", err)

					// Continue, so we don't update the cached previous IPs
					continue
				}

				domainLog.InfoContext(ctx, "Updated DNS records", "ips", newHealthyIPs)
			} else {
				domainLog.WarnContext(ctx, "No healthy endpoints found, not updating DNS")
			}

			// Update the stored previous IPs
			dc.healthyIPs = newHealthyIPs
		} else {
			domainLog.DebugContext(ctx, "Healthy IPs unchanged, skipping DNS update", "healthy", newHealthyIPs)
		}
	}
}
