package config

import (
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"
)

// NotificationEvent is the type of an event that can trigger a notification
type NotificationEvent string

const (
	// EventDNSUpdated is sent when the DNS records of a domain were changed
	EventDNSUpdated NotificationEvent = "dns_updated"
	// EventDNSUpdateFailed is sent when updating the DNS records of a domain failed
	EventDNSUpdateFailed NotificationEvent = "dns_update_failed"
	// EventAllUnhealthy is sent when a domain has no healthy endpoints
	EventAllUnhealthy NotificationEvent = "all_unhealthy"
)

// NotificationEvents returns all valid events
func NotificationEvents() []NotificationEvent {
	return []NotificationEvent{EventDNSUpdated, EventDNSUpdateFailed, EventAllUnhealthy}
}

// Webhook formats
// The list must match the formats supported by go-kit
const (
	WebhookFormatPlain   = "plain"
	WebhookFormatSlack   = "slack"
	WebhookFormatDiscord = "discord"
	WebhookFormatNtfy    = "ntfy"
)

// ConfigNotifications configures the notifications sent when something happens to a domain
// All kinds are optional, and if none is configured ddup does not send notifications
type ConfigNotifications struct {
	// Notifications sent to webhooks, using the plain, Slack, Discord, or ntfy formats
	Webhooks []ConfigNotificationWebhook `yaml:"webhooks"`

	// Notifications sent by email
	Emails []ConfigNotificationEmail `yaml:"emails"`
}

// ConfigNotificationDelivery are the delivery settings shared by the notifications sent with HTTP requests
type ConfigNotificationDelivery struct {
	// Timeout for each request
	// +default 20s
	Timeout time.Duration `yaml:"timeout"`

	// Maximum number of attempts to send each notification, including the first
	// Network errors, 408, 429 and 5xx responses are retried
	// +default 3
	MaxAttempts int `yaml:"maxAttempts"`

	// Delay between attempts
	// +default 20s
	RetryInterval time.Duration `yaml:"retryInterval"`

	// If true, allows sending notifications to private and non-routable addresses (such as 10.0.0.0/8, 192.168.0.0/16, or localhost)
	// Enable it only if the destination is on a trusted internal network
	// +default false
	AllowPrivateDestinations bool `yaml:"allowPrivateDestinations"`

	// Optional token sent in the Authorization header as "Bearer <token>"
	// Some formats may not use it
	Token string `yaml:"token"`
}

// ConfigNotificationWebhook configures notifications sent to a webhook
type ConfigNotificationWebhook struct {
	ConfigNotificationDelivery `yaml:",inline"`

	// Name of the notification, used in logs
	// Must be unique among the webhook notifications
	// +required
	Name string `yaml:"name"`

	// Format of the message: "plain", "slack", "discord", or "ntfy"
	// +default "plain"
	Format string `yaml:"format"`

	// URL of the webhook
	// +required
	URL string `yaml:"url"`

	// Events to send, among "dns_updated", "dns_update_failed" and "all_unhealthy"
	// +default all events
	Events []NotificationEvent `yaml:"events"`
}

// ConfigNotificationEmail configures notifications sent by email
type ConfigNotificationEmail struct {
	// Name of the notification, used in logs
	// Must be unique among the email notifications
	// +required
	Name string `yaml:"name"`

	// Connection string for the email provider, such as "resend://<api-key>?fromAddress=ddup@example.com" or "smtp://..."
	// +required
	ConnectionString string `yaml:"connectionString"`

	// Recipients of the email: one email is sent to each address
	// +required
	To []string `yaml:"to"`

	// Events to send, among "dns_updated", "dns_update_failed" and "all_unhealthy"
	// +default all events
	Events []NotificationEvent `yaml:"events"`
}

// validate the notifications configuration and sets the defaults
// URLs, timeouts, and connection strings are validated by go-kit when the notifications are created
func (c *ConfigNotifications) validate() error {
	names := make(map[string]int, len(c.Webhooks))
	for i := range c.Webhooks {
		w := &c.Webhooks[i]
		label := notificationLabel("webhooks", i, w.Name)

		err := checkNotificationName(names, i, w.Name)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}

		w.Format = strings.ToLower(w.Format)
		switch w.Format {
		case "":
			w.Format = WebhookFormatPlain
		case WebhookFormatPlain, WebhookFormatSlack, WebhookFormatDiscord, WebhookFormatNtfy:
		default:
			return fmt.Errorf("%s is invalid: unknown format %q", label, w.Format)
		}

		w.Events, err = validateNotificationEvents(w.Events)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
	}

	clear(names)

	for i := range c.Emails {
		e := &c.Emails[i]
		label := notificationLabel("emails", i, e.Name)

		err := checkNotificationName(names, i, e.Name)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}

		if len(e.To) == 0 {
			return fmt.Errorf("%s is invalid: to must have at least one recipient", label)
		}

		for ti, addr := range e.To {
			_, err = mail.ParseAddress(addr)
			if err != nil {
				return fmt.Errorf("%s is invalid: to[%d] is not a valid email address", label, ti)
			}
		}

		e.Events, err = validateNotificationEvents(e.Events)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
	}

	return nil
}

// notificationLabel identifies a notification in error messages, like `notifications.ntfy[0] "alerts"`
func notificationLabel(kind string, index int, name string) string {
	if name == "" {
		return fmt.Sprintf("notifications.%s[%d]", kind, index)
	}
	return fmt.Sprintf("notifications.%s[%d] %q", kind, index, name)
}

// checkNotificationName ensures that the name is set and not used by another notification of the same kind
func checkNotificationName(seen map[string]int, index int, name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	prev, ok := seen[name]
	if ok {
		return fmt.Errorf("name is already used by entry %d", prev)
	}
	seen[name] = index
	return nil
}

// validateNotificationEvents checks the list of events, and returns all events if the list is empty
func validateNotificationEvents(events []NotificationEvent) ([]NotificationEvent, error) {
	if len(events) == 0 {
		return NotificationEvents(), nil
	}

	valid := NotificationEvents()
	for _, ev := range events {
		if !slices.Contains(valid, ev) {
			names := make([]string, len(valid))
			for i, v := range valid {
				names[i] = string(v)
			}

			return nil, fmt.Errorf("unknown event %q (valid events: %s)", ev, strings.Join(names, ", "))
		}
	}
	return events, nil
}
