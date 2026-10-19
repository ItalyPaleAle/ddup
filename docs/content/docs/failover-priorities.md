---
title: "Failover with priorities"
nav_title: "Failover priorities"
weight: 50
---

By default, ddup publishes every healthy endpoint of a domain, so clients spread across all of them. With priorities, you can keep some endpoints out of DNS until they're needed: for example, a backup server that takes over only when the main one fails.

## How priorities work

Each endpoint has a `priority`, a number that is `0` unless you set it. Lower values are preferred.

On every check, ddup looks at the healthy endpoints and publishes only those with the lowest priority value:

- Endpoints with the same priority are published together, as round-robin.
- If no endpoint with the lowest value is healthy, ddup publishes the healthy endpoints with the next value, and so on.
- When a preferred endpoint is healthy again, ddup publishes it and removes the endpoints with higher values. If the preferred endpoint had been removed, it first has to pass `recoverAfter` checks in a row, like any other endpoint (see [Health checks](/docs/health-checks#when-endpoints-are-removed-and-added-back)).
- If no endpoint is healthy at all, ddup doesn't change DNS.

ddup checks all endpoints, including those that aren't published, so it knows whether a backup is healthy before switching to it. The [dashboard](/docs/dashboard) shows a healthy backup as healthy even while it's not in DNS.

## Primary and backup server

The primary server is published as long as it's healthy. The backup is published only while the primary is unhealthy.

```yaml
domains:
  - recordName: "app.example.com"
    provider: "my-dns"
    healthChecks:
      # Switch back to the primary only after it passes 3 checks in a row
      recoverAfter: 3
    endpoints:
      - name: "primary"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "backup"
        url: "http://203.0.113.20/healthz"
        ip: "203.0.113.20"
        priority: 1
```

With these settings and the default `interval` and `attempts`:

1. While both servers are healthy, `app.example.com` resolves to `192.0.2.10`.
2. When the primary fails 2 checks in a row, ddup replaces its IP with the backup's.
3. When the primary passes 3 checks in a row, ddup switches back to it.

Keep the backup running for a while after the switch back: clients that cached its IP keep using it until the TTL expires.

## Failover between locations

Endpoints with the same priority are published together, so you can fail over between groups of servers. In this example, clients use the two servers in the main location. If both fail, they use the two servers in the second location.

```yaml
domains:
  - recordName: "app.example.com"
    provider: "my-dns"
    endpoints:
      # Main location
      - name: "main-1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "main-2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
      # Second location
      - name: "second-1"
        url: "http://198.51.100.10/healthz"
        ip: "198.51.100.10"
        priority: 1
      - name: "second-2"
        url: "http://198.51.100.11/healthz"
        ip: "198.51.100.11"
        priority: 1
```

If only one of the main servers fails, ddup removes it and keeps publishing the other one. The second location is used only when neither main server is healthy.

## More than two levels

Priorities can have any number of levels, and the values don't need to be consecutive. For example, you can add a server that shows a maintenance page, used only as a last resort:

```yaml
endpoints:
  - name: "primary"
    url: "http://192.0.2.10/healthz"
    ip: "192.0.2.10"
  - name: "backup"
    url: "http://203.0.113.20/healthz"
    ip: "203.0.113.20"
    priority: 10
  - name: "maintenance"
    url: "http://203.0.113.99/healthz"
    ip: "203.0.113.99"
    priority: 100
```
