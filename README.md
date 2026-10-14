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
- `ipLookupURLs`: List of the services used to look up the public IP address of endpoints that use `ipLookupFamily` and don't have a list of their own. ddup has no built-in services. See [Dynamic DNS](#dynamic-dns)

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
    - `url`: HTTP URL to check for health status. Optional for endpoints that use `ipLookupFamily`, which are then not health-checked
    - `ip`: The IPv4 or IPv6 address to include in DNS records when healthy. IPv4 addresses create A records and IPv6 addresses create AAAA records. Exactly one of `ip` and `ipLookupFamily` is required
    - `ipLookupFamily`: `4` or `6`. Instead of a fixed `ip`, ddup looks up the public IP address of the network it runs in and publishes it: `4` creates an A record and `6` an AAAA record. See [Dynamic DNS](#dynamic-dns). Exactly one of `ip` and `ipLookupFamily` is required
    - `ipLookupURLs`: Optional, only with `ipLookupFamily`. One or more services for this endpoint, which **replace** the top-level `ipLookupURLs` for it. See [Dynamic DNS](#dynamic-dns)
    - `priority`: Optional non-negative integer for priority (default: 0). Only the healthy endpoints with the lowest priority value are published, and if none of them are healthy ddup falls back to the next value. When the preferred endpoints are healthy again (after `recoverAfter`, if set), ddup goes back to them. Endpoints with the same priority are all published together. If no endpoint is healthy, DNS is left unchanged
    - `host`: Optional hostname to include in the requests, when the request is made to an IP address or to a hostname different from the desired one

### Dynamic DNS

An endpoint can follow the public IP address of the network ddup runs in (dynamic DNS), instead of having a fixed `ip`. Set `ipLookupFamily` to `4` or `6`: `4` creates an A record and `6` an AAAA record.

**ddup has no lookup service built in, and contacts none unless you configure it.** You list the services in `ipLookupURLs`. Whichever service you pick sees the public IP address of your network, which is what these services are for, plus the fact that a ddup client is asking. Use one you trust, or one you host yourself.

On every check ddup asks the services for the current public address and publishes it, and updates the record when it changes.

#### Services for all the endpoints

List the services at the top level of the configuration. Every dynamic endpoint that doesn't have a list of its own uses the entries with its family. This example uses Akamai and ipify, which you can copy:

```yaml
ipLookupURLs:
  - url: "https://whatismyip.akamai.com"
    family: 4
  - url: "https://api.ipify.org"
    family: 4
  - url: "https://api6.ipify.org"
    family: 6

domains:
  - recordName: "home.example.com"
    provider: "example-provider-1"
    ttl: 300
    endpoints:
      # Asks the two services with family 4, at the same time
      - name: "home"
        ipLookupFamily: 4
      # Asks the service with family 6
      - name: "home-v6"
        ipLookupFamily: 6
```

About these services: Akamai and `api.ipify.org` were checked from an IPv4-only network, where they answered with a bare address and agreed with each other. `api6.ipify.org` is IPv6-only and should answer with a bare IPv6 address, but **I could not test it** (no IPv6 route here). I found no documentation of an Akamai host that answers for IPv6 only, so the example has one service for IPv6. If you need a second IPv6 service, add it.

#### Services for one endpoint

An endpoint with `ipLookupFamily` can have its own `ipLookupURLs`, with one or more entries. This is how you give one record several lookup URLs:

```yaml
domains:
  - recordName: "office.example.com"
    provider: "example-provider-1"
    ttl: 300
    endpoints:
      - name: "office"
        ipLookupFamily: 4
        # These two services are asked at the same time, and the first valid answer is used
        # This list replaces the top-level ipLookupURLs for this endpoint
        ipLookupURLs:
          - url: "https://ip.example.com/v4"
          - url: "https://ip2.example.com/json"
            pattern: '"ip":\s*"([^"]+)"'
```

Different endpoints can have different lists, also in the same domain.

#### Rules

- The list of an endpoint **replaces** the top-level list for that endpoint. They are never merged: the top-level services are not contacted for it.
- An endpoint's list can have one entry or several. All the services that apply to an endpoint (all the entries of its own list, or all the top-level entries with its family) are asked at the same time. With one entry, only that service is asked.
- At the top level, `family` (`4` or `6`) is required in every entry, because one list serves both families.
- In an endpoint's list, `family` is optional, because `ipLookupFamily` already says it. If it is set, it must be equal to `ipLookupFamily`: ddup refuses a configuration that mixes IPv4 and IPv6 services for one endpoint.
- An endpoint with `ipLookupFamily` needs at least one service, from its own list or from a top-level entry with its family. Otherwise the configuration is rejected, and the message says what to add. An empty list (`ipLookupURLs: []`) on an endpoint is rejected as well.
- `ipLookupURLs` on an endpoint with a fixed `ip` is rejected, because it would never be used.
- `url` must use `https`. An answer that can't be authenticated could be spoofed, and ddup would publish it. The URL can contain a token: ddup leaves the query string and credentials out of logs, errors and names.
- `pattern` is optional. By default the response body must contain only the address, as plain text. For a service that returns more (JSON, for example), `pattern` is a regular expression with exactly one capture group, which is the address.

#### How the lookup works

- With no `url` the endpoint isn't health-checked: it's healthy as long as the lookup works. With a `url`, the endpoint is also health-checked as usual, and the address that was looked up is the one published.
- Requests are made over the requested IP version only, so a service sees (and returns) an address of that family.
- The first valid answer is used. The other services finish in the background, and ddup logs a warning if they return a different address. A service that fails or returns something that is not valid is skipped; if all of them fail, the lookup fails, the endpoint is unhealthy, and DNS is left as it is until an address is found (like any other unhealthy endpoint).
- An answer is rejected unless it is just an IP address of the right family: an IPv4 address for `ipLookupFamily: 4` and an IPv6 address for `6`. Loopback, private, link-local, multicast and unspecified addresses are rejected too. Responses are limited to 4 KB, redirects are not followed, and each service has the health check `timeout` to answer.
- Lookups connect directly and ignore proxy environment variables, and every lookup opens a new connection, so the answer always reflects the current network path.
- If two endpoints end up with the same address, it is published once.
- ddup replaces every `A`/`AAAA` record at the name that isn't the looked-up address, so don't point it at a name that has other records.

Any service that answers with the caller's address works, including a few lines on a VPS you own that echo the client address.

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

ddup does not set the `proxied` flag, so the records it creates are DNS-only (not proxied by Cloudflare).

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

- `GET /api/status` and `GET /api/status/{recordname}`: current status of the domains
- `POST /api/check`: runs health checks for all domains right away (the dashboard's "Check now" button), and returns the updated status. The request must include the header `X-Requested-By: ddup-dashboard`, which keeps other websites from triggering it through a visitor's browser. If a check finished less than 5 seconds ago, no new one is run. A forced check counts towards `attempts` and `recoverAfter` like a scheduled one, and the next scheduled check is then a full `interval` later
- `GET /healthz`: returns 204 when the server is up

### Logging Settings

- `log`: Logging options
  - `level`: Controls log level and verbosity. Supported values: `debug`, `info` (default), `warn`, `error`.
  - `json`: If true, emits logs formatted as JSON, otherwise uses a text-based structured log format. Defaults to false if a TTY is attached (e.g. when running the binary directly in the terminal or in development); true otherwise.
