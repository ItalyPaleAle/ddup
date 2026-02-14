package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/italypaleale/go-kit/observability"
	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"

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

	meter, shutdownFn, err := observability.InitMetrics(ctx, observability.InitMetricsOpts{
		Config:  cfg,
		AppName: buildinfo.AppName,
		Prefix:  prefix,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to init metrics: %w", err)
	}

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

	return m, shutdownFn, nil
}

//nolint:contextcheck
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

//nolint:contextcheck
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
