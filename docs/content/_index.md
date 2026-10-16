---
title: "ddup"
nav_title: "Introduction"
weight: 10
---

ddup keeps your DNS records pointed at the servers that are up. It checks each server over HTTP on a schedule, and updates the A and AAAA records of a domain so they list only the healthy ones.

Use it to set up [round-robin DNS](https://en.wikipedia.org/wiki/Round-robin_DNS) for load balancing and failover, for internal or public apps, without running a load balancer.

{{< figure src="img/screenshot.webp" alt="The ddup dashboard, showing the health of each domain and its endpoints" >}}

## How it works

1. You list the domains that ddup manages. For each domain, you list its endpoints: the IP address of a server, and a URL that ddup requests to check its health.
2. Every 30 seconds by default, ddup sends a request to each health check URL.
3. When the set of healthy endpoints changes, ddup updates the domain's records through your DNS provider's API. It adds the IPs of endpoints that pass their checks, and removes the IPs of endpoints that fail them.
4. Clients that resolve the domain get only the IPs of healthy endpoints.

**ddup is not a DNS server**. It changes the records in the DNS service that already hosts your zone, using that service's API.

## Features

- Health checks over HTTP or HTTPS, with your choice of method, expected status code, timeout, and number of attempts
- Round-robin across all healthy endpoints, or [priorities](/docs/failover-priorities) that keep backup servers out of DNS until the main ones fail
- A records for IPv4 addresses, and AAAA records for IPv6
- A dashboard and an API with the health of every domain and endpoint

## Supported DNS providers

- [AWS Route 53](/providers/aws-route53)
- [Azure DNS](/providers/azure-dns)
- [Cloudflare](/providers/cloudflare)
- [Google Cloud DNS](/providers/google-cloud-dns)
- [OVHcloud](/providers/ovh)

## Limits of DNS failover

DNS failover works with any client and doesn't need a load balancer, but it has limits:

- Resolvers and clients cache records for as long as their TTL. After ddup removes a failed endpoint, clients with a cached copy keep using its IP until the cache expires. Use a short TTL: ddup sets 120 seconds by default.
- When a name has several IPs, the client picks which one to connect to. Some clients keep using the IP they picked first, and others try the next one when a connection fails, so how evenly the load spreads depends on your clients.
- ddup checks endpoints from the machine it runs on. If that machine can reach an endpoint but your users can't, or the other way round, the checks won't match what your users see.

## Start here

- [Quickstart](/docs/quickstart)
- [Configuration](/docs/configuration)
- [Health checks](/docs/health-checks)
- [DNS providers](/providers)
