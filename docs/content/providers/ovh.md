---
title: "OVHcloud"
weight: 50
---

ddup can manage records in a DNS zone hosted on [OVHcloud](https://www.ovhcloud.com). It needs API keys for the OVHcloud API, with permission to change the zone.

## Example

```yaml
providers:
  ovh-main:
    ovh:
      apiKey: "your-application-key"
      apiSecret: "your-application-secret"
      consumerKey: "your-consumer-key"
      zoneName: "example.com"

domains:
  - recordName: "app.example.com"
    provider: "ovh-main"
    endpoints:
      - name: "web1"
        url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - name: "web2"
        url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
```

To keep the keys out of the configuration file, read them from a dotenv file:

```yaml
providers:
  ovh-main:
    ovh:
      apiKey: !envfile ovh.env OVH_APPLICATION_KEY
      apiSecret: !envfile ovh.env OVH_APPLICATION_SECRET
      consumerKey: !envfile ovh.env OVH_CONSUMER_KEY
      zoneName: "example.com"
```

## Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `apiKey` | string | **required** | Application key |
| `apiSecret` | string | **required** | Application secret |
| `consumerKey` | string | **required** | Consumer key |
| `zoneName` | string | **required** | Name of the DNS zone, which is the domain name, such as `example.com` |
| `endpoint` | string | `eu` | OVHcloud API to use: `eu`, `ca`, `us`, or a URL, see [API endpoints](#api-endpoints) |

Each provider entry manages one zone. To manage records in several zones, add an entry for each zone.

## Creating the API keys

OVHcloud can create all three keys at once, with access limited to your zone. Open this link after replacing `example.com` with the name of your zone (it appears three times):

```text
https://api.ovh.com/createToken/index.cgi?GET=/domain/zone/example.com/*&POST=/domain/zone/example.com/*&DELETE=/domain/zone/example.com/*
```

1. Log in with your OVHcloud account.
2. Give the application a name and a description, such as `ddup`.
3. Choose the validity. When the keys expire, ddup can't update DNS anymore, so choose **Unlimited** or remember to create new keys before then.
4. Check that the rights list `GET`, `POST`, and `DELETE` on `/domain/zone/example.com/*`, with the name of your zone.
5. Select **Create keys**.

OVHcloud shows three values. Copy them to the configuration:

| Shown by OVHcloud | Option in ddup |
|-------------------|----------------|
| Application key | `apiKey` |
| Application secret | `apiSecret` |
| Consumer key | `consumerKey` |

The link above is for OVHcloud Europe. For OVHcloud Canada or US, use the same path on the host of [your region's API](#api-endpoints), for example `https://ca.api.ovh.com/createToken/?GET=/domain/zone/example.com/*&POST=/domain/zone/example.com/*&DELETE=/domain/zone/example.com/*`.

## API endpoints

Set `endpoint` to the OVHcloud API of the region where your account is:

| Value | Region | API |
|-------|--------|-----|
| `eu` (default) | OVHcloud Europe | `https://eu.api.ovh.com/1.0` |
| `ca` | OVHcloud Canada | `https://ca.api.ovh.com/1.0` |
| `us` | OVHcloud US | `https://api.us.ovhcloud.com/1.0` |

For any other API compatible with OVHcloud's, set `endpoint` to its full URL, including the version, such as `https://eu.api.ovh.com/1.0`.

```yaml
providers:
  ovh-ca:
    ovh:
      apiKey: "your-application-key"
      apiSecret: "your-application-secret"
      consumerKey: "your-consumer-key"
      zoneName: "example.ca"
      endpoint: "ca"
```

## Things to know

- `recordName` must be the zone name or a subdomain of it. For example, with `zoneName: example.com`, `app.example.com` and `example.com` work, and `app.example.net` makes the update fail.
- A and AAAA records with the domain's name that don't belong to a published endpoint are deleted, including records you created in the OVHcloud Control Panel. See [How ddup manages records](/providers#how-ddup-manages-records).
