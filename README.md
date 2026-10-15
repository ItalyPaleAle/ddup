<div align="center">
  <img src="icon.png" alt="ddup icon" width="96" />
</div>

# ddup - Dynamic DNS Health Checker

ddup periodically checks the health of your services and updates the DNS records pointing to healthy deployments.

You can use ddup to configure [round-robin DNS](https://en.wikipedia.org/wiki/Round-robin_DNS) for load balancing and failover, for internal or external apps, automatically excluding un-healthy replicas.

ddup is **not** a DNS server, instead it works with "dynamic" DNS servers. Currently, it supports these DNS providers:

- AWS Route53
- Azure DNS
- Cloudflare DNS
- Google Cloud DNS
- OVH

![Screenshot of the ddup dashboard, showing the status of domains and their health](screenshot.webp)

## Installation

### Using Docker/Podman

You can run ddup as a Docker/Podman container. Container images are available for Linux and support amd64, arm64, and armv7/armhf.

First, create a folder where you will store the configuration file `config.yaml`, for example `$HOME/.ddup`. You can then start ddup with:

```sh
# For podman, replace "docker run" with "podman run"
docker run \
  -d \
  --read-only \
  -v $HOME/.ddup:/etc/ddup:ro \
  ghcr.io/italypaleale/ddup:v0
```

> ddup follows semver for versioning. The command above uses the latest version in the 0.x branch. We do not publish a container image tagged "latest".

### Using Docker Compose

This is an example of a `docker-compose.yaml` for running ddup:

```yaml
version: "3.6"

services:
  ddup:
    image: "ghcr.io/italypaleale/ddup:v0"
    volumes:
      # Set the path on the host OS
      - "/path/to/ddup:/etc/ddup:ro"
    restart: "unless-stopped"
    read_only: true
    logging:
      driver: "json-file"
      options:
        max-file: "5"
        max-size: "20m"
```

### Start as standalone app

You can download the latest version of ddup from the [Releases](https://github.com/italypaleale/ddup/releases) page. Fetch the correct archive for your system and architecture, then extract the files and copy the `ddup` binary to `/usr/local/bin` or another folder.

Place the configuration for ddup in the `/etc/ddup` folder.

You will need to start ddup as a service using the process manager for your system.

For example, for Linux distributions based on **systemd** you can use the sample unit in [`ddup.service`](./ddup.service): copy this file to `/etc/systemd/system/ddup.service`.

Start the service and enable it at boot with:

```sh
sudo systemctl enable --now ddup
```

## Configuration

ddup requires a configuration file `config.yaml` in one of the following paths:

- The path defined in the `DDUP_CONFIG` env var
- `/etc/ddup/config.yaml`
- `$HOME/.ddup/config.yaml`
- Or in the same folder where the ddup binary is located

You can find an example of the configuration file, and a description of every option, in the [`config.sample.yaml`](/config.sample.yaml) file.

> A config file can reference other files to load secrets from, using `!file /path/to/file` or `!envfile /path/to/.env VAR_NAME`. See [go-kit's README](https://github.com/ItalyPaleAle/go-kit/blob/main/config/README.md) for more details.

## Configuration Options

### Global Settings

- `interval`: How often to perform health checks (e.g., "30s", "1m", "5m")

### Domains and Endpoints

- `domains`: Array of domains to manage
  - `recordName`: The DNS record to update (e.g., "api.example.com")
  - `provider`: Name of the DNS provider (from the [`providers` map](#providers-configuration))
  - `ttl`: Time to live for DNS records. A short value is preferred to ensure faster failover from failed deployments. The default value is 120 (seconds, equivalent to 2 minutes)
  - `healthChecks`: Configuration for health checks
    - `timeout`: Request timeout (default: "3s")
    - `attempts`: Maximum number of consecutive attempts before considering the endpoint unhealthy (default: 2)
    - `recoverAfter`: Consecutive successful checks needed before an endpoint that was removed from DNS is added back (default: 1)
    - `method`: `GET` (default) or `HEAD`
    - `expectStatus`: Status code that means healthy: an exact code (like `204`) or `2xx` for any 2xx code. Default: `2xx`. Redirects are not followed
  - `endpoints`: Array of endpoints for this domain
    - `name`: Friendly name for the endpoint, used for logging (optional)
    - `url`: HTTP URL to check for health status
    - `ip`: The IPv4 or IPv6 address to include in DNS records when healthy. IPv4 addresses create A records and IPv6 addresses create AAAA records
    - `cname`: A hostname to publish in a CNAME record when healthy, as an alternative to `ip`. Each endpoint sets exactly one of `ip` and `cname`. See [CNAME records and Cloudflare proxying](#cname-records-and-cloudflare-proxying)
    - `priority`: Optional non-negative integer for priority (default: 0). Only the healthy endpoints with the lowest priority value are published, and if none of them are healthy ddup falls back to the next value. When the preferred endpoints are healthy again (after `recoverAfter`, if set), ddup goes back to them. Endpoints with the same priority are all published together. If no endpoint is healthy, DNS is left unchanged
    - `host`: Optional hostname to include in the requests, when the request is made to an IP address or to a hostname different from the desired one
    - `providerOptions`: Optional settings that apply only with a specific DNS provider, under a key named after the provider. Using one with a domain whose provider is different is a configuration error. The only one is `cloudflare`, with `proxied` (see below)

An endpoint with `cname` is health-checked through its own `url`, like any other endpoint, so `url` is required.
Two endpoints of the same domain can't publish the same IP address or hostname.

#### CNAME records and Cloudflare proxying

By default, ddup publishes A and AAAA records that point to the `ip` of each healthy endpoint.
An endpoint can set `cname` instead, to publish a CNAME record pointing to a hostname.
This is useful for a target that is only reachable by hostname, such as a [Cloudflare Tunnel](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/) (`<tunnel-id>.cfargotunnel.com`), which can only be reached through Cloudflare's proxy.

Cloudflare can also proxy a record, so traffic goes through Cloudflare instead of directly to your server.
This is set per endpoint with `providerOptions.cloudflare.proxied` (default `false`), and works with both `ip` and `cname`.

The example below publishes the VPS endpoints as plain A records, and falls back to a proxied CNAME for a Cloudflare Tunnel only when all of them are unhealthy:

```yaml
domains:
  - recordName: "app.example.com"
    provider: "my-cloudflare"
    endpoints:
      - name: "vps-us"
        url: "https://vps-us.example.com/health"
        ip: "192.0.2.10"
      - name: "vps-eu"
        url: "https://vps-eu.example.com/health"
        ip: "198.51.100.10"
      - name: "tunnel"
        url: "https://tunnel-health.example.com/health"
        cname: "0a1b2c3d.cfargotunnel.com"
        priority: 1
        providerOptions:
          cloudflare:
            proxied: true
```

The rules, which ddup checks when it starts:

- A CNAME record can't coexist with other records at the same name, so an endpoint with `cname` must be the only one with its `priority`. Use a different `priority` from the other endpoints, as in the example
- A set of records is either all proxied or all DNS-only, so all endpoints with the same `priority` must use the same value for `proxied`. Endpoints with different priorities can differ, as in the example, because only one priority is published at a time
- Proxied records use Cloudflare's automatic TTL, so the domain's `ttl` is ignored for them

Providers declare what they support, and ddup refuses to start if the configuration asks for more:

| Provider         | `cname` | `providerOptions.cloudflare` |
| ---------------- | ------- | ---------------------------- |
| AWS Route53      | No      | No                           |
| Azure DNS        | No      | No                           |
| Cloudflare DNS   | Yes     | Yes                          |
| Google Cloud DNS | No      | No                           |
| OVH              | No      | No                           |

When ddup switches between a CNAME record and A or AAAA records, it overwrites an existing record in place, so the name never ends up with no records.

### Providers Configuration

- `providers`: Map of providers.
  - Key: provider name (e.g. `my-provider-1`)
  - Value: an object containing a provider configuration, which is one (and only one) of:
    - [`awsRoute53`](#aws-route53-provider-settings)
    - [`azure`](#azure-provider-settings)
    - [`cloudflare`](#cloudflare-provider-settings)
    - [`googleCloudDns`](#google-cloud-dns-provider-settings)
    - [`ovh`](#ovh-provider-settings)

#### AWS Route53 Provider Settings

Required settings:

- `hostedZoneId`: ID of the hosted zone (e.g. `Z0123456789ABCDEFGHIJ`)

Optional settings, to authenticate with an IAM user's access key:

- `accessKeyId`: Access key ID
- `secretAccessKey`: Secret access key

If these are not set, ddup uses the [default AWS credential chain](https://docs.aws.amazon.com/sdkref/latest/guide/standardized-credentials.html), which includes environmental variables such as `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`, shared config and credentials files (selected with `AWS_PROFILE`), IAM Roles for Service Accounts on EKS, ECS task roles, and EC2 instance profiles.

The principal needs an IAM policy that allows listing and changing record sets in the hosted zone:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "route53:ListResourceRecordSets",
        "route53:ChangeResourceRecordSets"
      ],
      "Resource": "arn:aws:route53:::hostedzone/<hosted-zone-id>"
    }
  ]
}
```

To restrict the record names ddup can change, add a condition on the `route53:ChangeResourceRecordSetsNormalizedRecordNames` key.

Example:

```yaml
providers:
  example-provider-1:
    awsRoute53:
      hostedZoneId: "Z0123456789ABCDEFGHIJ"
```

#### Azure Provider Settings

Required settings:

- `subscriptionId`: ID of the Azure subscription where the DNS Zone is deployed
- `resourceGroupName`: Name of the Resource Group containing the DNS Zone resource
- `zoneName`: Name of the DNS Zone, which corresponds to the domain name (e.g. `example.com`)

The other settings depend on the authentication method:

- The default authentication method automatically attempts a number of supported methods, including Managed Identity, Workload Identity, Azure CLI credentials (in development), etc. You can also configure it with environmental variables including `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_SECRET` ([full reference](https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azidentity#readme-environment-variables))
- To use a service principal (with client ID and client secret), set these options:
  - `clientId`: Client ID
  - `clientSecret`: Client Secret
  - `tenantId`: Tenant ID
- To use a user-assigned managed identity, set:
  - `managedIdentityClientId`: Client ID of the user-assigned managed identity

Regardless of the authentication method, ensure that the principal (user, service principal, or managed identity) has the **DNS Zone Contributor** role assigned to the DNS zone.

If using a custom RBAC role instead, ensure it includes permissions for both IPv4 and IPv6 DNS records:

```text
Microsoft.Network/dnsZones/A/read
Microsoft.Network/dnsZones/A/write
Microsoft.Network/dnsZones/A/delete
Microsoft.Network/dnsZones/AAAA/read
Microsoft.Network/dnsZones/AAAA/write
Microsoft.Network/dnsZones/AAAA/delete
```

Using the Azure CLI, the built-in **DNS Zone Contributor** role can be assigned with:

```sh
az role assignment create \
  --assignee <client-id> \
  --role "DNS Zone Contributor" \
  --scope "/subscriptions/<subscription-id>/resourceGroups/<rg-name>/providers/Microsoft.Network/dnsZones/<zone-name>"
```

Example:

```yaml
providers:
  example-provider-1:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "my-dns-rg"
      zoneName: "example.com"
```

#### Cloudflare Provider Settings

Required settings:

- `zoneId`: Cloudflare Zone ID for your domain
- `apiToken`: Cloudflare API token with permission to edit DNS records in that zone (see below)

To get the credentials:

- API Token: Go to Cloudflare dashboard → My Profile → API Tokens → Create Token → Create Custom Token
  - Permissions: **Zone → DNS → Edit** (this is the only permission ddup needs)
  - Zone Resources: **Include → Specific zone →** your domain
  - It must be an API token (sent as a Bearer token), not the Global API Key
- Zone ID: Found on the domain's Overview page, in the right sidebar

Cloudflare cannot scope a token to a single record, so the token can edit every DNS record in the zone. Use a token dedicated to ddup rather than sharing one with other tools.

Records are created DNS-only (not proxied by Cloudflare), unless the endpoint sets [`providerOptions.cloudflare.proxied`](#cname-records-and-cloudflare-proxying). The same token permission is enough for CNAME and proxied records.

Example:

```yaml
providers:
  example-provider-1:
    cloudflare:
      apiToken: "your-cloudflare-api-token"
      zoneId: "your-zone-id"
```

#### Google Cloud DNS Provider Settings

Required settings:

- `projectId`: ID of the Google Cloud project containing the managed zone
- `managedZone`: Name of the managed zone resource (e.g. `my-zone`), which is not the same as its DNS name

Optional settings:

- `credentialsFile`: Path to a service account key file (JSON)

If `credentialsFile` is not set, ddup uses [Application Default Credentials](https://cloud.google.com/docs/authentication/application-default-credentials), which include the `GOOGLE_APPLICATION_CREDENTIALS` environment variable, the attached service account when running on Google Cloud (including GKE Workload Identity), Workload Identity Federation, and credentials from `gcloud auth application-default login` (in development).

The principal needs the **DNS Administrator** role (`roles/dns.admin`), which can be granted on the managed zone alone. If using a custom role instead, it needs these permissions:

```text
dns.changes.create
dns.resourceRecordSets.list
dns.resourceRecordSets.create
dns.resourceRecordSets.update
dns.resourceRecordSets.delete
```

Example:

```yaml
providers:
  example-provider-1:
    googleCloudDns:
      projectId: "my-project"
      managedZone: "my-zone"
```

#### OVH Provider Settings

Required settings:

- `apiKey`: API key
- `apiSecret`: API secret
- `consumerKey`: Consumer key
- `zoneName`: Name of the zone (e.g. `example.com`)

Optional settings:

- `endpoint`: OVH API endpoint, which is one of:
  - `"eu"` (default value if omitted)
  - `"ca"`
  - `"us"`
  - A custom URL

To get the required credentials, navigate to this URL, replacing `{zoneName}` with the name of your zone (e.g. `example.com`):

```text
https://api.ovh.com/createToken/index.cgi?GET=/domain/zone/{zoneName}/*&POST=/domain/zone/{zoneName}/*&DELETE=/domain/zone/{zoneName}/*
```

Example:

```yaml
providers:
  ovh-eu-example:
    ovh:
      apiKey: "your-ovh-api-key"
      apiSecret: "your-ovh-api-secret" 
      consumerKey: "your-ovh-consumer-key"
      zoneName: "example.com"
      endpoint: "eu"
```

### Server Settings

- `enabled`: Enable the server (disabled by default)
- `bind`: Address to bind to (defaults to `127.0.0.1`)
- `port`: Port to listen on (defaults to `7401`)

The server has no authentication, so keep it on a trusted network. It exposes:

- `GET /api/status` and `GET /api/status/{recordname}`: current status of the domains. Each endpoint has its `ip` (the IP address, or the hostname for an endpoint with `cname`), and the optional `type` of its DNS record (`A`, `AAAA` or `CNAME`) and `proxied` (only when it's true)
- `POST /api/check`: runs health checks for all domains right away (the dashboard's "Check now" button), and returns the updated status. The request must include the header `X-Requested-By: ddup-dashboard`, which keeps other websites from triggering it through a visitor's browser. If a check finished less than 5 seconds ago, no new one is run. A forced check counts towards `attempts` and `recoverAfter` like a scheduled one, and the next scheduled check is then a full `interval` later
- `GET /healthz`: returns 204 when the server is up

### Logging Settings

- `log`: Logging options
  - `level`: Controls log level and verbosity. Supported values: `debug`, `info` (default), `warn`, `error`.
  - `json`: If true, emits logs formatted as JSON, otherwise uses a text-based structured log format. Defaults to false if a TTY is attached (e.g. when running the binary directly in the terminal or in development); true otherwise.
