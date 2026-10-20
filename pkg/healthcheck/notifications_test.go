package healthcheck

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
	"github.com/italypaleale/ddup/pkg/notify"
)

// recordingNotifier records the events it receives
type recordingNotifier struct {
	lock   sync.Mutex
	events []notify.Event
}

func (r *recordingNotifier) Notify(ev notify.Event) {
	r.lock.Lock()
	defer r.lock.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingNotifier) Events() []notify.Event {
	r.lock.Lock()
	defer r.lock.Unlock()
	return append([]notify.Event(nil), r.events...)
}

func (r *recordingNotifier) Types() []config.NotificationEvent {
	events := r.Events()
	types := make([]config.NotificationEvent, len(events))
	for i, ev := range events {
		types[i] = ev.Type
	}
	return types
}

// newNotifyTestChecker returns a health checker for example.com with two endpoints, and the objects to control it
func newNotifyTestChecker(n Notifier) (hc *HealthChecker, mc *checker.MockChecker, provider *dns.MockProvider, endpoints []*config.ConfigEndpoint) {
	provider = dns.NewMockProvider(false)
	endpoints = []*config.ConfigEndpoint{
		{Name: "server1", IP: "192.0.2.10"},
		{Name: "server2", IP: "192.0.2.11"},
	}
	mc = &checker.MockChecker{
		Domain:      "example.com",
		MaxAttempts: 1,
	}
	hc = &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:   mc,
				ttl:       60,
				failedIPs: make(map[string]int),
				provider:  provider,
			},
		},
		notifier: n,
	}
	return hc, mc, provider, endpoints
}

func setHealth(mc *checker.MockChecker, endpoints []*config.ConfigEndpoint, healthy ...bool) {
	mc.Results = make([]checker.Result, len(endpoints))
	for i, ep := range endpoints {
		mc.Results[i] = checker.Result{Endpoint: ep, Healthy: healthy[i]}
		if !healthy[i] {
			mc.Results[i].Error = errors.New("connection failed")
		}
	}
}

func TestNotifyDNSUpdated(t *testing.T) {
	rec := &recordingNotifier{}
	hc, mc, provider, endpoints := newNotifyTestChecker(rec)
	provider.Previous = []string{"192.0.2.9"}
	setHealth(mc, endpoints, true, false)

	hc.checkAndUpdateDNS(t.Context())

	events := rec.Events()
	require.Len(t, events, 1)
	ev := events[0]
	assert.Equal(t, config.EventDNSUpdated, ev.Type)
	assert.Equal(t, "example.com", ev.Domain)
	assert.Equal(t, []string{"192.0.2.10"}, ev.Published)
	assert.Equal(t, []string{"192.0.2.9"}, ev.Previous)
	assert.Empty(t, ev.Error)
	assert.Equal(t, []notify.EndpointState{
		{Name: "server1", IP: "192.0.2.10", Healthy: true},
		{Name: "server2", IP: "192.0.2.11", Healthy: false},
	}, ev.Endpoints)
}

func TestNotifyEndpointStates(t *testing.T) {
	fixed := &config.ConfigEndpoint{Name: "fixed", IP: "192.0.2.10"}
	found := &config.ConfigEndpoint{Name: "found", IPLookupFamily: 4}
	unresolved := &config.ConfigEndpoint{Name: "unresolved", IPLookupFamily: 4}

	results := []checker.Result{
		{Endpoint: fixed},
		{Endpoint: found, Target: "198.51.100.7"},
		{Endpoint: unresolved},
	}
	states := notifyEndpointStates(results, []string{"192.0.2.10", "198.51.100.7"})

	assert.Equal(t, []notify.EndpointState{
		{Name: "fixed", IP: "192.0.2.10", Healthy: true},
		{Name: "found", IP: "198.51.100.7", Healthy: true},
		{Name: "unresolved", IP: "", Healthy: false},
	}, states)
}

func TestNotifyNothingWhenDNSIsUnchanged(t *testing.T) {
	rec := &recordingNotifier{}
	hc, mc, provider, endpoints := newNotifyTestChecker(rec)
	setHealth(mc, endpoints, true, true)

	// The records already match, so the provider reports no change
	provider.NoChange = true
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 1, provider.CallCount)
	assert.Empty(t, rec.Events())

	// The published addresses did not change, so DNS is not even touched
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 1, provider.CallCount)
	assert.Empty(t, rec.Events())
}

func TestNotifyDNSUpdateFailed(t *testing.T) {
	rec := &recordingNotifier{}
	hc, mc, provider, endpoints := newNotifyTestChecker(rec)
	setHealth(mc, endpoints, true, true)
	provider.ShouldError = true

	hc.checkAndUpdateDNS(t.Context())

	events := rec.Events()
	require.Len(t, events, 1)
	assert.Equal(t, config.EventDNSUpdateFailed, events[0].Type)
	assert.Equal(t, "example.com", events[0].Domain)
	assert.Equal(t, "mock error", events[0].Error)
	assert.ElementsMatch(t, []string{"192.0.2.10", "192.0.2.11"}, events[0].Published)

	// The update is retried at every check, but the same error is not notified again
	hc.checkAndUpdateDNS(t.Context())
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, 3, provider.CallCount)
	assert.Len(t, rec.Events(), 1)

	// After the update succeeds, the domain is reported as updated
	provider.ShouldError = false
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []config.NotificationEvent{config.EventDNSUpdateFailed, config.EventDNSUpdated}, rec.Types())

	// And a new failure is notified again
	setHealth(mc, endpoints, true, false)
	provider.ShouldError = true
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []config.NotificationEvent{config.EventDNSUpdateFailed, config.EventDNSUpdated, config.EventDNSUpdateFailed}, rec.Types())
}

func TestNotifyAllUnhealthyOncePerOutage(t *testing.T) {
	rec := &recordingNotifier{}
	hc, mc, provider, endpoints := newNotifyTestChecker(rec)

	// Healthy
	setHealth(mc, endpoints, true, true)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []config.NotificationEvent{config.EventDNSUpdated}, rec.Types())

	// The outage is notified once, and DNS is not changed
	setHealth(mc, endpoints, false, false)
	hc.checkAndUpdateDNS(t.Context())
	require.Equal(t, []config.NotificationEvent{config.EventDNSUpdated, config.EventAllUnhealthy}, rec.Types())
	ev := rec.Events()[1]
	assert.Equal(t, "example.com", ev.Domain)
	assert.ElementsMatch(t, []string{"192.0.2.10", "192.0.2.11"}, ev.Previous)
	assert.Empty(t, ev.Published)
	assert.Equal(t, []notify.EndpointState{
		{Name: "server1", IP: "192.0.2.10"},
		{Name: "server2", IP: "192.0.2.11"},
	}, ev.Endpoints)

	hc.checkAndUpdateDNS(t.Context())
	hc.checkAndUpdateDNS(t.Context())
	assert.Len(t, rec.Events(), 2)
	assert.Equal(t, 1, provider.CallCount, "DNS is left unchanged during the outage")

	// The recovery is reported by dns_updated
	setHealth(mc, endpoints, false, true)
	hc.checkAndUpdateDNS(t.Context())
	require.Equal(t, []config.NotificationEvent{config.EventDNSUpdated, config.EventAllUnhealthy, config.EventDNSUpdated}, rec.Types())
	assert.Equal(t, []string{"192.0.2.11"}, rec.Events()[2].Published)

	// A new outage is notified again
	setHealth(mc, endpoints, false, false)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []config.NotificationEvent{config.EventDNSUpdated, config.EventAllUnhealthy, config.EventDNSUpdated, config.EventAllUnhealthy}, rec.Types())
}

func TestNotifyAllUnhealthyAtStartup(t *testing.T) {
	rec := &recordingNotifier{}
	hc, mc, provider, endpoints := newNotifyTestChecker(rec)
	setHealth(mc, endpoints, false, false)

	hc.checkAndUpdateDNS(t.Context())
	hc.checkAndUpdateDNS(t.Context())

	assert.Equal(t, []config.NotificationEvent{config.EventAllUnhealthy}, rec.Types())
	assert.Equal(t, 0, provider.CallCount)
}

func TestNotifyHonorsRetriesBeforeOutage(t *testing.T) {
	rec := &recordingNotifier{}
	hc, mc, _, endpoints := newNotifyTestChecker(rec)
	mc.MaxAttempts = 2

	setHealth(mc, endpoints, true, true)
	hc.checkAndUpdateDNS(t.Context())

	// The first failed check does not remove the endpoints, so it's not an outage
	setHealth(mc, endpoints, false, false)
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []config.NotificationEvent{config.EventDNSUpdated}, rec.Types())

	// The second one does
	hc.checkAndUpdateDNS(t.Context())
	assert.Equal(t, []config.NotificationEvent{config.EventDNSUpdated, config.EventAllUnhealthy}, rec.Types())
}

// Failures and slow destinations must not delay or fail a health check
func TestNotifyDeliveryDoesNotAffectChecks(t *testing.T) {
	block := make(chan struct{})
	var requests sync.WaitGroup
	requests.Add(1)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(requests.Done)
		select {
		case <-block:
		case <-r.Context().Done():
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	cfg := &config.Config{
		Providers: map[string]config.ConfigProvider{
			"test": {
				Cloudflare: &config.CloudflareConfig{},
			},
		},
		Domains: []config.ConfigDomain{
			{
				RecordName: "example.com",
				Provider:   "test",
				Endpoints: []*config.ConfigEndpoint{
					{URL: "https://example.com/health", IP: "192.0.2.10"},
				},
			},
		},
		Notifications: config.ConfigNotifications{
			Webhooks: []config.ConfigNotificationWebhook{
				{
					Format:                   "ntfy",
					Name:                     "alerts",
					URL:                      srv.URL + "/ddup",
					Timeout:                  10 * time.Second,
					MaxAttempts:              1,
					AllowPrivateDestinations: true,
				},
			},
		},
	}
	err := cfg.Validate(slog.Default())
	require.NoError(t, err)
	n, err := notify.New(t.Context(), cfg)
	require.NoError(t, err)
	require.NotNil(t, n)

	hc, mc, provider, endpoints := newNotifyTestChecker(n)
	setHealth(mc, endpoints, true, true)

	// Many events while the receiver hangs on the first one
	start := time.Now()
	for i := range 200 {
		healthy := i%2 == 0
		setHealth(mc, endpoints, healthy, healthy)
		hc.checkAndUpdateDNS(t.Context())
	}
	elapsed := time.Since(start)
	requests.Wait()

	assert.Less(t, elapsed, 2*time.Second, "checks must not wait for the notifications")
	assert.Positive(t, provider.CallCount, "DNS was updated")

	// Shutdown aborts the stuck delivery
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = n.Shutdown(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	close(block)
}
