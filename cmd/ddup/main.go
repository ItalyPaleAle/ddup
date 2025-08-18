package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	yaml "sigs.k8s.io/yaml/goyaml.v3"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck"
	"github.com/italypaleale/ddup/pkg/signals"
)

func main() {
	// Get a context that is canceled when the application is stopping
	ctx := signals.SignalContext(context.Background())

	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize DNS provider
	dnsProvider, err := dns.NewProvider(cfg.DNS)
	if err != nil {
		log.Fatalf("Failed to initialize DNS provider: %v", err)
	}

	// Initialize health checker
	checker := healthcheck.New(cfg.Endpoints)

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start health check loop
	go func() {
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()

		// Run initial health check
		runHealthCheckAndUpdateDNS(ctx, checker, dnsProvider, cfg.DNS)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runHealthCheckAndUpdateDNS(ctx, checker, dnsProvider, cfg.DNS)
			}
		}
	}()

	log.Printf("Health checker started, checking every %v", cfg.Interval)

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down...")
}

func loadConfig(path string) (*config.Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg config.Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	return &cfg, nil
}

func runHealthCheckAndUpdateDNS(ctx context.Context, checker *healthcheck.Checker, dnsProvider dns.Provider, dnsConfig config.DNSConfig) {
	// Perform health checks
	results := checker.CheckAll(ctx)

	// Collect healthy IPs
	var healthyIPs []string
	for _, result := range results {
		if result.Healthy {
			healthyIPs = append(healthyIPs, result.Endpoint.IP)
			log.Printf("✓ %s (%s) is healthy", result.Endpoint.Name, result.Endpoint.IP)
		} else {
			log.Printf("✗ %s (%s) failed: %v", result.Endpoint.Name, result.Endpoint.IP, result.Error)
		}
	}

	// Update DNS records
	if len(healthyIPs) > 0 {
		err := dnsProvider.UpdateRecords(ctx, dnsConfig.RecordName, healthyIPs)
		if err != nil {
			log.Printf("Failed to update DNS records: %v", err)
		} else {
			log.Printf("Updated DNS records for %s with %d healthy IPs: %v", dnsConfig.RecordName, len(healthyIPs), healthyIPs)
		}
	} else {
		log.Printf("No healthy endpoints found, not updating DNS")
	}
}
