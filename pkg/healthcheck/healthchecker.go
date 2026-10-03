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
	"github.com/italypaleale/ddup/pkg/notify"
	"github.com/italypaleale/ddup/pkg/utils"
)

// HealthChecker manages health checking and DNS updates
type HealthChecker struct {
	// Key is domain name
	domainCheckers map[string]*domainChecker
	// Requests to run a check right away; handled by Run, so checks never overlap
	forceCh chan forceRequest
	// Optional; may be nil
	notifier *notify.Notifier
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
func NewHealthChecker(dnsProviders map[string]dns.Provider, metrics *appmetrics.AppMetrics, notifier *notify.Notifier) (*HealthChecker, error) {
	cfg := config.Get()

	dcs := make(map[string]*domainChecker, len(cfg.Domains))
	for _, d := range cfg.Domains {
		provider, ok := dnsProviders[d.Provider]
		if !ok || provider == nil {
			return nil, fmt.Errorf("domain '%s' references DNS provider '%s' that is not configured", d.RecordName, d.Provider)
		}
		endpoints := make(map[string]*config.ConfigEndpoint, len(d.Endpoints))
		for _, ep := range d.Endpoints {
			endpoints[ep.Target()] = ep
		}
		dcs[d.RecordName] = &domainChecker{
			endpoints:  endpoints,
			checker:    checker.New(d.RecordName, d.Endpoints, d.HealthChecks, metrics),
			ttl:        d.TTL,
			failedIPs:  make(map[string]int, 0),
			recovering: make(map[string]int, 0),
			provider:   provider,
		}
	}

	return &HealthChecker{
		domainCheckers: dcs,
		forceCh:        make(chan forceRequest, 1),
		notifier:       notifier,
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
		results := dedupeResults(dc.checker.CheckAll(ctx))

		// Collect healthy IPs
		maxAttempts := dc.checker.GetMaxAttempts()
		recoverAfter := dc.checker.GetRecoverAfter()
		recovering := dc.getRecovering()
		newHealthyIPs := make([]string, 0, len(results))
		endpointStates := make([]notify.EndpointState, 0, len(results))
		for _, result := range results {
			// Endpoints that look up their IP address report what they found; others have a fixed target
			ip := result.Target
			if ip == "" {
				ip = result.Endpoint.Target()
			}
			state := notify.EndpointState{Name: result.Endpoint.Name, IP: ip, Priority: result.Endpoint.Priority, Healthy: result.Healthy}
			if result.Error != nil {
				state.Error = result.Error.Error()
			}
			endpointStates = append(endpointStates, state)

			// An endpoint whose IP address has never been found has nothing we can publish or keep track of
			if ip == "" {
				domainLog.WarnContext(ctx, "✗ Endpoint has no IP address", "endpoint", result.Endpoint.Name, "error", result.Error)
				continue
			}

			// If the endpoint is healthy, save it in the healthy list and remove any record of recent failed attempts
			if result.Healthy {
				domainLog.DebugContext(ctx, "✓ Endpoint is healthy", "endpoint", result.Endpoint.Name, "ip", ip)

				// An endpoint that was removed from DNS must pass recoverAfter consecutive checks before it's added back
				// Endpoints that were not removed (including all endpoints on the first run) are added right away
				if failedIPs[ip] >= maxAttempts && recoverAfter > 1 {
					recovering[ip]++
					if recovering[ip] < recoverAfter {
						domainLog.InfoContext(ctx, "Endpoint is recovering", "endpoint", result.Endpoint.Name, "ip", ip, "passed", recovering[ip], "required", recoverAfter)
						continue
					}
				}

				newHealthyIPs = append(newHealthyIPs, ip)
				delete(failedIPs, ip)
				delete(recovering, ip)
				continue
			}

			// Endpoint is unhealthy
			domainLog.WarnContext(ctx, "✗ Endpoint health check failed", "endpoint", result.Endpoint.Name, "ip", ip, "error", result.Error)
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

		dc.noteTargets(results, newHealthyIPs, currentHealthyIPs, slices.Collect(maps.Keys(failedIPs)))
		endpoints := dc.getEndpoints()

		// Only the healthy endpoints with the lowest priority value are published
		// The previous publication is derived from the previous healthy endpoints in the same way, so we only touch DNS when what's published changes
		newPub := selectPublication(endpoints, newHealthyIPs)
		prevPub := selectPublication(endpoints, currentHealthyIPs)
		for i := range endpointStates {
			endpointStates[i].Active = slices.Contains(newPub.values(), endpointStates[i].IP)
		}

		event := notify.Event{
			Domain:       domainName,
			Healthy:      newHealthyIPs,
			Published:    newPub.values(),
			Tier:         newPub.priority,
			PreviousTier: prevPub.priority,
			Previous:     currentHealthyIPs,
			Endpoints:    endpointStates,
		}

		// Notify when we transition to having no healthy endpoints; we don't repeat the notification on every cycle
		allDown := len(newHealthyIPs) == 0
		if allDown && !dc.swapAllDown(true) {
			event.Type = notify.EventAllUnhealthy
			hc.notifier.Notify(ctx, event)
		} else if !allDown {
			dc.swapAllDown(false)
		}

		// Check if what we publish has changed
		if !utils.ElementsMatch(prevPub.keys(), newPub.keys()) {
			// Update DNS records
			if len(newHealthyIPs) > 0 {
				var res dns.UpdateResult
				res, err = dc.provider.UpdateRecords(ctx, dc.checker.GetDomain(), dc.ttl, newPub.targets)
				if err != nil {
					domainLog.ErrorContext(ctx, "Error updating DNS records", "error", err)
					dc.setError("Error updating DNS records: " + err.Error())

					// Notify once per distinct error
					if dc.swapNotifiedError(err.Error()) != err.Error() {
						event.Type = notify.EventDNSUpdateFailed
						event.Error = err.Error()
						hc.notifier.Notify(ctx, event)
					}

					// Continue, so we don't update the cached previous IPs
					continue
				}

				dc.swapNotifiedError("")
				if res.Changed {
					domainLog.InfoContext(ctx, "Updated DNS records", "targets", event.Published, "priority", newPub.priority, "previous", res.Previous)
					event.Type = notify.EventDNSUpdated
					event.Previous = res.Previous
					hc.notifier.Notify(ctx, event)
				} else {
					// For example on startup, when DNS already reflects the healthy endpoints
					domainLog.InfoContext(ctx, "DNS records already up to date", "targets", event.Published, "priority", newPub.priority)
				}
			} else {
				domainLog.WarnContext(ctx, "No healthy endpoints found, not updating DNS")
			}
		} else {
			domainLog.DebugContext(ctx, "Published targets unchanged, skipping DNS update", "healthy", newHealthyIPs, "published", event.Published)
		}

		// Update the stored previous IPs
		dc.setState(newHealthyIPs, failedIPs)
	}
}
