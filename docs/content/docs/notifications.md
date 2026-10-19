---
title: "Notifications"
weight: 65
---

ddup can notify you when something happens to a domain. Notifications are optional: with no `notifications` section, ddup sends nothing.

Notifications can be sent to webhooks (plain text, Slack, Discord, or [ntfy](https://ntfy.sh)), and by email.
The messages are built by ddup and are not customizable.

## Events

| Event | When it is sent |
| :---- | :-------------- |
| `dns_updated` | The DNS records of a domain were changed. The message has the addresses published now and the ones that were in DNS before |
| `dns_update_failed` | Updating the DNS records of a domain failed. The message has the error. ddup retries at every check, and the same error is notified only once |
| `all_unhealthy` | A domain has no healthy endpoint. DNS is left unchanged. It is sent once, when the domain enters this state (including at startup), and not on every check |

When a domain recovers after `all_unhealthy`, ddup updates DNS as usual and sends `dns_updated`. If the records in DNS are already the right ones, nothing changed and no event is sent.

Every notification has an optional `events` list to choose the events to receive. If omitted, it receives all events.

## Settings

- `notifications`: Notifications to send
  - `webhooks`: List of webhooks
    - `name`: Name of the notification, used in logs. Required, and unique among the `webhooks` entries
    - `format`: `plain` (default, the message as plain text), `slack` (a Slack-compatible message), `discord` (Discord's Slack-compatible endpoint), or `ntfy` (a message for an [ntfy](https://ntfy.sh) topic)
    - `url`: URL of the webhook. Required. For `ntfy`, it includes the topic name, such as `https://ntfy.example.com/ddup`
    - `token`: Optional token, sent as `Authorization: Bearer <token>`. Some formats do not use it
    - `events`: Events to send (default: all)
  - `emails`: List of email notifications
    - `name`: Name of the notification, used in logs. Required, and unique among the `emails` entries
    - `connectionString`: Connection string for the email provider. Required. The supported providers are Resend (`resend://<api-key>?fromAddress=<address>&fromName=<name>`), SMTP (`smtp://<username>:<password>@<host>:<port>?fromAddress=<address>&fromName=<name>&tls=<auto|starttls|implicit|none>`), AWS SES (`awsses://<access-key-id>:<secret-access-key>@<region>?fromAddress=<address>&fromName=<name>`), SendGrid, and `console://` (logs the email instead of sending it, for development). See [go-kit's documentation](https://github.com/ItalyPaleAle/go-kit/tree/main/emailer) for all options
    - `to`: List of recipients. One email is sent to each address. Required
    - `events`: Events to send (default: all)

`webhooks` entries also have these optional settings for delivering the request:

- `timeout`: Timeout of each request (default: `20s`)
- `maxAttempts`: Maximum number of attempts to send each notification, including the first (default: 3). Network errors and `408`, `429` and `5xx` responses are retried, while other errors are not. Set it to `1` to disable retries
- `retryInterval`: Delay between attempts (default: `20s`)
- `allowPrivateDestinations`: By default, ddup refuses to send notifications to private and non-routable addresses (such as `10.x.x.x`, `192.168.x.x`, link-local addresses, and `localhost`). This keeps a webhook URL that points to an internal service from being used to reach it. Set it to `true` when the destination is on a trusted internal network, for example an ntfy server in your LAN or cluster (default: `false`)

Redirects are never followed.
Emails are sent once, without retries.

The tokens of webhooks and the email connection strings are secrets, and URLs of Slack and Discord webhooks are confidential. Read them from files with `!file` (see [Secrets](/docs/configuration/#secrets)), so the configuration file does not contain them. Failures are logged with the `name` of the notification and the error, which can include the URL.

## Examples

Send all events to an ntfy topic on a server in a private network:

```yaml
notifications:
  webhooks:
    - name: "alerts"
      format: "ntfy"
      url: "https://ntfy.example.com/ddup"
      token: !file /run/secrets/ntfy-token
      allowPrivateDestinations: true
```

Send the events about problems to a Slack channel, and all events to a Discord channel:

```yaml
notifications:
  webhooks:
    - name: "slack-alerts"
      format: "slack"
      url: !file /run/secrets/slack-webhook-url
      events: ["dns_update_failed", "all_unhealthy"]
    - name: "discord"
      format: "discord"
      url: !file /run/secrets/discord-webhook-url
```

Send emails with Resend, or with an SMTP server (the file contains the whole connection string, such as `resend://<api-key>?fromAddress=ddup@example.com&fromName=ddup`):

```yaml
notifications:
  emails:
    - name: "ops-resend"
      connectionString: !file /run/secrets/resend-connection-string
      to: ["postmaster@example.com"]
      events: ["dns_update_failed", "all_unhealthy"]
    - name: "ops-smtp"
      # smtp://ddup:<password>@smtp.example.com:587?fromAddress=ddup@example.com&tls=starttls
      connectionString: !file /run/secrets/smtp-connection-string
      to: ["postmaster@example.com", "oncall@example.com"]
```

## Delivery

Notifications are sent in the background, so a slow or unavailable destination never delays a health check. Each notification has its own queue of up to 64 events: if a destination cannot keep up, the oldest events are delivered first and the newest are dropped, with a warning in the logs. When ddup stops, it waits a few seconds for queued notifications to be delivered.

When ddup starts, it checks the settings of every notification (including the email connection strings), and exits with an error if one is invalid.

## Messages

Every message has a title and a body. The title is the subject of emails and the title of ntfy notifications. The body is the same for all kinds, and Slack and Discord messages are the title and the body in one text.

The ntfy notifications also set the tags `green_circle`, `yellow_circle`, or `red_circle` for the health of the domain (all, some, or none of its endpoints are healthy), and `warning` for `dns_update_failed` and `all_unhealthy`. The priority is `urgent` (5) for `all_unhealthy` and `high` (4) for `dns_update_failed`, and the default of the ntfy server for `dns_updated`.

`dns_updated`, as an ntfy notification (title, tags, then the body):

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

`dns_update_failed`, as an email (the subject, then the text):

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

`all_unhealthy`, as a Slack or Discord message:

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
