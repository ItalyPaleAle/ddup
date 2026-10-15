---
title: "ddup"
nav_title: "Introduction"
weight: 10
---

ddup keeps your DNS records pointed at the servers that are up. It checks the health of each server over HTTP on a schedule, and updates the A and AAAA records of a domain so they list only the healthy ones.

Use it to set up [round-robin DNS](https://en.wikipedia.org/wiki/Round-robin_DNS) for load balancing and failover, for internal or public apps, without running a load balancer.

{{< figure src="img/screenshot.webp" alt="The ddup dashboard, showing the health of each domain and its endpoints" >}}

## How it works

1. You list the domains that ddup manages. For each domain, you list its endpoints: the IP address of a server, and a URL that ddup requests to check its health.
2. Every 30 seconds (you can change the interval), ddup sends a request to each health check URL.
3. When the set of healthy endpoints changes, ddup updates the domain's records through your DNS provider's API: it adds the IPs of endpoints that are healthy, and removes the IPs of endpoints that are failing.
4. Clients that resolve the domain get only the IPs of healthy endpoints.

ddup is not a DNS server. It works with the DNS service that already hosts your zone, using its API.

## Features

- **HTTP and HTTPS health checks**, with a configurable method, expected status code, timeout, and number of attempts
- **Round-robin or failover**: publish all healthy endpoints at once, or set [priorities](/docs/failover-priorities) to keep backup servers out of DNS until they're needed
- **IPv4 and IPv6**, as A and AAAA records
- **Protection against flapping**: an endpoint is removed only after several failed checks in a row, and you can require it to pass several checks before it's added back
- **Dashboard and API** that show the health of every domain and endpoint
- **Metrics** for OpenTelemetry or Prometheus
- **Single binary**, also available as a container image for amd64, arm64, and armv7

## Supported DNS providers

- [AWS Route 53](/providers/aws-route53)
- [Azure DNS](/providers/azure-dns)
- [Cloudflare](/providers/cloudflare)
- [Google Cloud DNS](/providers/google-cloud-dns)
- [OVHcloud](/providers/ovh)

## Before you start: DNS failover isn't instant

Failover with DNS is simple and works with any client, but it has limits that are good to know up front:

- Resolvers and clients cache DNS records for as long as the TTL says. After ddup removes a failed endpoint, clients with a cached copy of the record keep using its IP until their cache expires. Use a short TTL: ddup sets 120 seconds by default.
- When a name has multiple IPs, clients choose which one to connect to. Some pick one and stick with it, and others try the next IP when a connection fails. How evenly round-robin spreads the load depends on your clients.
- ddup checks endpoints from the machine it runs on. If that machine can reach an endpoint but your users can't (or the other way round), the health checks won't match what your users see.

## Start here

- [Quickstart](/docs/quickstart)
- [Configuration](/docs/configuration)
- [Health checks](/docs/health-checks)
- [DNS providers](/providers)
