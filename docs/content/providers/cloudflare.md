---
title: "Cloudflare"
weight: 30
---

ddup can manage records in a DNS zone hosted on [Cloudflare](https://www.cloudflare.com), on any plan. It needs an API token with permission to edit the zone's DNS records, and the ID of the zone.

## Example

```yaml
providers:
  cloudflare-main:
    cloudflare:
      apiToken: "your-api-token"
      zoneId: "023e105f4ecef8ad9ca31a8372d0c353"

domains:
  - recordName: "app.example.com"
    provider: "cloudflare-main"
    ttl: 60
    endpoints:
      - name: "web1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "web2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
```

To keep the token out of the configuration file, read it from another file:

```yaml
providers:
  cloudflare-main:
    cloudflare:
      apiToken: !file /run/secrets/cloudflare-token
      zoneId: "023e105f4ecef8ad9ca31a8372d0c353"
```

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `apiToken` | string | **required** | API token with permission to edit the DNS records of the zone |
| `zoneId` | string | **required** | ID of the zone |

Each provider entry manages one zone. To manage records in several zones, add an entry for each zone: they can use the same token, if it has access to all of them.

## Getting the zone ID

1. Log in to the [Cloudflare dashboard](https://dash.cloudflare.com), and select your domain.
2. On the domain's **Overview** page, find **Zone ID** in the **API** section of the right sidebar, and copy it.

## Creating an API token

1. In the Cloudflare dashboard, open **My Profile** > **API Tokens**, and select **Create Token**.
2. Next to **Create Custom Token**, select **Get started**.
3. Give the token a name, such as `ddup`.
4. Under **Permissions**, choose **Zone** > **DNS** > **Edit**. This is the only permission ddup needs.
5. Under **Zone Resources**, choose **Include** > **Specific zone**, and select your domain.
6. Optionally, under **Client IP Address Filtering**, allow only the IP address of the machine that runs ddup.
7. Select **Continue to summary**, then **Create Token**, and copy the token. Cloudflare shows it only once.

Use an API token, not the Global API Key: ddup sends the token as a Bearer token, which doesn't work with the Global API Key.

> [!WARNING]
> Cloudflare can't limit a token to a single record: the token can edit every DNS record in the zone. Create a separate token for ddup, and don't share it with other tools.

To test the token and the zone ID, list the records of the domain:

```sh
curl -s "https://api.cloudflare.com/client/v4/zones/<zone-id>/dns_records?name=app.example.com" \
  -H "Authorization: Bearer <api-token>"
```

The response has `"success": true` if both are right.

## CDN Proxying and Cloudflare Tunnel

Records in Cloudflare can be configured in one of two modes:

- DNS only ("gray cloud"): the default option. Cloudflare responds to DNS queries with the IP of your server.
- Proxying ("orange cloud"): in this mode, traffic gets proxied through Cloudflare and visitors never hit your server directly.

Proxying can be enabled per endpoint with `providerOptions.cloudflare.proxied` (default `false`), and works with both `ip` and `cname`.

A [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) can only be reached through the proxy, so it needs both `cname` and `proxied`:

```yaml
domains:
  - recordName: "app.example.com"
    provider: "my-cloudflare"
    endpoints:
      - name: "vps-us"
        url: "https://vps-us.example.com/health"
        ip: "192.0.2.10"
      - name: "tunnel"
        url: "https://tunnel-health.example.com/health"
        cname: "0a1b2c3d.cfargotunnel.com"
        priority: 1
        providerOptions:
          cloudflare:
            proxied: true
```

A set of records is either all proxied or all DNS-only, so all endpoints with the same `priority` must use the same value for `proxied`. Endpoints with different priorities can differ, as in the example, because only one priority is published at a time

> Proxied records use Cloudflare's automatic TTL, so the domain's `ttl` is ignored for them

## Things to know

- Cloudflare doesn't accept a `ttl` lower than 60 seconds, or 30 seconds on Enterprise plans.
- A and AAAA records with the domain's name that don't belong to a published endpoint are deleted, including records you created in the dashboard. See [How ddup manages records](/providers#how-ddup-manages-records).
