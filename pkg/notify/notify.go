// Package notify delivers events to user-configured webhooks
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
)

// Event types
const (
	EventDNSUpdated      = "dns_updated"
	EventDNSUpdateFailed = "dns_update_failed"
	EventAllUnhealthy    = "all_unhealthy"
)

// Event is sent to webhooks, and is the data available to body and header templates
type Event struct {
	Type   string    `json:"event"`
	Domain string    `json:"domain"`
	Time   time.Time `json:"time"`
	// All healthy targets (IP addresses or CNAME hostnames)
	Healthy []string `json:"healthy"`
	// The healthy targets that are published in DNS: those with the lowest priority value
	Published []string `json:"published"`
	// Priority value of the published targets, and of those published before the check
	Tier         int `json:"tier"`
	PreviousTier int `json:"previousTier"`
	// What was in DNS before the update (for dns_updated), or the previously healthy targets
	Previous  []string        `json:"previous"`
	Endpoints []EndpointState `json:"endpoints"`
	Error     string          `json:"error,omitempty"`
}

// EndpointState is the result of the latest health check for an endpoint
type EndpointState struct {
	Name string `json:"name"`
	// IP address, or CNAME hostname
	IP       string `json:"ip"`
	Priority int    `json:"priority"`
	Healthy  bool   `json:"healthy"`
	// True if the endpoint is published in DNS
	Active bool   `json:"active"`
	Error  string `json:"error,omitempty"`
}

// Subject returns a short human-readable summary of the event
func (e Event) Subject() string {
	switch e.Type {
	case EventDNSUpdated:
		return fmt.Sprintf("%s now points to %s", e.Domain, strings.Join(e.targets(), ", "))
	case EventDNSUpdateFailed:
		return "DNS update failed for " + e.Domain
	case EventAllUnhealthy:
		return "No healthy endpoints for " + e.Domain
	default:
		return e.Type + " " + e.Domain
	}
}

type hook struct {
	cfg     config.ConfigWebhook
	body    *template.Template
	headers map[string]*template.Template
}

// targets returns what's published in DNS, which is all healthy targets if there's no priority information
func (e Event) targets() []string {
	if len(e.Published) > 0 {
		return e.Published
	}
	return e.Healthy
}

// Text returns a plain-text multi-line description of the event, useful as the body of an email or chat message
func (e Event) Text() string {
	var b strings.Builder
	b.WriteString(e.Subject() + "\n\n")
	fmt.Fprintf(&b, "Event: %s\nDomain: %s\nTime: %s\n", e.Type, e.Domain, e.Time.Format(time.RFC3339))
	if len(e.Previous) > 0 {
		fmt.Fprintf(&b, "Previous records: %s\n", strings.Join(e.Previous, ", "))
	}
	if len(e.targets()) > 0 {
		fmt.Fprintf(&b, "Current records: %s\n", strings.Join(e.targets(), ", "))
	}
	if e.Type == EventDNSUpdated && e.Tier != e.PreviousTier {
		fmt.Fprintf(&b, "Priority: %d (was %d)\n", e.Tier, e.PreviousTier)
	}
	if e.Error != "" {
		fmt.Fprintf(&b, "Error: %s\n", e.Error)
	}
	if len(e.Endpoints) > 0 {
		b.WriteString("\nEndpoints:\n")
		for _, ep := range e.Endpoints {
			status := "healthy"
			if ep.Healthy && !ep.Active {
				status = "healthy (standby)"
			}
			if !ep.Healthy {
				status = "UNHEALTHY"
				if ep.Error != "" {
					status += " (" + ep.Error + ")"
				}
			}
			fmt.Fprintf(&b, "  %s (%s): %s\n", ep.Name, ep.IP, status)
		}
	}
	return b.String()
}

// Notifier delivers events to webhooks without blocking the caller
// Deliveries are retried with exponential backoff, so a destination that is temporarily unreachable (for example while ingress is down) still gets the event
// A nil Notifier is valid and does nothing
type Notifier struct {
	hooks       []*hook
	client      *http.Client
	baseBackoff time.Duration
	wg          sync.WaitGroup
}

// New creates a Notifier
func New(webhooks []config.ConfigWebhook) (*Notifier, error) {
	if len(webhooks) == 0 {
		return nil, nil //nolint:nilnil
	}

	n := &Notifier{
		client:      &http.Client{},
		baseBackoff: 2 * time.Second,
		hooks:       make([]*hook, 0, len(webhooks)),
	}
	for _, w := range webhooks {
		h := &hook{cfg: w, headers: make(map[string]*template.Template, len(w.Headers))}
		var err error
		if w.Body != "" {
			h.body, err = template.New("body").Funcs(config.WebhookTemplateFuncs).Parse(w.Body)
			if err != nil {
				return nil, fmt.Errorf("webhook %q: body template: %w", w.Name, err)
			}
		}
		for k, v := range w.Headers {
			h.headers[k], err = template.New(k).Funcs(config.WebhookTemplateFuncs).Parse(v)
			if err != nil {
				return nil, fmt.Errorf("webhook %q: header %q template: %w", w.Name, k, err)
			}
		}
		n.hooks = append(n.hooks, h)
	}
	return n, nil
}

// Notify sends the event to every webhook subscribed to it, in the background
// Deliveries, including retries, stop when ctx is canceled
func (n *Notifier) Notify(ctx context.Context, ev Event) {
	if n == nil {
		return
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}

	for _, h := range n.hooks {
		if len(h.cfg.Events) > 0 && !slices.Contains(h.cfg.Events, ev.Type) {
			continue
		}
		n.wg.Go(func() {
			n.deliver(ctx, h, ev)
		})
	}
}

// Wait blocks until in-flight deliveries finish or the timeout elapses; used at shutdown
func (n *Notifier) Wait(timeout time.Duration) {
	if n == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
}

func (n *Notifier) deliver(ctx context.Context, h *hook, ev Event) {
	log := slog.With("webhook", h.cfg.Name, "event", ev.Type, "domain", ev.Domain)

	body, contentType, headers, err := h.render(ev)
	if err != nil {
		log.ErrorContext(ctx, "Failed to render webhook", "error", err)
		return
	}

	backoff := n.baseBackoff
	for attempt := 1; ; attempt++ {
		err = n.send(ctx, h, body, contentType, headers)
		if err == nil {
			log.DebugContext(ctx, "Webhook delivered", "attempt", attempt)
			return
		}
		if attempt >= h.cfg.Attempts {
			log.ErrorContext(ctx, "Webhook delivery failed, giving up", "attempts", attempt, "error", err)
			return
		}

		log.WarnContext(ctx, "Webhook delivery failed, will retry", "attempt", attempt, "retryIn", backoff, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (h *hook) render(ev Event) (body []byte, contentType string, headers map[string]string, err error) {
	if h.body == nil {
		body, err = json.Marshal(ev)
		if err != nil {
			return nil, "", nil, fmt.Errorf("marshaling event: %w", err)
		}
		contentType = "application/json"
	} else {
		var buf bytes.Buffer
		err = h.body.Execute(&buf, ev)
		if err != nil {
			return nil, "", nil, fmt.Errorf("executing body template: %w", err)
		}
		body = buf.Bytes()
		contentType = "text/plain; charset=utf-8"
	}

	headers = make(map[string]string, len(h.headers))
	for k, t := range h.headers {
		var buf bytes.Buffer
		err = t.Execute(&buf, ev)
		if err != nil {
			return nil, "", nil, fmt.Errorf("executing header %q template: %w", k, err)
		}
		headers[k] = buf.String()
	}
	return body, contentType, headers, nil
}

func (n *Notifier) send(ctx context.Context, h *hook, body []byte, contentType string, headers map[string]string) error {
	ctx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, h.cfg.Method, h.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "ddup/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return errors.New("status code " + resp.Status + ": " + strings.TrimSpace(string(snippet)))
	}
	return nil
}
