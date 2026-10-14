package notify

import (
	"fmt"
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

var _ webhook.MessageProvider = message{}

// newMessage builds the notification for an event
func newMessage(ev Event) message {
	status := ev.status()
	m := message{
		body: messageBody(ev, status),
	}

	// The tag shows a colored circle for the health of the domain
	switch status {
	case StatusHealthy:
		m.tags = []string{"green_circle"}
	case StatusWarning:
		m.tags = []string{"yellow_circle"}
	default:
		m.tags = []string{"red_circle"}
	}

	switch ev.Type {
	case config.EventDNSUpdated:
		m.title = fmt.Sprintf("%s now points to %s", ev.Domain, joinSorted(ev.Published))
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
	fmt.Fprintf(&b, "Event: %s\nDomain: %s\nTime: %s\nStatus: %s\n", ev.Type, ev.Domain, ev.Time.UTC().Format(time.RFC3339), status)

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
		fmt.Fprintf(&b, "Error: %s\n", ev.Error)
	}

	if len(ev.Endpoints) > 0 {
		b.WriteString("\nEndpoints:\n")
		for _, ep := range ev.Endpoints {
			state := "healthy"
			if !ep.Healthy {
				state = "UNHEALTHY"
			}
			fmt.Fprintf(&b, "  %s (%s): %s\n", ep.Name, ep.IP, state)
		}
	}

	return b.String()
}

func writeAddresses(b *strings.Builder, label string, addrs []string) {
	if len(addrs) == 0 {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", label, joinSorted(addrs))
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
