package healthcheck

import (
	"context"
	"errors"
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
)

// HealthChecker manages health checking and DNS updates
type HealthChecker struct {
	// Key is domain name
	domainCheckers map[string]*domainChecker
	// Requests to run a check right away; handled by Run, so checks never overlap
	forceCh chan forceRequest
}

// If a check completed more recently than this, a forced check returns without running another
// Forced checks count towards `attempts` and `recoverAfter` like scheduled ones, so this keeps repeated clicks from tripping thresholds
const minForceInterval = 5 * time.Second

type forceRequest struct {
	done chan struct{}
}

// ErrNotRunning is returned by ForceCheck when the health checker's Run loop isn't active
var ErrNotRunning = errors.New("health checker is not running")

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
			checker:    checker.New(d.RecordName, d.Endpoints, d.HealthChecks, metrics),
			ttl:        d.TTL,
			priorities: endpointPriorities(d.Endpoints),
			failedIPs:  make(map[string]int, 0),
			recovering: make(map[string]int, 0),
			provider:   provider,
		}
	}

	return &HealthChecker{
		domainCheckers: dcs,
		forceCh:        make(chan forceRequest, 1),
	}, nil
}

func (hc *HealthChecker) Run(ctx context.Context) error {
	cfg := config.Get()

	slog.InfoContext(ctx, "Health checker started", "interval", cfg.Interval)

	// Run immediately
	hc.checkAndUpdateDNS(ctx)
	lastCheck := time.Now()

	// Run on an interval until the context is canceled
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			hc.checkAndUpdateDNS(ctx)
			lastCheck = time.Now()
		case req := <-hc.forceCh:
			// A check that just finished is as good as a new one
			if time.Since(lastCheck) >= minForceInterval {
				slog.InfoContext(ctx, "Running forced health check")
				hc.checkAndUpdateDNS(ctx)
				lastCheck = time.Now()
				// Restart the interval, so the next scheduled check isn't right after this one
				ticker.Reset(cfg.Interval)
			}
			close(req.done)
		}
	}
}

// ForceCheck asks the Run loop to check all domains now, and waits for it to finish
// If the context is canceled first, the check still completes in the background
func (hc *HealthChecker) ForceCheck(ctx context.Context) error {
	if hc.forceCh == nil {
		return ErrNotRunning
	}

	req := forceRequest{done: make(chan struct{})}
	select {
	case hc.forceCh <- req:
	case <-ctx.Done():
		return fmt.Errorf("waiting to start check: %w", ctx.Err())
	}

	select {
	case <-req.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("waiting for check to complete: %w", ctx.Err())
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
		maxAttempts := dc.checker.GetMaxAttempts()
		recoverAfter := dc.checker.GetRecoverAfter()
		recovering := dc.getRecovering()
		newHealthyIPs := make([]string, 0, len(results))
		targets := make(map[string]dns.Target, len(results))
		for _, result := range results {
			// The key of an endpoint is what it publishes: its IP address or its CNAME hostname
			ip := result.Endpoint.Target()

			target, targetErr := endpointTarget(result.Endpoint)
			if targetErr != nil {
				// The configuration has been validated, so this indicates a development-time error
				domainLog.ErrorContext(ctx, "Invalid endpoint", "endpoint", result.Endpoint.Name, "error", targetErr)
				continue
			}
			targets[ip] = target

			// If the endpoint is healthy, save it in the healthy list and remove any record of recent failed attempts
			if result.Healthy {
				domainLog.DebugContext(ctx, "✓ Endpoint is healthy", "endpoint", result.Endpoint.Name, "target", ip)

				// An endpoint that was removed from DNS must pass recoverAfter consecutive checks before it's added back
				// Endpoints that were not removed (including all endpoints on the first run) are added right away
				if failedIPs[ip] >= maxAttempts && recoverAfter > 1 {
					recovering[ip]++
					if recovering[ip] < recoverAfter {
						domainLog.InfoContext(ctx, "Endpoint is recovering", "endpoint", result.Endpoint.Name, "target", ip, "passed", recovering[ip], "required", recoverAfter)
						continue
					}
				}

				newHealthyIPs = append(newHealthyIPs, ip)
				delete(failedIPs, ip)
				delete(recovering, ip)
				continue
			}

			// Endpoint is unhealthy
			domainLog.WarnContext(ctx, "✗ Endpoint health check failed", "endpoint", result.Endpoint.Name, "target", ip, "error", result.Error)
			failedIPs[ip]++
			delete(recovering, ip)

			// Prevent overflows
			if failedIPs[ip] < 0 {
				failedIPs[ip] = math.MaxInt
			}

			// If the number of attempts is less than the maximum, we consider the endpoint healthy if it was healthy before
			// This is to allow for retries
			if failedIPs[ip] < maxAttempts && slices.Contains(currentHealthyIPs, ip) {
				newHealthyIPs = append(newHealthyIPs, ip)
			}
		}
		dc.setRecovering(recovering)
		dc.setTargets(targets)

		// Only the healthy endpoints with the lowest priority value are published
		// The previous selection is derived from the previous healthy endpoints in the same way, so we only touch DNS when what's published changes
		publishIPs := selectLowestPriority(dc.priorities, newHealthyIPs)
		previousPublishIPs := selectLowestPriority(dc.priorities, currentHealthyIPs)
		publishTargets := targetsFor(targets, publishIPs)
		previousTargets := targetsFor(targets, previousPublishIPs)

		// Check if the published targets have changed, or if the last update failed
		var updateErr string
		if dc.updatePending || !targetsMatch(previousTargets, publishTargets) {
			// Update DNS records
			if len(publishTargets) > 0 {
				var res dns.UpdateResult
				res, err = dc.provider.UpdateRecords(ctx, dc.checker.GetDomain(), dc.ttl, publishTargets)
				if err != nil {
					domainLog.ErrorContext(ctx, "Error updating DNS records", "error", err)
					updateErr = "Error updating DNS records: " + err.Error()

					// Try again at the next check, even if the published targets do not change
					dc.updatePending = true
				} else {
					dc.updatePending = false
					if res.Changed {
						domainLog.InfoContext(ctx, "Updated DNS records", "targets", publishIPs, "previous", res.Previous)
					} else {
						domainLog.InfoContext(ctx, "DNS records already up to date", "targets", publishIPs)
					}
				}
			} else {
				domainLog.WarnContext(ctx, "No healthy endpoints found, not updating DNS")

				// There is nothing to publish, and DNS is left as it is
				dc.updatePending = false
			}
		} else {
			domainLog.DebugContext(ctx, "Published targets unchanged, skipping DNS update", "healthy", newHealthyIPs, "published", publishIPs)
		}

		// Update the stored state, also when the update failed, so the health of the endpoints keeps being tracked
		dc.setState(newHealthyIPs, failedIPs)

		// The state resets the error, so it is set again after it
		if updateErr != "" {
			dc.setError(updateErr)
		}
	}
}

// endpointTarget returns the target that DNS records should point to for an endpoint
// The type of the record depends on which key the endpoint uses: an IP address is an A or AAAA record, depending on its family, and cname is a CNAME record
func endpointTarget(ep *config.ConfigEndpoint) (dns.Target, error) {
	if ep.CNAME != "" {
		return dns.CNAMETarget(ep.CNAME, ep.Proxied()), nil
	}
	target, err := dns.IPTarget(ep.IP, ep.Proxied())
	if err != nil {
		return dns.Target{}, fmt.Errorf("invalid IP for endpoint %s: %w", ep.Name, err)
	}
	return target, nil
}

// targetsFor returns the targets for the keys, skipping those that are not known
func targetsFor(targets map[string]dns.Target, keys []string) []dns.Target {
	res := make([]dns.Target, 0, len(keys))
	for _, key := range keys {
		t, ok := targets[key]
		if ok {
			res = append(res, t)
		}
	}
	return res
}

// targetsMatch returns true if both lists have the same targets, regardless of the order
func targetsMatch(a, b []dns.Target) bool {
	if len(a) != len(b) {
		return false
	}
	for _, t := range a {
		if !slices.Contains(b, t) {
			return false
		}
	}
	return true
}
