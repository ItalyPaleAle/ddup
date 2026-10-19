<div align="center">
  <img src="icon.png" alt="ddup icon" width="96" />
</div>

# ddup - Dynamic DNS Health Checker

**[Read the docs](https://ddup.italypaleale.me)**

ddup keeps your DNS records pointed at the servers that are up. It checks each server over HTTP on a schedule, and updates the A and AAAA records of a domain so they list only the healthy ones.

Use it to set up [round-robin DNS](https://en.wikipedia.org/wiki/Round-robin_DNS) for load balancing and failover, for internal or public apps, without running a load balancer.

ddup isn't a DNS server. It changes the records in the DNS service that already hosts your zone, using that service's API.

![Screenshot of the ddup dashboard, showing the status of domains and their health](screenshot.webp)

## Features

- Health checks multiple apps over HTTP or HTTPS
- Round-robin across all healthy endpoints, or priorities that keep backup servers out of DNS until the main ones fail
- A records for IPv4 addresses, and AAAA records for IPv6
- A dashboard and an API with the health of every domain and endpoint

## Supported DNS providers

- [AWS Route 53](https://ddup.italypaleale.me/providers/aws-route53)
- [Azure DNS](https://ddup.italypaleale.me/providers/azure-dns)
- [Cloudflare](https://ddup.italypaleale.me/providers/cloudflare)
- [Google Cloud DNS](https://ddup.italypaleale.me/providers/google-cloud-dns)
- [OVHcloud](https://ddup.italypaleale.me/providers/ovh)

## Docs

The documentation is at [`https://ddup.italypaleale.me`](https://ddup.italypaleale.me).

- [Quickstart](https://ddup.italypaleale.me/docs/quickstart)
- [Installation](https://ddup.italypaleale.me/docs/installation)
- [Configuration](https://ddup.italypaleale.me/docs/configuration)
- [Health checks](https://ddup.italypaleale.me/docs/health-checks)
- [Failover with priorities](https://ddup.italypaleale.me/docs/failover-priorities)
- [Dynamic DNS](https://ddup.italypaleale.me/docs/dynamic-dns)
- [Dashboard and API](https://ddup.italypaleale.me/docs/dashboard)
- [Logs and metrics](https://ddup.italypaleale.me/docs/observability)
- [DNS providers](https://ddup.italypaleale.me/providers)

## Development

To build, run and test ddup from source, see [DEVELOPMENT.md](./DEVELOPMENT.md).

## License

This project is licensed under the MIT License.
