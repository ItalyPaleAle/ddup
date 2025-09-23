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
	appmetrics "github.com/italypaleale/ddup/pkg/metrics"
	"github.com/italypaleale/ddup/pkg/server"
	"github.com/italypaleale/ddup/pkg/servicerunner"
	"github.com/italypaleale/ddup/pkg/signals"
	"github.com/italypaleale/ddup/pkg/utils"
)

var statusProvider healthcheck.StatusProvider

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
	ctx := signals.SignalContext(context.Background())

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
		provider, err = dns.NewProvider(name, &pc, metrics)
		if err != nil {
			utils.FatalError(log, "Failed to init DNS provider '"+name+"'", err)
			return
		}
		dnsProviders[name] = provider
	}

	// List of services to run
	services := make([]servicerunner.Service, 0, 2)

	// Initialize health checker
	// If there's a non-nil statusProvider, it means we're in the "dashboarddev" mode where we use static data
	if statusProvider == nil {
		hc, err := healthcheck.NewHealthChecker(dnsProviders, metrics)
		if err != nil {
			utils.FatalError(log, "Failed to init health checker", err)
			return
		}
		services = append(services, hc.Run)

		statusProvider = hc
	}

	// Init the server if needed
	if cfg.Server.Enabled {
		srv, err := server.NewServer(server.NewServerOpts{
			HealthChecker: statusProvider,
		})
		if err != nil {
			utils.FatalError(log, "Failed to init server", err)
			return
		}

		services = append(services, srv.Run)
	}

	// Run all services
	// This call blocks until the context is canceled
	err = servicerunner.
		NewServiceRunner(services...).
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
