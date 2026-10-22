---
title: "Dynamic DNS"
weight: 55
---

An endpoint can follow the public IP address of the network ddup runs in (dynamic DNS), instead of having a fixed `ip`. Set `ipLookupFamily` to `4` or `6`: `4` creates an A record and `6` an AAAA record.

**ddup has no lookup service built in, and contacts none unless you configure it.** You list the services in `ipLookupURLs`. Whichever service you pick sees the public IP address of your network, which is what these services are for, plus the fact that a ddup client is asking. Use one you trust, or one you host yourself.

On every check ddup asks the services for the current public address and publishes it, and updates the record when it changes.

## Services for all the endpoints

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

## Services for one endpoint

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
          - url: "https://ip2.example.com/plain"
```

Different endpoints can have different lists, also in the same domain.

This is also how an endpoint gets its address from something other than a public service. The URL can be anything that answers with just an IP address, for example a script on a server you own or a page on your router. It needs to use `https`.

## Rules

- The list of an endpoint **replaces** the top-level list for that endpoint. They are never merged: the top-level services are not contacted for it.
- An endpoint's list can have one entry or several. All the services that apply to an endpoint (all the entries of its own list, or all the top-level entries with its family) are asked at the same time. With one entry, only that service is asked.
- At the top level, `family` (`4` or `6`) says which IP version the service answers for. It defaults to `4`.
- In an endpoint's list, `family` is optional, because `ipLookupFamily` already says it. If the endpoint sets neither `ip` nor `ipLookupFamily`, `ipLookupFamily` is `4`. If it is set, it must be equal to `ipLookupFamily`: ddup refuses a configuration that mixes IPv4 and IPv6 services for one endpoint.
- An endpoint with `ipLookupFamily` needs at least one service, from its own list or from a top-level entry with its family. Otherwise the configuration is rejected, and the message says what to add. An empty list (`ipLookupURLs: []`) on an endpoint is rejected as well.
- `ipLookupURLs` on an endpoint with a fixed `ip` is rejected, because it would never be used.
- `url` must use `https`. An answer that can't be authenticated could be spoofed, and ddup would publish it.
- A service must answer with just the IP address, as plain text.

## How the lookup works

- With no `url` the endpoint isn't health-checked: it's healthy as long as the lookup works. With a `url`, the endpoint is also health-checked as usual, and the address that was looked up is the one published.
- Requests are made over the requested IP version only, so a service sees (and returns) an address of that family.
- The first valid answer is used. The other services finish in the background, and ddup logs a warning if they return a different address. A service that fails or returns something that is not valid is skipped; if all of them fail, the lookup fails, the endpoint is unhealthy, and DNS is left as it is until an address is found (like any other unhealthy endpoint).
- An answer is rejected unless it is just an IP address of the right family: an IPv4 address for `ipLookupFamily: 4` and an IPv6 address for `6`. Loopback, private, carrier-grade NAT (`100.64.0.0/10`), link-local, multicast and unspecified addresses are rejected too. Responses are limited to 4 KB, redirects are not followed, and each service has the health check `timeout` to answer.
- Requests use the proxy settings of the environment, like any other request, and every lookup opens a new connection, so the answer always reflects the current network path.
- If two endpoints end up with the same address, it is published once.
- ddup replaces every `A`/`AAAA` record at the name that isn't the looked-up address, so don't point it at a name that has other records.

Any service that answers with the caller's address works, including a few lines on a VPS you own that echo the client address.
