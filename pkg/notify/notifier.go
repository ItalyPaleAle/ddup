// Package notify delivers notifications about the events of the domains
// Delivery is asynchronous, so a slow or failing destination never delays a health check
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"sync"
	"time"

	"github.com/italypaleale/go-kit/emailer"
	"github.com/italypaleale/go-kit/webhook"

	"github.com/italypaleale/ddup/pkg/config"
)

const (
	// Number of events that can wait for delivery to each destination
	// When the queue is full, new events for that destination are dropped
	queueSize = 64

	// After the context passed to Shutdown is canceled, we wait up to this long for in-flight deliveries to stop
	cancelGracePeriod = time.Second
)

// Notifier sends events to the destinations in the configuration
// A nil Notifier is valid and does nothing
type Notifier struct {
	senders []*sender
	log     *slog.Logger

	// Canceled to abort in-flight deliveries
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// Protects closed, and the queues from being closed while Notify writes to them
	lock   sync.RWMutex
	closed bool
}

// sender is a destination for notifications, with its own queue and goroutine
type sender struct {
	kind   string
	name   string
	events map[config.NotificationEvent]struct{}
	queue  chan Event
	// Sends the message, returning an error that can be logged
	send func(ctx context.Context, msg message) error
}

// emailSender is the part of emailer.Emailer used to send notifications
type emailSender interface {
	SendEmail(ctx context.Context, to emailer.EmailAddress, subject string, message emailer.SendEmailMessage) error
}

// dependencies are the constructors of the libraries used to send notifications, replaced in tests
type dependencies struct {
	newWebhook func(opts webhook.NewWebhookOpts) (webhook.Webhook, error)
	newEmailer func(ctx context.Context, opts emailer.NewEmailerOpts) (emailSender, error)
}

func newEmailer(ctx context.Context, opts emailer.NewEmailerOpts) (emailSender, error) {
	em, err := emailer.NewEmailer(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create emailer: %w", err)
	}
	return em, nil
}

// New creates the Notifier for the configuration, and starts the goroutines that deliver notifications
// It returns nil if no notifications are configured
// The configuration must have been validated
func New(ctx context.Context, cfg *config.Config) (*Notifier, error) {
	return newNotifier(ctx, cfg, dependencies{
		newWebhook: webhook.NewWebhook,
		newEmailer: newEmailer,
	})
}

func newNotifier(ctx context.Context, cfg *config.Config, deps dependencies) (*Notifier, error) {
	nc := cfg.Notifications
	if len(nc.Webhooks) == 0 && len(nc.Emails) == 0 {
		return nil, nil //nolint:nilnil
	}

	n := &Notifier{
		senders: make([]*sender, 0, len(nc.Webhooks)+len(nc.Emails)),
		log:     slog.Default(),
	}

	for _, c := range nc.Webhooks {
		s, err := n.newWebhookSender(c, deps)
		if err != nil {
			return nil, err
		}
		n.senders = append(n.senders, s)
	}

	for _, c := range nc.Emails {
		senders, err := n.newEmailSenders(ctx, c, deps)
		if err != nil {
			return nil, err
		}
		n.senders = append(n.senders, senders...)
	}

	// Start the workers
	// Deliveries use their own context, so events queued when the application is stopping are still delivered
	workCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	n.cancel = cancel
	for _, s := range n.senders {
		n.wg.Go(func() {
			n.run(workCtx, s)
		})
	}

	return n, nil
}

func newEventSet(events []config.NotificationEvent) map[config.NotificationEvent]struct{} {
	set := make(map[config.NotificationEvent]struct{}, len(events))
	for _, ev := range events {
		set[ev] = struct{}{}
	}
	return set
}

func (n *Notifier) newWebhookSender(c config.ConfigNotificationWebhook, deps dependencies) (*sender, error) {
	var key string
	if c.Token != "" {
		key = "Bearer " + c.Token
	}

	wh, err := deps.newWebhook(webhook.NewWebhookOpts{
		URL:             c.URL,
		Key:             key,
		Format:          webhook.WebhookFormat(c.Format),
		Timeout:         c.Timeout,
		MaxAttempts:     c.MaxAttempts,
		RetryInterval:   c.RetryInterval,
		AllowPrivateIPs: c.AllowPrivateDestinations,
		Logger:          slog.Default().With("notification", c.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize notifications.webhooks %q: %w", c.Name, err)
	}

	send := func(ctx context.Context, msg message) error {
		return wh.SendWebhook(ctx, msg)
	}
	return newSender("webhook", c.Name, c.Events, send), nil
}

// newEmailSenders returns one sender for each recipient, so the emails are sent in parallel and a failure for one recipient does not affect the others
func (n *Notifier) newEmailSenders(ctx context.Context, c config.ConfigNotificationEmail, deps dependencies) ([]*sender, error) {
	em, err := deps.newEmailer(ctx, emailer.NewEmailerOpts{
		ConnString: c.ConnectionString,
		Logger:     slog.Default().With("notification", c.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to initialize notifications.emails %q: %w", c.Name, err)
	}

	// The addresses were validated with the configuration
	senders := make([]*sender, 0, len(c.To))
	for _, to := range c.To {
		addr, err := mail.ParseAddress(to)
		if err != nil {
			return nil, fmt.Errorf("notifications.emails %q has an invalid recipient", c.Name)
		}

		recipient := emailer.EmailAddress{
			Name:    addr.Name,
			Address: addr.Address,
		}

		send := func(ctx context.Context, msg message) error {
			return em.SendEmail(ctx, recipient, msg.title, emailer.SendEmailMessage{
				Text: msg.body,
			})
		}
		senders = append(senders, newSender("email", c.Name, c.Events, send))
	}

	return senders, nil
}

func newSender(kind string, name string, events []config.NotificationEvent, send func(ctx context.Context, msg message) error) *sender {
	return &sender{
		kind:   kind,
		name:   name,
		events: newEventSet(events),
		queue:  make(chan Event, queueSize),
		send:   send,
	}
}

// Notify queues the event for delivery to all the destinations that subscribed to it
// It never blocks: if the queue of a destination is full, the event is dropped for that destination
func (n *Notifier) Notify(ev Event) {
	if n == nil {
		return
	}

	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}

	n.lock.RLock()
	defer n.lock.RUnlock()
	if n.closed {
		return
	}

	for _, s := range n.senders {
		_, ok := s.events[ev.Type]
		if !ok {
			continue
		}

		select {
		case s.queue <- ev:
		default:
			n.log.Warn("Notification queue is full, dropping event", "notification", s.name, "kind", s.kind, "event", ev.Type, "domain", ev.Domain)
		}
	}
}

// run delivers the events in the queue of the sender, until the queue is closed
func (n *Notifier) run(ctx context.Context, s *sender) {
	for ev := range s.queue {
		// After the context is canceled, the events still in the queue are dropped
		if ctx.Err() != nil {
			n.log.Warn("Dropping notification not delivered before shutdown", "notification", s.name, "kind", s.kind, "event", ev.Type, "domain", ev.Domain)
			continue
		}

		err := s.send(ctx, newMessage(ev))
		if err != nil {
			n.log.ErrorContext(ctx, "Failed to send notification", "notification", s.name, "kind", s.kind, "event", ev.Type, "domain", ev.Domain, "error", err)
			continue
		}

		n.log.DebugContext(ctx, "Notification sent", "notification", s.name, "kind", s.kind, "event", ev.Type, "domain", ev.Domain)
	}
}

// Shutdown stops accepting events and waits for the queued notifications to be delivered
// If ctx is canceled first, in-flight deliveries are aborted and the events still in the queue are dropped
func (n *Notifier) Shutdown(ctx context.Context) error {
	if n == nil {
		return nil
	}

	n.lock.Lock()
	if !n.closed {
		n.closed = true
		for _, s := range n.senders {
			close(s.queue)
		}
	}
	n.lock.Unlock()

	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		n.cancel()
		return nil
	case <-ctx.Done():
		// Fallthrough
	}

	// Out of time: abort what is in flight, and give it a moment to stop
	n.cancel()
	grace := time.NewTimer(cancelGracePeriod)
	defer grace.Stop()
	select {
	case <-done:
		// Fallthrough
	case <-grace.C:
		// Fallthrough
	}

	return fmt.Errorf("notifications were not all delivered before shutdown: %w", ctx.Err())
}
