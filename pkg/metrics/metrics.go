package metrics

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/config"
)

const prefix = "dd"

type AppMetrics struct {
	apiCalls     api.Float64Histogram
	healthChecks api.Int64Counter
}

func NewAppMetrics(ctx context.Context) (m *AppMetrics, shutdownFn func(ctx context.Context) error, err error) {
	cfg := config.Get()

	m = &AppMetrics{}

	resource, err := cfg.GetOtelResource(buildinfo.AppName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get OpenTelemetry resource: %w", err)
	}

	// Get the metric reader
	// If the env var OTEL_METRICS_EXPORTER is empty, we set it to "none"
	if os.Getenv("OTEL_METRICS_EXPORTER") == "" {
		_ = os.Setenv("OTEL_METRICS_EXPORTER", "none") //nolint:errcheck
	}
	mr, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize OpenTelemetry metric reader: %w", err)
	}

	mp := metric.NewMeterProvider(
		metric.WithResource(resource),
		metric.WithReader(mr),
	)
	meter := mp.Meter(prefix)

	m.healthChecks, err = meter.Int64Counter(
		prefix+"_checks",
		api.WithDescription("The number of health checks"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create "+prefix+"_checks meter: %w", err)
	}

	m.apiCalls, err = meter.Float64Histogram(
		prefix+"_api_calls",
		api.WithDescription("API calls to providers and duration in milliseconds"),
		api.WithExplicitBucketBoundaries(20, 50, 100, 200, 400, 600, 800, 1000, 1500, 2500),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create "+prefix+"_api_calls meter: %w", err)
	}

	return m, mp.Shutdown, nil
}

func (m *AppMetrics) RecordHealthCheck(domain string, endpoint string, ok bool) {
	if m == nil {
		return
	}

	m.healthChecks.Add(
		context.Background(),
		1,
		api.WithAttributeSet(
			attribute.NewSet(
				attribute.KeyValue{Key: "domain", Value: attribute.StringValue(domain)},
				attribute.KeyValue{Key: "endpoint", Value: attribute.StringValue(endpoint)},
				attribute.KeyValue{Key: "ok", Value: attribute.BoolValue(ok)},
			),
		),
	)
}

func (m *AppMetrics) RecordAPICall(provider string, method string, path string, ok bool, duration time.Duration) {
	if m == nil {
		return
	}

	m.apiCalls.Record(
		context.Background(),
		float64(duration.Microseconds())/1000,
		api.WithAttributeSet(
			attribute.NewSet(
				attribute.KeyValue{Key: "provider", Value: attribute.StringValue(provider)},
				attribute.KeyValue{Key: "method", Value: attribute.StringValue(method)},
				attribute.KeyValue{Key: "path", Value: attribute.StringValue(path)},
				attribute.KeyValue{Key: "ok", Value: attribute.BoolValue(ok)},
			),
		),
	)
}
