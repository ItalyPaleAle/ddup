<div align="center">
  <img src="icon.png" alt="ddup icon" width="96" />
</div>

# ddup - Dynamic DNS Health Checker

**[📚 Read the docs](https://ddup.italypaleale.me)**

ddup keeps your DNS records pointed at the servers that are up. It checks the health of each server over HTTP on a schedule, and updates the A and AAAA records of a domain so they list only the healthy ones.

Use it to set up [round-robin DNS](https://en.wikipedia.org/wiki/Round-robin_DNS) for load balancing and failover, for internal or public apps, without running a load balancer.

ddup is **not** a DNS server: it updates the records in the DNS service that already hosts your zone, using its API.

![Screenshot of the ddup dashboard, showing the status of domains and their health](screenshot.webp)

## ✨ Features

- **HTTP and HTTPS health checks**, with a configurable method, expected status code, timeout, and number of attempts
- **Round-robin or failover**: publish all healthy endpoints at once, or set priorities to keep backup servers out of DNS until they're needed
- **IPv4 and IPv6**, as A and AAAA records
- **Protection against flapping**: an endpoint is removed only after several failed checks in a row, and you can require it to pass several checks before it's added back
- **Dashboard and API** that show the health of every domain and endpoint
- **Metrics** for OpenTelemetry or Prometheus
- **Single binary**, also available as a container image for amd64, arm64, and armv7

## 🌐 Supported DNS providers

- [AWS Route 53](https://ddup.italypaleale.me/providers/aws-route53)
- [Azure DNS](https://ddup.italypaleale.me/providers/azure-dns)
- [Cloudflare](https://ddup.italypaleale.me/providers/cloudflare)
- [Google Cloud DNS](https://ddup.italypaleale.me/providers/google-cloud-dns)
- [OVHcloud](https://ddup.italypaleale.me/providers/ovh)

## 📘 Docs

The documentation is available at [`https://ddup.italypaleale.me`](https://ddup.italypaleale.me).

- [Quickstart](https://ddup.italypaleale.me/docs/quickstart)
- [Installation](https://ddup.italypaleale.me/docs/installation)
- [Configuration](https://ddup.italypaleale.me/docs/configuration)
- [Health checks](https://ddup.italypaleale.me/docs/health-checks)
- [Failover with priorities](https://ddup.italypaleale.me/docs/failover-priorities)
- [Dashboard and API](https://ddup.italypaleale.me/docs/dashboard)
- [Logs and metrics](https://ddup.italypaleale.me/docs/observability)
- [DNS providers](https://ddup.italypaleale.me/providers)

## 🛠️ Development

To build, run and test ddup from source, see [DEVELOPMENT.md](./DEVELOPMENT.md).

## License

This project is licensed under the MIT License.
