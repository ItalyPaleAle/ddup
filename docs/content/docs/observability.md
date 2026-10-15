---
title: "Logs and metrics"
weight: 70
---

ddup writes logs to the standard output, and can export logs and metrics with OpenTelemetry or expose metrics for Prometheus.

## Logs

Set the level and format in the configuration:

```yaml
logs:
  # debug, info, warn, or error
  level: info
  # Write one JSON object per line instead of text
  json: false
```

At the `info` level, ddup logs every change to DNS and every failed check. These are the messages to look for:

| Message | Level | Meaning |
|---------|-------|---------|
| `Health checker started` | info | ddup started, and is about to run the first checks |
| `✗ Endpoint health check failed` | warn | An endpoint failed a check. The `error` field says why. |
| `Endpoint is recovering` | info | A removed endpoint passed a check, but hasn't reached `recoverAfter` successful checks yet |
| `Updated DNS records` | info | ddup changed the records. The `ips` field lists the published IPs, and `previous` the IPs before the change. |
| `DNS records already up to date` | info | ddup was about to update the records, but they already listed the right IPs, for example when ddup restarts |
| `No healthy endpoints found, not updating DNS` | warn | No endpoint of the domain is healthy, so ddup left the records as they were |
| `Error updating DNS records` | error | The provider's API returned an error. ddup tries again on the next check. |

At the `debug` level, ddup also logs every successful check, and every check that didn't need a DNS update.

If the configuration is invalid or a provider can't be set up (for example, because of missing credentials), ddup logs the error and exits.

## Exporting with OpenTelemetry

ddup can send logs and metrics to an [OpenTelemetry](https://opentelemetry.io) collector, or to any backend that accepts OTLP, such as Grafana Cloud, Honeycomb, or Datadog. Exporting is off by default: turn it on with environment variables.

```sh
# Export metrics
export OTEL_METRICS_EXPORTER=otlp
# Export logs too, in addition to writing them to the standard output
export OTEL_LOGS_EXPORTER=otlp
# Where to send them
export OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
```

For example, with Docker:

```sh
docker run -d \
  --name ddup \
  --read-only \
  -v $HOME/.ddup:/etc/ddup:ro \
  -e OTEL_METRICS_EXPORTER=otlp \
  -e OTEL_LOGS_EXPORTER=otlp \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318 \
  ghcr.io/italypaleale/ddup:v0
```

| Variable | Description | Default |
|----------|-------------|---------|
| `OTEL_METRICS_EXPORTER` | `otlp` to export metrics, `prometheus` to expose them for Prometheus, or `console` to print them | `none` |
| `OTEL_LOGS_EXPORTER` | `otlp` to export logs, or `console` to print them | `none` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Address of the OTLP collector | `http://localhost:4318` |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | `http/protobuf` or `grpc` | `http/protobuf` |
| `OTEL_EXPORTER_OTLP_HEADERS` | Headers to send to the collector, such as API keys, as `key1=value1,key2=value2` | |
| `OTEL_RESOURCE_ATTRIBUTES` | Attributes added to all logs and metrics, as `key1=value1,key2=value2` | |

Logs and metrics are reported with the service name `ddup`. See the [OpenTelemetry documentation](https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/) for all environment variables.

## Prometheus

To expose metrics for Prometheus to scrape, set:

```sh
export OTEL_METRICS_EXPORTER=prometheus
# Listen on all interfaces (the default is localhost), for example in a container
export OTEL_EXPORTER_PROMETHEUS_HOST=0.0.0.0
# Default port
export OTEL_EXPORTER_PROMETHEUS_PORT=9464
```

Metrics are then available at `http://<host>:9464/metrics`.

## Metrics

| Metric | Type | Description | Attributes |
|--------|------|-------------|------------|
| `dd_checks` | Counter | Number of health checks | `domain`, `endpoint` (name of the endpoint), `ok` (`true` if the check passed) |
| `dd_api_calls` | Histogram | Duration of the calls to the DNS provider's API, in milliseconds | `provider` (`awsroute53`, `azure`, `cloudflare`, `googleclouddns`, or `ovh`), `method`, `path`, `ok` (`true` if the call succeeded) |

In Prometheus, the counter is named `dd_checks_total`. For example, this query shows the rate of failed checks for each endpoint:

```promql
sum by (domain, endpoint) (rate(dd_checks_total{ok="false"}[5m]))
```
