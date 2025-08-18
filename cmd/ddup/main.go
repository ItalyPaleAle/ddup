package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck"
	"github.com/italypaleale/ddup/pkg/logging"
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

	// Initialize DNS provider
	dnsProvider, err := dns.NewProvider(cfg.DNS)
	if err != nil {
		utils.FatalError(log, "Failed to DNS provider", err)
		return
	}

	// Initialize health checker
	checker := healthcheck.New(cfg.Endpoints)

	// Healthcheck service
	checkerSvc := func(ctx context.Context) error {
		log.InfoContext(ctx, "Health checker started", "interval", cfg.Interval)

		// Run initial health check
		runHealthCheckAndUpdateDNS(ctx, checker, dnsProvider, cfg.DNS)

		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				runHealthCheckAndUpdateDNS(ctx, checker, dnsProvider, cfg.DNS)
			}
		}
	}

	// Run all services
	// This call blocks until the context is canceled
	err = servicerunner.
		NewServiceRunner(checkerSvc).
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

func runHealthCheckAndUpdateDNS(ctx context.Context, checker *healthcheck.Checker, dnsProvider dns.Provider, dnsConfig config.ConfigDNS) {
	log := utils.LogFromContext(ctx)

	// Perform health checks
	results := checker.CheckAll(ctx)

	// Collect healthy IPs
	var healthyIPs []string
	for _, result := range results {
		if result.Healthy {
			healthyIPs = append(healthyIPs, result.Endpoint.IP)
			log.DebugContext(ctx, "✓ Endpoint is healthy", "endpoint", result.Endpoint.Name, "ip", result.Endpoint.IP)
		} else {
			log.WarnContext(ctx, "✗ Endpoint health check failed", "endpoint", result.Endpoint.Name, "ip", result.Endpoint.IP, "error", result.Error)
		}
	}

	// Update DNS records
	if len(healthyIPs) > 0 {
		err := dnsProvider.UpdateRecords(ctx, dnsConfig.RecordName, healthyIPs)
		if err != nil {
			log.ErrorContext(ctx, "Error updating DNS records", "domain", dnsConfig.RecordName, "error", err)
		} else {
			log.DebugContext(ctx, "✓ Updated DNS records", "domain", dnsConfig.RecordName, "healthy", healthyIPs)
		}
	} else {
		log.WarnContext(ctx, "No healthy endpoints found, not updating DNS", "domain", dnsConfig.RecordName)
	}
}
