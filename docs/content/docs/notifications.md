---
title: "Notifications"
weight: 65
---

ddup can send you a message when it changes the DNS records of a domain, when an update fails, or when a domain has no healthy endpoint. It sends them to webhooks (including Slack, Discord, or ntfy) and by email.

Notifications are off until you add a `notifications` section to the configuration.

## Events

| Event | Sent when |
|-------|-----------|
| `dns_updated` | ddup changed the DNS records of a domain. The message lists the addresses published now and the ones that were in DNS before. |
| `dns_update_failed` | Updating the DNS records of a domain failed. The message includes the error. ddup tries again at every check, but doesn't send the event again while the error stays the same. |
| `all_unhealthy` | A domain has no healthy endpoint, so ddup leaves its DNS records as they are. The event is sent once when the domain enters this state. |

When a domain recovers, ddup updates DNS as usual and sends `dns_updated`. If the records in DNS are already the right ones, nothing changes and no event is sent.

Each destination receives every event by default. To receive only some, list them in the destination's `events` option.

## Webhooks

Each entry in `notifications.webhooks` sends a POST request to a URL.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `name` | string | **required** | Name of the destination, shown in logs. It must be unique among the webhooks. |
| `url` | string | **required** | URL to send the request to |
| `format` | string | `plain` | Format of the message: `plain`, `slack`, `discord`, or `ntfy` |
| `token` | string | | Token sent in the `Authorization: Bearer <token>` header, for example to publish to a protected ntfy topic. Some webhook formats, including Slack and Discord, don't need it. |
| `events` | list | All events | Events to send, see [Events](#events) |
| `timeout` | duration | `20s` | Timeout of each request |
| `maxAttempts` | number | `3` | Number of attempts to send each message, including the first. Set it to `1` to turn off retries. |
| `retryInterval` | duration | `20s` | Time to wait between attempts |
| `allowPrivateDestinations` | boolean | `false` | Allow sending to private addresses, see [Private destinations](#private-destinations) |

The formats are:

- `plain` sends the message as plain text.
- `slack` sends a message for a Slack incoming webhook, or for another service that accepts Slack's format.
- `discord` sends a message for a Discord webhook.
- `ntfy` publishes to an ntfy topic, with a title, a priority, and tags. See [Messages](#messages).

ddup retries a request after a network error, or a `408` or `5xx` response.

### Private destinations

By default, ddup refuses to send webhooks to private and non-routable addresses, e.g. `10.0.0.0/8` or `192.168.0.0/16`.

If the destination is on a network you trust, such as an ntfy server in your LAN or cluster, set `allowPrivateDestinations: true`.

### Webhook examples

Send every event to an ntfy topic on a server in your private network:

```yaml
notifications:
  webhooks:
    - name: "alerts"
      format: "ntfy"
      url: "https://ntfy.example.com/ddup"
      token: !file /run/secrets/ntfy-token
      allowPrivateDestinations: true
```

Send failed updates and outages to a Slack channel, and every event to a Discord channel:

```yaml
notifications:
  webhooks:
    - name: "slack-alerts"
      format: "slack"
      url: "https://hooks.slack.com/services/..."
      events: ["dns_update_failed", "all_unhealthy"]
    - name: "discord"
      format: "discord"
      url: "https://discord.com/api/webhooks/..."
```

## Email

Each entry in `notifications.emails` sends an email to one or more addresses.

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `name` | string | **required** | Name of the destination, shown in logs. It must be unique among the emails. |
| `connectionString` | string | **required** | Email provider and sender, see [Connection strings](#connection-strings) |
| `to` | list | **required** | Addresses to send the email to |
| `events` | list | All events | Events to send, see [Events](#events) |

Emails are sent once, without retries.

### Connection strings

The connection string picks the email provider and sets the sender:

| Provider | Connection string |
|----------|-------------------|
| Resend | `resend://<api-key>?fromAddress=<address>&fromName=<name>` |
| SMTP | `smtp://<username>:<password>@<host>:<port>?fromAddress=<address>&fromName=<name>&tls=<mode>` |
| AWS SES | `awsses://<access-key-id>:<secret-access-key>@<region>?fromAddress=<address>&fromName=<name>` |
| SendGrid | `sendgrid://<api-key>?fromAddress=<address>&fromName=<name>` |

`fromName` is optional. For SMTP, `tls` is `auto` (the default), `starttls`, `implicit`, or `none`.

[go-kit's documentation](https://github.com/ItalyPaleAle/go-kit/tree/main/emailer) lists all the options.

### Email example

Send failed updates and outages with Resend, and every event with an SMTP server:

```yaml
notifications:
  emails:
    - name: "ops-smtp"
      # The file contains smtp://ddup:<password>@smtp.example.com:587?fromAddress=ddup@example.com&tls=starttls
      connectionString: !file /run/secrets/smtp-connection-string
      to: ["postmaster@example.com", "oncall@example.com"]
    - name: "ops-resend"
      # The file contains resend://<api-key>?fromAddress=ddup@example.com&fromName=ddup
      connectionString: !file /run/secrets/resend-connection-string
      to: ["postmaster@example.com"]
      events: ["dns_update_failed", "all_unhealthy"]
```

## Delivery

Each webhook and each email recipient has its own queue of up to 64 events, sent in order. If a queue is full, ddup drops the new event for that destination and logs a warning.

## Messages

Every message has a title and a body, and ddup uses them the same way for each kind of destination:

- An email has the title as its subject, and the body as its text
- An ntfy notification has the title as its title, and the body as its message
- Plain text, Slack, and Discord messages contain the title, an empty line, and the body

The `Status` line of the body is `healthy` when all endpoints of the domain are healthy, `warning` when only some are, and `unhealthy` when none are or the update failed.

ntfy notifications also have tags and a priority. The first tag matches the status: `green_circle` for `healthy`, `yellow_circle` for `warning`, and `red_circle` for `unhealthy`. `dns_update_failed` and `all_unhealthy` add the `warning` tag. The priority is `urgent` (5) for `all_unhealthy`, `high` (4) for `dns_update_failed`, and the ntfy server's default for `dns_updated`.

A `dns_updated` event sent to ntfy. The first two lines are the title and the tags, which ddup sends as ntfy headers, and the rest is the body:

```text
Title: example.com now points to 192.0.2.10, 192.0.2.11
Tags: green_circle

Event: dns_updated
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: healthy
Now: 192.0.2.10, 192.0.2.11
Before: 192.0.2.12

Endpoints:
  server1 (192.0.2.10): healthy
  server2 (192.0.2.11): healthy
  server3 (192.0.2.12): healthy
```

A `dns_update_failed` event sent by email, with the subject first:

```text
Subject: DNS update failed for example.com

Event: dns_update_failed
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: unhealthy
Wanted: 192.0.2.10, 192.0.2.11
Before: 192.0.2.12
Error: provider returned status 500

Endpoints:
  server1 (192.0.2.10): healthy
  server2 (192.0.2.11): healthy
  server3 (192.0.2.12): UNHEALTHY
```

An `all_unhealthy` event sent to Slack or Discord:

```text
No healthy endpoints for example.com

Event: all_unhealthy
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: unhealthy
Unchanged: 192.0.2.10, 192.0.2.11

Endpoints:
  server1 (192.0.2.10): UNHEALTHY
  server2 (192.0.2.11): UNHEALTHY
```
