package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	configkit "github.com/italypaleale/go-kit/config"
	"github.com/italypaleale/go-kit/observability"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck"
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
	cfg := config.Get()
	err := configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	if err != nil {
		var ce *configkit.ConfigError
		if errors.As(err, &ce) {
			ce.LogFatal(initLogger)
		} else {
			utils.FatalError(initLogger, "Failed to load configuration", err)
			return
		}
	}

	shutdowns := &shutdownManager{
		fns: make([]servicerunner.Service, 0, 2),
	}

	// Get the logger and set it in the context
	log, loggerShutdownFn, err := observability.InitLogs(context.Background(), observability.InitLogsOpts{
		Config:     cfg,
		Level:      cfg.Logs.Level,
		JSON:       cfg.Logs.JSON,
		AppName:    buildinfo.AppName,
		AppVersion: buildinfo.AppVersion,
	})
	if err != nil {
		utils.FatalError(initLogger, "Failed to create logger", err)
		return
	}
	slog.SetDefault(log)
	shutdowns.Add(loggerShutdownFn)

	// Validate the configuration
	err = cfg.Validate(log)
	if err != nil {
		shutdowns.Run(log)
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
		shutdowns.Run(log)
		utils.FatalError(log, "Failed to init metrics", err)
		return
	}
	shutdowns.Add(metricsShutdownFn)

	// Initialize DNS providers
	dnsProviders := make(map[string]dns.Provider, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		var provider dns.Provider
		provider, err = dns.NewProvider(name, &pc, metrics)
		if err != nil {
			shutdowns.Run(log)
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
			shutdowns.Run(log)
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
			shutdowns.Run(log)
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
		shutdowns.Run(log)
		utils.FatalError(log, "Failed to run service", err)
		return
	}

	shutdowns.Run(log)
}

type shutdownManager struct {
	fns []servicerunner.Service
}

func (s *shutdownManager) Add(fn servicerunner.Service) {
	if fn == nil {
		return
	}
	s.fns = append(s.fns, fn)
}

func (s *shutdownManager) Run(log *slog.Logger) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	err := servicerunner.
		NewServiceRunner(s.fns...).
		Run(shutdownCtx)
	if err != nil {
		log.Error("Error shutting down services", slog.Any("error", err))
	}
}
