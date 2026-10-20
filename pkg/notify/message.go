package notify

import (
	"slices"
	"strings"
	"time"

	"github.com/italypaleale/go-kit/webhook"

	"github.com/italypaleale/ddup/pkg/config"
)

// message is the notification built from an event
// It implements webhook.MessageProvider, and its title and body are also the subject and text of emails
type message struct {
	title    string
	body     string
	priority webhook.NtfyPriority
	tags     []string
}

// newMessage builds the notification for an event
func newMessage(ev Event) message {
	status := ev.status()
	m := message{
		body: messageBody(ev, status),
		tags: make([]string, 0, 2),
	}

	// The tag shows a colored circle for the health of the domain
	switch status {
	case StatusHealthy:
		m.tags = append(m.tags, "green_circle")
	case StatusWarning:
		m.tags = append(m.tags, "yellow_circle")
	default:
		m.tags = append(m.tags, "red_circle")
	}

	switch ev.Type {
	case config.EventDNSUpdated:
		m.title = ev.Domain + " now points to " + joinSorted(ev.Published)
	case config.EventDNSUpdateFailed:
		m.title = "DNS update failed for " + ev.Domain
		m.priority = webhook.NtfyPriorityHigh
		m.tags = append(m.tags, "warning")
	case config.EventAllUnhealthy:
		m.title = "No healthy endpoints for " + ev.Domain
		m.priority = webhook.NtfyPriorityMax
		m.tags = append(m.tags, "warning")
	default:
		m.title = string(ev.Type) + " " + ev.Domain
	}

	return m
}

func messageBody(ev Event, status string) string {
	var b strings.Builder
	b.WriteString("Event: ")
	b.WriteString(string(ev.Type))
	b.WriteString("\nDomain: ")
	b.WriteString(ev.Domain)
	b.WriteString("\nTime: ")
	b.WriteString(ev.Time.UTC().Format(time.RFC3339))
	b.WriteString("\nStatus: ")
	b.WriteString(status)
	b.WriteByte('\n')

	switch ev.Type {
	case config.EventDNSUpdated:
		writeAddresses(&b, "Now", ev.Published)
		writeAddresses(&b, "Before", ev.Previous)
	case config.EventDNSUpdateFailed:
		writeAddresses(&b, "Wanted", ev.Published)
		writeAddresses(&b, "Before", ev.Previous)
	case config.EventAllUnhealthy:
		writeAddresses(&b, "Unchanged", ev.Previous)
	}

	if ev.Error != "" {
		b.WriteString("Error: ")
		b.WriteString(ev.Error)
		b.WriteByte('\n')
	}

	if len(ev.Endpoints) > 0 {
		b.WriteString("\nEndpoints:\n")
		for _, ep := range ev.Endpoints {
			state := "healthy"
			if !ep.Healthy {
				state = "UNHEALTHY"
			}
			b.WriteString("  ")
			b.WriteString(ep.Name)
			b.WriteString(" (")
			b.WriteString(ep.IP)
			b.WriteString("): ")
			b.WriteString(state)
			b.WriteByte('\n')
		}
	}

	return b.String()
}

func writeAddresses(b *strings.Builder, label string, addrs []string) {
	if len(addrs) == 0 {
		return
	}

	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(joinSorted(addrs))
	b.WriteByte('\n')
}

func joinSorted(addrs []string) string {
	sorted := slices.Clone(addrs)
	slices.Sort(sorted)
	return strings.Join(sorted, ", ")
}

// GetPlainMessage implements webhook.MessageProvider
func (m message) GetPlainMessage() (string, error) {
	return m.title + "\n\n" + m.body, nil
}

// GetSlackMessage implements webhook.MessageProvider
func (m message) GetSlackMessage() (webhook.SlackMessage, error) {
	return webhook.SlackMessage{
		Text: m.title + "\n\n" + m.body,
	}, nil
}

// GetNtfyMessage implements webhook.MessageProvider
func (m message) GetNtfyMessage() (webhook.NtfyMessage, error) {
	return webhook.NtfyMessage{
		Message:  m.body,
		Title:    m.title,
		Priority: m.priority,
		Tags:     m.tags,
	}, nil
}
