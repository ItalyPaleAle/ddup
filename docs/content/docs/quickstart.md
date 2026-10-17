---
title: "Quickstart"
weight: 10
---

You'll set up ddup to manage one domain, `app.example.com`, served by two web servers. When one of the servers stops responding, ddup removes its IP from DNS, and adds it back when the server recovers.

## Prerequisites

- A DNS zone hosted by one of the [supported DNS providers](/providers), and API credentials for it. Each provider's page explains how to create them.
- Two or more servers with a URL that ddup can request to check their health. It must return a 2xx status code when the server is healthy (you can [change the status code](/docs/health-checks#expected-status-code)).
- Docker or Podman. To run ddup without containers, see [Installation](/docs/installation).

## Step 1: Create the configuration file

Create a folder for the configuration:

```sh
mkdir -p $HOME/.ddup
```

In that folder, create a file named `config.yaml`, replacing the IPs and URLs with the ones of your servers:

```yaml
# How often to check the endpoints
interval: 30s

domains:
  - recordName: "app.example.com"
    # Name of the provider, defined in the "providers" section below
    provider: "my-dns"
    endpoints:
      - name: "web1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "web2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"

# Enable the dashboard
server:
  enabled: true
  # Needed to reach the dashboard from outside the container
  bind: "0.0.0.0"
```

Then add a `providers` section at the end of the file, with the settings for your DNS provider:

{{< tabs >}}

{{< tab title="AWS Route 53" >}}
```yaml
providers:
  my-dns:
    awsRoute53:
      hostedZoneId: "Z0123456789ABCDEFGHIJ"
      accessKeyId: "your-access-key-id"
      secretAccessKey: "your-secret-access-key"
```

See [AWS Route 53](/providers/aws-route53) to find the hosted zone ID and create the access key, or to use an IAM role instead.
{{< /tab >}}

{{< tab title="Azure DNS" >}}
```yaml
providers:
  my-dns:
    azure:
      subscriptionId: "00000000-0000-0000-0000-000000000000"
      resourceGroupName: "my-dns-rg"
      zoneName: "example.com"
      tenantId: "00000000-0000-0000-0000-000000000000"
      clientId: "00000000-0000-0000-0000-000000000000"
      clientSecret: "your-client-secret"
```

See [Azure DNS](/providers/azure-dns) to create the service principal and allow it to change the zone, or to use a managed identity instead.
{{< /tab >}}

{{< tab title="Cloudflare" >}}
```yaml
providers:
  my-dns:
    cloudflare:
      apiToken: "your-api-token"
      zoneId: "your-zone-id"
```

See [Cloudflare](/providers/cloudflare) to create the API token and find the zone ID.
{{< /tab >}}

{{< tab title="Google Cloud DNS" >}}
```yaml
providers:
  my-dns:
    googleCloudDns:
      projectId: "my-project"
      managedZone: "example-com"
      # The key file, in the same folder as config.yaml
      credentialsFile: "/etc/ddup/ddup-key.json"
```

See [Google Cloud DNS](/providers/google-cloud-dns) to find the managed zone name and create the service account key.
{{< /tab >}}

{{< tab title="OVHcloud" >}}
```yaml
providers:
  my-dns:
    ovh:
      apiKey: "your-application-key"
      apiSecret: "your-application-secret"
      consumerKey: "your-consumer-key"
      zoneName: "example.com"
```

See [OVHcloud](/providers/ovh) to create the API keys.
{{< /tab >}}

{{< /tabs >}}

> [!TIP]
> You can keep secrets such as API tokens out of the configuration file and read them from other files instead. See [Secrets](/docs/configuration#secrets).

## Step 2: Start ddup

```sh
# For Podman, replace "docker" with "podman"
docker run -d \
  --name ddup \
  --read-only \
  --restart unless-stopped \
  -v $HOME/.ddup:/etc/ddup:ro \
  -p 127.0.0.1:7401:7401 \
  ghcr.io/italypaleale/ddup:v0
```

The `-p` flag makes the dashboard available on port 7401 of your machine, only from the machine itself. The dashboard has no authentication, so don't expose it on a network you don't trust.

## Step 3: Check that it works

ddup checks all endpoints as soon as it starts. Look at its logs:

```sh
docker logs -f ddup
```

For each domain, you should see a line with the message `Updated DNS records`, which lists the IPs that ddup published. If the records already matched, the message is `DNS records already up to date` instead. If something is wrong, such as invalid credentials, the logs show the error.

Open `http://localhost:7401` in a browser to see the [dashboard](/docs/dashboard), and look up the domain to see the records:

```sh
dig +short app.example.com
```

## Step 4: Try a failover

Stop the web server on one of the machines. By default, ddup removes an endpoint after it fails 2 checks in a row, so with a 30-second interval its IP disappears from DNS within about a minute. The logs show `Endpoint health check failed` for each failed check, then `Updated DNS records` with the remaining IPs.

Start the web server again. On the next check, ddup adds its IP back.

## Next steps

- [Installation](/docs/installation): run ddup with Docker Compose, or as a service without containers
- [Configuration](/docs/configuration): every configuration option
- [Health checks](/docs/health-checks): control how endpoints are checked, and how quickly they're removed and added back
- [Failover with priorities](/docs/failover-priorities): keep backup servers out of DNS until the main ones fail
