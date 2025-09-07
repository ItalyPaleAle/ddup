package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck"
	"github.com/italypaleale/ddup/pkg/logging"
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
	"github.com/italypaleale/ddup/pkg/servicerunner"
	"github.com/italypaleale/ddup/pkg/signals"
	"github.com/italypaleale/ddup/pkg/utils"
)

func main() {
	// Init a logger used for initialization only, to report initialization errors
	initLogger := slog.Default().
		With(slog.String("app", buildinfo.AppName)).
		With(slog.String("version", buildinfo.AppVersion))

	// Load config
	err := config.LoadConfig()
	if err != nil {
		var ce *config.ConfigError
		if errors.As(err, &ce) {
			ce.LogFatal(initLogger)
		} else {
			utils.FatalError(initLogger, "Failed to load configuration", err)
			return
		}
	}
	cfg := config.Get()

	// Shutdown functions
	shutdownFns := make([]servicerunner.Service, 0)

	// Get the logger and set it in the context
	log, loggerShutdownFn, err := logging.GetLogger(context.Background(), cfg)
	if err != nil {
		utils.FatalError(initLogger, "Failed to create logger", err)
		return
	}
	slog.SetDefault(log)
	if loggerShutdownFn != nil {
		shutdownFns = append(shutdownFns, loggerShutdownFn)
	}

	// Validate the configuration
	err = cfg.Validate(log)
	if err != nil {
		utils.FatalError(log, "Invalid configuration", err)
		return
	}

	log.Info("Starting ddup", "build", buildinfo.BuildDescription)

	// Get a context that is canceled when the application receives a termination signal
	// We store the logger in the context too
	ctx := utils.LogToContext(context.Background(), log)
	ctx = signals.SignalContext(ctx)

	// Init metrics
	metrics, metricsShutdownFn, err := appmetrics.NewAppMetrics(ctx)
	if err != nil {
		utils.FatalError(log, "Failed to init metrics", err)
		return
	}
	if metricsShutdownFn != nil {
		shutdownFns = append(shutdownFns, metricsShutdownFn)
	}

	// Initialize DNS providers
	dnsProviders := make(map[string]dns.Provider, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		var provider dns.Provider
		provider, err = dns.NewProvider(&pc, metrics)
		if err != nil {
			utils.FatalError(log, "Failed to init DNS provider '"+name+"'", err)
			return
		}
		dnsProviders[name] = provider
	}

	// Initialize health checker
	hc, err := NewHealthChecker(dnsProviders, metrics)
	if err != nil {
		utils.FatalError(log, "Failed to init health checker", err)
		return
	}

	// Run all services
	// This call blocks until the context is canceled
	err = servicerunner.
		NewServiceRunner(hc.Run).
		Run(ctx)
	if err != nil {
		utils.FatalError(log, "Failed to run service", err)
		return
	}

	// Invoke all shutdown functions
	// We give these a timeout of 5s
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	err = servicerunner.
		NewServiceRunner(shutdownFns...).
		Run(shutdownCtx)
	if err != nil {
		log.Error("Error shutting down services", slog.Any("error", err))
	}
}

// HealthChecker manages health checking and DNS updates
type HealthChecker struct {
	domainCheckers []domainChecker
}

type domainChecker struct {
	checker    *healthcheck.Checker
	ttl        int
	healthyIPs []string
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
			ttl:      d.TTL,
			checker:  healthcheck.New(d.RecordName, d.Endpoints, metrics),
			provider: provider,
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
		var newHealthyIPs []string
		for _, result := range results {
			if result.Healthy {
				newHealthyIPs = append(newHealthyIPs, result.Endpoint.IP)
				domainLog.DebugContext(ctx, "✓ Endpoint is healthy", "endpoint", result.Endpoint.Name, "ip", result.Endpoint.IP)
			} else {
				domainLog.WarnContext(ctx, "✗ Endpoint health check failed", "endpoint", result.Endpoint.Name, "ip", result.Endpoint.IP, "error", result.Error)
			}
		}

		// Check if healthy IPs have changed
		if !utils.ElementsMatch(dc.healthyIPs, newHealthyIPs) {
			// Update DNS records
			if len(newHealthyIPs) > 0 {
				err = dc.provider.UpdateRecords(ctx, dc.checker.GetDomain(), dc.ttl, newHealthyIPs)
				if err != nil {
					domainLog.ErrorContext(ctx, "Error updating DNS records", "error", err)
				} else {
					domainLog.InfoContext(ctx, "Updated DNS records", "ips", newHealthyIPs)
				}
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
