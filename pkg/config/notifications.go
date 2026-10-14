package config

import (
	"errors"
	"fmt"
	"net/mail"
	"net/url"
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

// Valid webhook formats
const (
	WebhookFormatPlain   = "plain"
	WebhookFormatSlack   = "slack"
	WebhookFormatDiscord = "discord"
)

// ConfigNotifications configures the notifications sent when something happens to a domain
// All kinds are optional, and if none is configured ddup does not send notifications
type ConfigNotifications struct {
	// Notifications sent to ntfy topics
	Ntfy []ConfigNotificationNtfy `yaml:"ntfy"`

	// Notifications sent to webhooks, using the plain, Slack, or Discord formats
	Webhooks []ConfigNotificationWebhook `yaml:"webhooks"`

	// Notifications sent by email
	Email []ConfigNotificationEmail `yaml:"email"`
}

// ConfigNotificationDelivery are the delivery settings shared by the notifications sent with HTTP requests
// For all of them, zero values mean using the defaults
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
}

// ConfigNotificationNtfy configures notifications sent to an ntfy topic
type ConfigNotificationNtfy struct {
	ConfigNotificationDelivery `yaml:",inline"`

	// Name of the notification, used in logs
	// Must be unique among the ntfy notifications
	// +required
	Name string `yaml:"name"`

	// URL of the topic, including the topic name (for example "https://ntfy.example.com/ddup")
	// +required
	URL string `json:"-" yaml:"url"`

	// Access token for the topic, sent as "Bearer <token>"
	// Use `!file` to read it from a file
	Token string `json:"-" yaml:"token"`

	// Events to send, among "dns_updated", "dns_update_failed" and "all_unhealthy"
	// +default all events
	Events []NotificationEvent `yaml:"events"`
}

// ConfigNotificationWebhook configures notifications sent to a webhook
type ConfigNotificationWebhook struct {
	ConfigNotificationDelivery `yaml:",inline"`

	// Name of the notification, used in logs
	// Must be unique among the webhook notifications
	// +required
	Name string `yaml:"name"`

	// Format of the message: "plain", "slack", or "discord"
	// +default "plain"
	Format string `yaml:"format"`

	// URL of the webhook
	// Slack and Discord URLs are secrets: use `!file` to read it from a file
	// +required
	URL string `json:"-" yaml:"url"`

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
	// It contains secrets: use `!file` to read it from a file
	// +required
	ConnectionString string `json:"-" yaml:"connectionString"`

	// Recipients of the email: one email is sent to each address
	// +required
	To []string `yaml:"to"`

	// Events to send, among "dns_updated", "dns_update_failed" and "all_unhealthy"
	// +default all events
	Events []NotificationEvent `yaml:"events"`
}

// validate the notifications configuration and sets the defaults
// Error messages never include URLs, tokens, or connection strings, because they are secrets
func (c *ConfigNotifications) validate() error {
	names := make(map[string]int, len(c.Ntfy))
	for i := range c.Ntfy {
		n := &c.Ntfy[i]
		label := notificationLabel("ntfy", i, n.Name)

		err := checkNotificationName(names, i, n.Name)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}

		err = validateNotificationURL(n.URL, true)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
		err = n.validate()
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
		n.Events, err = validateNotificationEvents(n.Events)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
	}

	clear(names)
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
		case WebhookFormatPlain, WebhookFormatSlack, WebhookFormatDiscord:
		default:
			return fmt.Errorf("%s is invalid: unknown format %q (valid formats: %s, %s, %s)", label, w.Format, WebhookFormatPlain, WebhookFormatSlack, WebhookFormatDiscord)
		}

		err = validateNotificationURL(w.URL, false)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
		err = w.validate()
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
		w.Events, err = validateNotificationEvents(w.Events)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}
	}

	clear(names)
	for i := range c.Email {
		e := &c.Email[i]
		label := notificationLabel("email", i, e.Name)

		err := checkNotificationName(names, i, e.Name)
		if err != nil {
			return fmt.Errorf("%s is invalid: %w", label, err)
		}

		if strings.TrimSpace(e.ConnectionString) == "" {
			return fmt.Errorf("%s is invalid: connectionString is empty", label)
		}
		if len(e.To) == 0 {
			return fmt.Errorf("%s is invalid: to is empty; specify at least one recipient", label)
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

func (d ConfigNotificationDelivery) validate() error {
	if d.Timeout < 0 {
		return errors.New("timeout must not be negative")
	}
	if d.MaxAttempts < 0 {
		return errors.New("maxAttempts must not be negative")
	}
	if d.RetryInterval < 0 {
		return errors.New("retryInterval must not be negative")
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

// validateNotificationURL ensures that the URL uses http or https
// The URL is never included in the errors
func validateNotificationURL(val string, requireTopic bool) error {
	if val == "" {
		return errors.New("url is empty")
	}

	u, err := url.Parse(val)
	if err != nil {
		return errors.New("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must use the http or https scheme")
	}
	if u.Host == "" {
		return errors.New("url must include a host")
	}
	if requireTopic && strings.Trim(u.Path, "/") == "" {
		return errors.New("url must include the topic name, like https://ntfy.example.com/topic")
	}
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
