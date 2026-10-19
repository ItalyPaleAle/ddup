---
title: "DNS providers"
nav_title: "Overview"
weight: 30
---

ddup updates DNS records through the API of the service that hosts your DNS zone. It supports these providers:

| Provider | Key in the configuration |
|----------|--------------------------|
| [AWS Route 53](/providers/aws-route53) | `awsRoute53` |
| [Azure DNS](/providers/azure-dns) | `azure` |
| [Cloudflare](/providers/cloudflare) | `cloudflare` |
| [Google Cloud DNS](/providers/google-cloud-dns) | `googleCloudDns` |
| [OVHcloud](/providers/ovh) | `ovh` |

## Configuring a provider

Providers go in the `providers` map of the configuration file. Each entry has a name of your choice, and contains the settings for exactly one provider. Domains use a provider by its name:

```yaml
providers:
  # A name of your choice
  cloudflare-main:
    cloudflare:
      apiToken: !file /run/secrets/cloudflare-token
      zoneId: "023e105f4ecef8ad9ca31a8372d0c353"

domains:
  - recordName: "app.example.com"
    # The name of an entry in "providers"
    provider: "cloudflare-main"
    endpoints:
      - url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
```

Each entry manages one DNS zone. To manage records in more than one zone, add an entry for each zone, even if they use the same provider and credentials. You can also mix providers:

```yaml
providers:
  example-com:
    cloudflare:
      apiToken: !file /run/secrets/cloudflare-token
      zoneId: "023e105f4ecef8ad9ca31a8372d0c353"
  example-net:
    cloudflare:
      apiToken: !file /run/secrets/cloudflare-token
      zoneId: "372e67954025e0ba6aaa6d586b9e0b59"
  example-org:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "dns"
      zoneName: "example.org"

domains:
  - recordName: "app.example.com"
    provider: "example-com"
    endpoints: [...]
  - recordName: "app.example.net"
    provider: "example-net"
    endpoints: [...]
  - recordName: "example.org"
    provider: "example-org"
    endpoints: [...]
```

Use `!file` or `!envfile` to keep credentials out of the configuration file. See [Secrets](/docs/configuration#secrets).

## How ddup manages records

You don't need to create the records in advance: ddup creates them on its first check. ddup owns all the A and AAAA records with the name in `recordName`:

- It creates a record for each endpoint it publishes, and deletes the other A and AAAA records with that name, **including records you created by hand**.
- IPv4 addresses are published as A records, and IPv6 addresses as AAAA records. If a domain has endpoints of both types, ddup manages both.
- Other types of records with the same name, such as TXT or MX, aren't changed.
- If no endpoint is healthy, ddup leaves the records as they are.

ddup writes to DNS when it starts, and when the set of endpoints to publish changes.  If you change the records by hand while ddup is running, ddup doesn't notice until the next time the set of endpoints changes, or until it restarts.

### Record names

- `recordName` is the full name, such as `app.example.com`, and must be in the provider's zone. For the zone `example.com`, use names like `app.example.com` or `api.eu.example.com`.
- To manage the records at the apex of the zone, use the zone name itself, such as `example.com`.
- The name can't have a CNAME record: DNS doesn't allow a CNAME record next to other records with the same name.
