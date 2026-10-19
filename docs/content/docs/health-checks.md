---
title: "Health checks"
weight: 40
---

ddup decides which IPs to publish by sending an HTTP request to each endpoint's health check URL and looking at the response.

## How endpoints are checked

ddup checks all endpoints when it starts, and then every `interval` (30 seconds by default). All endpoints of a domain are checked at the same time.

For each endpoint, ddup sends a request to its `url`:

- Method is `GET` (default) or `HEAD`
- `User-Agent` header is `ddup/1.0`
- Redirects are not followed: a 3xx response is a failure, unless `expectStatus` is set to that code
- For HTTPS URLs, the server's certificate must be valid for the host name; checks of servers with self-signed or expired certificates fail

The endpoint passes the check if it responds within `timeout` (3 seconds by default) with the [expected status code](#expected-status-code). Anything else is a failure: an error connecting, a timeout, a TLS error, or a different status code.

The health check URL doesn't need to use the same port or protocol as your app. For example, your app can serve HTTPS on port 443 and a health check over HTTP on port 8080.

## When endpoints are removed and added back

At startup, ddup publishes the endpoints that pass the first check, and updates DNS to match.

After that, a published endpoint stays in DNS until it fails `attempts` checks in a row (2 by default). If it passes a check before then, the count starts over, so a single slow response doesn't remove a server.

Once an endpoint has failed `attempts` checks in a row, it must pass `recoverAfter` checks in a row before ddup publishes it again (1 by default). Increase `recoverAfter` to keep a server that keeps going up and down out of DNS until it's stable.

If none of a domain's endpoints are healthy, ddup leaves DNS as it is. Removing every record would make the domain impossible to resolve, and the old records may still work for some clients.

If you also set [priorities](/docs/failover-priorities), ddup publishes only the healthy endpoints with the lowest priority value.

ddup changes DNS only when the set of endpoints to publish changes. If an update fails, for example because the provider's API is unavailable, ddup tries again on the next check.

## How long failover takes

After a server goes down, clients can keep getting its IP for up to `interval` times `attempts`, plus `ttl`. With the default settings, that's 30 seconds times 2, plus 120 seconds, for a total of 3 minutes. If the checks fail because of a timeout, add up to `timeout` for each check.

To fail over sooner:

- Lower `interval`, so failures are detected sooner. Every check sends a request to each endpoint, so a lower interval means more requests.
- Lower `ttl`, so clients ask for the records again sooner. Many providers don't accept TTLs below 60 seconds.
- Lower `attempts`. With `attempts: 1`, a single failed check removes a server, so keep it at 2 or more unless your servers respond reliably.

Keep `timeout` well below `interval`.

This configuration notices a failure within about 30 seconds, and keeps clients from using a failed server for about a minute and a half:

```yaml
interval: 10s

domains:
  - recordName: "app.example.com"
    provider: "my-dns"
    ttl: 60
    healthChecks:
      timeout: 2s
      attempts: 3
      # Add a server back only after 3 successful checks in a row
      recoverAfter: 3
    endpoints:
      - url: "http://192.0.2.10/healthz"
        ip: "192.0.2.10"
      - url: "http://192.0.2.11/healthz"
        ip: "192.0.2.11"
```

## Expected status code

By default, any 2xx status code means the endpoint is healthy. To accept only one code, set `expectStatus` to that code:

```yaml
healthChecks:
  # Only "204 No Content" means healthy
  expectStatus: "204"
```

`expectStatus` accepts one exact code between 100 and 599, or `2xx` for all codes in the range 200-299. Since ddup doesn't follow redirects, you can also use a 3xx code: for example, `"301"` for a URL that always redirects.

## Request method

ddup sends `GET` requests by default. Set `method: HEAD` to ask the server for the headers only, which is useful when the health check URL returns a large response.

```yaml
healthChecks:
  method: HEAD
```

## Checking a server by IP

When several servers serve the same domain, check each server directly, using its IP address in `url`. A URL with the domain name would reach whichever server DNS returns, which isn't necessarily the one you meant to check.

If your server needs a host name to route the request (for example, a web server with several virtual hosts), set `host`. ddup connects to the address in `url`, and sends `host` in the `Host` header.

```yaml
endpoints:
  - name: "web1"
    # Connects to 192.0.2.10, and requests https://app.example.com/healthz
    url: "https://192.0.2.10/healthz"
    ip: "192.0.2.10"
    host: "app.example.com"
  - name: "web2"
    url: "https://192.0.2.11/healthz"
    ip: "192.0.2.11"
    host: "app.example.com"
```

To use an IPv6 address in a URL, put it in square brackets:

```yaml
endpoints:
  - name: "web1-ipv6"
    url: "http://[2001:db8::10]:8080/healthz"
    ip: "2001:db8::10"
```

The `url` can also contain a different host name, such as one that resolves to a single server (for example, `web1.internal.example.com`). ddup resolves it again on every check.
