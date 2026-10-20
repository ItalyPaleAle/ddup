---
title: "Configuration"
weight: 30
---

ddup reads its configuration from a YAML file, usually named `config.yaml` (or `config.yml`).

## Configuration file

ddup looks for the configuration file in this order:

1. The path in the `DDUP_CONFIG` environment variable, if set
2. `config.yaml` in the current folder
3. `~/.ddup/config.yaml`
4. `/etc/ddup/config.yaml`

> When running ddup in a container, mount the folder that contains `config.yaml` at `/etc/ddup`.

## Example

This configuration manages two domains in a Cloudflare zone:

```yaml
# How often to check all endpoints
interval: 30s

providers:
  # A name of your choice, used by the domains below
  cloudflare-main:
    cloudflare:
      apiToken: !file /run/secrets/cloudflare-token
      zoneId: "023e105f4ecef8ad9ca31a8372d0c353"

domains:
  # Round-robin across two servers
  - recordName: "app.example.com"
    provider: "cloudflare-main"
    ttl: 60
    healthChecks:
      timeout: 2s
      attempts: 3
      recoverAfter: 2
    endpoints:
      - name: "web1"
        url: "https://192.0.2.10/healthz"
        ip: "192.0.2.10"
        # Request https://app.example.com/healthz, but connect to 192.0.2.10
        host: "app.example.com"
      - name: "web2"
        url: "https://192.0.2.11/healthz"
        ip: "192.0.2.11"
        host: "app.example.com"

  # Failover from a primary server to a backup
  - recordName: "api.example.com"
    provider: "cloudflare-main"
    endpoints:
      - name: "primary"
        url: "http://198.51.100.20:8080/health"
        ip: "198.51.100.20"
      - name: "backup"
        url: "http://203.0.113.30:8080/health"
        ip: "203.0.113.30"
        priority: 1

server:
  enabled: true

logs:
  level: info
```

## Secrets

Any text value in the configuration file can be read from another file. Use this to keep API tokens and other secrets out of `config.yaml`, for example when they're stored in Docker secrets, Kubernetes Secrets, or systemd credentials.

```yaml
providers:
  cloudflare-main:
    cloudflare:
      # Reads the whole file
      apiToken: !file /run/secrets/cloudflare-token
      # Reads the variable CF_ZONE_ID from a dotenv file
      zoneId: !envfile secrets.env CF_ZONE_ID
```

- `!file <path>` reads the whole file. Trailing newlines are removed, and other whitespace is kept.
- `!envfile <path> <name>` reads the variable `<name>` from a dotenv file.
- Relative paths are relative to the folder that contains the configuration file.
- The tag and its value must be on the same line. Quotes are optional, but needed when the path contains characters such as ` #` or `: `, for example `!file "/path/to/my secret.txt"`.
- The values are always text: they can't be used for numbers or booleans, such as `ttl` or `server.enabled`.

Dotenv files have one `NAME=value` per line, optionally starting with `export`. Lines starting with `#` are comments.

```sh
# Comment
CF_ZONE_ID=abc123
export OVH_API_KEY='pa$$word'
OVH_API_SECRET="line1\nline2"
```

- Unquoted values have spaces at the start and end removed. Anything after ` #` is a comment.
- Values in single quotes are used as they are.
- Values in double quotes support the escape sequences `\n`, `\r`, `\t`, `\"`, and `\\`.
- Values can't span multiple lines. References to other variables, such as `$NAME`, are not expanded.

## Configuration reference

Durations are written as a number and a unit, such as `500ms`, `30s`, `1m`, or `1m30s`.

### Global options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `interval` | duration | `30s` | How often to check all endpoints |
| `domains` | list | **required** | The domains to manage, see [`domains`](#domains) |
| `providers` | map | **required** | The DNS providers, see [`providers`](#providers) |
| `server` | object | | The dashboard and API, see [`server`](#server) |
| `logs` | object | | Logging options, see [`logs`](#logs) |

### `domains`

Each item in the `domains` list is a DNS name that ddup manages.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `recordName` | string | **required** | Full DNS name to manage, such as `app.example.com`. It must be in the zone of the provider. |
| `provider` | string | **required** | Name of the provider to use, from the [`providers`](#providers) map |
| `ttl` | number | `120` | TTL of the records ddup creates, in seconds. A short TTL makes clients notice changes sooner. |
| `healthChecks` | object | | How endpoints are checked, see [`healthChecks`](#domainshealthchecks) |
| `endpoints` | list | **required** | The endpoints that can serve this domain, see [`endpoints`](#domainsendpoints) |

### `domains[].healthChecks`

These options apply to all endpoints of the domain. [Health checks](/docs/health-checks) explains how they work together.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `timeout` | duration | `3s` | How long to wait for a response |
| `attempts` | number | `2` | Number of failed checks in a row after which an endpoint is removed from DNS |
| `recoverAfter` | number | `1` | Number of successful checks in a row before a removed endpoint is added back |
| `method` | string | `GET` | HTTP method of the request: `GET` or `HEAD` |
| `expectStatus` | string | `2xx` | Status code of a healthy response: an exact code such as `"204"`, or `2xx` for any 2xx code |

### `domains[].endpoints`

Each endpoint is a server that can serve the domain.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `url` | string | **required** | URL that ddup requests to check the endpoint's health, starting with `http://` or `https://` |
| `ip` | string | **required** | IP address to publish when the endpoint is healthy. IPv4 addresses are published as A records, and IPv6 addresses as AAAA records. |
| `name` | string | The URL | Name of the endpoint, shown in logs and metrics |
| `priority` | number | `0` | Lower values are preferred: only the healthy endpoints with the lowest value are published. See [Failover with priorities](/docs/failover-priorities). |
| `host` | string | | Host name to send in the request, when `url` contains an IP address or a different host name. It's also used for TLS. See [Checking a server by IP](/docs/health-checks#checking-a-server-by-ip). |

### `providers`

A map of DNS providers. Each key is a name of your choice, which domains use in their `provider` option. Each value contains the settings for exactly one provider:

| Key | Provider |
|-----|----------|
| `awsRoute53` | [AWS Route 53](/providers/aws-route53) |
| `azure` | [Azure DNS](/providers/azure-dns) |
| `cloudflare` | [Cloudflare](/providers/cloudflare) |
| `googleCloudDns` | [Google Cloud DNS](/providers/google-cloud-dns) |
| `ovh` | [OVHcloud](/providers/ovh) |

```yaml
providers:
  cloudflare-main:
    cloudflare:
      apiToken: !file /run/secrets/cloudflare-token
      zoneId: "023e105f4ecef8ad9ca31a8372d0c353"
  ovh-eu:
    ovh:
      apiKey: !envfile ovh.env OVH_API_KEY
      apiSecret: !envfile ovh.env OVH_API_SECRET
      consumerKey: !envfile ovh.env OVH_CONSUMER_KEY
      zoneName: "example.org"
```

See each provider's page for its options.

### `server`

The server hosts the [dashboard and API](/docs/dashboard).

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `enabled` | boolean | `false` | Start the server |
| `bind` | string | `127.0.0.1` | Address to listen on. Use `0.0.0.0` to listen on all interfaces, which is needed in a container. |
| `port` | number | `7401` | Port to listen on |

> [!WARNING]
> The dashboard and API have no authentication. Only make them reachable from networks you trust.

### `notifications`

Send a message to webhooks or by email when the DNS records of a domain change, an update fails, or no endpoint is healthy. See [Notifications](/docs/notifications) for the options.

### `logs`

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `level` | string | `info` | Minimum level of the messages to log: `debug`, `info`, `warn`, or `error` |
| `json` | boolean | `false` | Write logs as JSON, one object per line, instead of text |

See [Logs and metrics](/docs/observability) for logs and metrics.

## Environment variables

| Variable | Description |
|----------|-------------|
| `DDUP_CONFIG` | Path to the configuration file |
| `OTEL_*` | OpenTelemetry options for exporting logs and metrics, see [Logs and metrics](/docs/observability) |

Some providers may support authenticating with environmental variables too. See the documentation for each provider for more info.
