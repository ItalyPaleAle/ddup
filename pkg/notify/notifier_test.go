package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	configkit "github.com/italypaleale/go-kit/config"
	"github.com/italypaleale/go-kit/emailer"
	"github.com/italypaleale/go-kit/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

// Secrets used in the tests, which must never appear in logs
const (
	testNtfyToken = "tk_example_token_value"
	testTopic     = "secret-topic-name"
	testHookToken = "hook-secret-token-value"
	testResendKey = "re_example_api_key_value"
	testCFToken   = "cf-example-token-value"
)

// receivedRequest is a request received by a test server
type receivedRequest struct {
	Method string
	Path   string
	Header http.Header
	Body   string
}

// testServer is a HTTP server that records the requests it receives
type testServer struct {
	*httptest.Server

	lock     sync.Mutex
	requests []receivedRequest
	// Returns the status code for the nth request (starting from 1)
	status func(n int) int
	// If set, the handler waits until it's closed
	block chan struct{}
}

func newTestServer(t *testing.T, status func(n int) int) *testServer {
	t.Helper()

	ts := &testServer{
		status: status,
	}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		ts.lock.Lock()
		ts.requests = append(ts.requests, receivedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Header: r.Header.Clone(),
			Body:   string(body),
		})
		n := len(ts.requests)
		block := ts.block
		ts.lock.Unlock()

		if block != nil {
			select {
			case <-block:
			case <-r.Context().Done():
				return
			}
		}

		code := http.StatusOK
		if ts.status != nil {
			code = ts.status(n)
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *testServer) Requests() []receivedRequest {
	ts.lock.Lock()
	defer ts.lock.Unlock()
	return append([]receivedRequest(nil), ts.requests...)
}

func (ts *testServer) waitForRequests(t *testing.T, n int) []receivedRequest {
	t.Helper()
	require.Eventually(t, func() bool {
		return len(ts.Requests()) >= n
	}, 5*time.Second, 5*time.Millisecond, "expected %d requests", n)
	return ts.Requests()
}

// syncBuffer is a bytes.Buffer safe for concurrent use
type syncBuffer struct {
	lock sync.Mutex
	buf  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.Write(p) //nolint:wrapcheck
}

func (b *syncBuffer) String() string {
	b.lock.Lock()
	defer b.lock.Unlock()
	return b.buf.String()
}

// captureLogs replaces the default logger for the duration of the test
// Tests that use it must not run in parallel
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()

	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})
	return buf
}

// newTestConfig returns a validated configuration with the notifications
func newTestConfig(t *testing.T, nc config.ConfigNotifications) *config.Config {
	t.Helper()

	cfg := &config.Config{
		Providers: map[string]config.ConfigProvider{
			"test": {
				Cloudflare: &config.CloudflareConfig{
					APIToken: testCFToken,
					ZoneID:   "zone-id-value",
				},
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
		Notifications: nc,
	}
	err := cfg.Validate(slog.Default())
	require.NoError(t, err)
	return cfg
}

// fastDelivery are delivery settings for tests that allow loopback addresses and don't wait between retries
func fastDelivery() config.ConfigNotificationDelivery {
	return config.ConfigNotificationDelivery{
		Timeout:                  5 * time.Second,
		MaxAttempts:              3,
		RetryInterval:            10 * time.Millisecond,
		AllowPrivateDestinations: true,
	}
}

// shutdownNotifier stops the notifier and waits for the deliveries to end
func shutdownNotifier(t *testing.T, n *Notifier) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := n.Shutdown(ctx)
	require.NoError(t, err)
}

func newTestNotifier(t *testing.T, nc config.ConfigNotifications, deps dependencies) *Notifier {
	t.Helper()

	n, err := newNotifier(t.Context(), newTestConfig(t, nc), deps)
	require.NoError(t, err)
	require.NotNil(t, n)
	return n
}

func realDeps() dependencies {
	return dependencies{
		newWebhook: webhook.NewWebhook,
		newEmailer: newEmailer,
	}
}

func testEvent(typ config.NotificationEvent) Event {
	return Event{
		Type:      typ,
		Domain:    "example.com",
		Time:      testTime,
		Endpoints: testEndpoints(),
		Published: []string{"192.0.2.10", "192.0.2.11"},
		Previous:  []string{"192.0.2.12"},
	}
}

func TestNtfyDelivery(t *testing.T) {
	ts := newTestServer(t, nil)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				Token:                      testNtfyToken,
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
	}, realDeps())

	n.Notify(testEvent(config.EventDNSUpdated))
	failed := testEvent(config.EventDNSUpdateFailed)
	failed.Error = "provider returned status 500"
	n.Notify(failed)
	n.Notify(testEvent(config.EventAllUnhealthy))
	shutdownNotifier(t, n)

	reqs := ts.Requests()
	require.Len(t, reqs, 3)

	for _, req := range reqs {
		assert.Equal(t, http.MethodPost, req.Method)
		assert.Equal(t, "/ddup", req.Path)
		assert.Equal(t, "Bearer "+testNtfyToken, req.Header.Get("Authorization"))
		assert.Equal(t, "text/plain; charset=utf-8", req.Header.Get("Content-Type"))
	}

	// Updated
	assert.Equal(t, "example.com now points to 192.0.2.10, 192.0.2.11", reqs[0].Header.Get("Title"))
	assert.Empty(t, reqs[0].Header.Get("Priority"))
	assert.Equal(t, "yellow_circle", reqs[0].Header.Get("Tags"))
	assert.Equal(t, newMessage(testEvent(config.EventDNSUpdated)).body, reqs[0].Body)

	// Failed
	assert.Equal(t, "DNS update failed for example.com", reqs[1].Header.Get("Title"))
	assert.Equal(t, "4", reqs[1].Header.Get("Priority"))
	assert.Equal(t, "red_circle,warning", reqs[1].Header.Get("Tags"))
	assert.Contains(t, reqs[1].Body, "Error: provider returned status 500\n")

	// All unhealthy
	assert.Equal(t, "No healthy endpoints for example.com", reqs[2].Header.Get("Title"))
	assert.Equal(t, "5", reqs[2].Header.Get("Priority"))
	assert.Equal(t, "red_circle,warning", reqs[2].Header.Get("Tags"))
	assert.Contains(t, reqs[2].Body, "Event: all_unhealthy\n")
}

func TestNtfyWithoutToken(t *testing.T) {
	ts := newTestServer(t, nil)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
	}, realDeps())

	n.Notify(testEvent(config.EventDNSUpdated))
	shutdownNotifier(t, n)

	reqs := ts.Requests()
	require.Len(t, reqs, 1)
	assert.Empty(t, reqs[0].Header.Get("Authorization"))
}

func TestWebhookFormats(t *testing.T) {
	msg := newMessage(testEvent(config.EventDNSUpdated))

	tests := []struct {
		format      string
		path        string
		expectPath  string
		contentType string
		checkBody   func(t *testing.T, body string)
	}{
		{
			format:      "plain",
			path:        "/hooks/plain",
			expectPath:  "/hooks/plain",
			contentType: "text/plain",
			checkBody: func(t *testing.T, body string) {
				t.Helper()
				assert.Equal(t, msg.title+"\n\n"+msg.body, body)
			},
		},
		{
			format:      "slack",
			path:        "/services/T000/B000/" + testHookToken,
			expectPath:  "/services/T000/B000/" + testHookToken,
			contentType: "application/json",
			checkBody: func(t *testing.T, body string) {
				t.Helper()
				var parsed map[string]string
				err := json.Unmarshal([]byte(body), &parsed)
				require.NoError(t, err)
				assert.Equal(t, map[string]string{"text": msg.title + "\n\n" + msg.body}, parsed)
			},
		},
		{
			// Discord webhooks are sent to the Slack-compatible endpoint
			format:      "discord",
			path:        "/api/webhooks/123/" + testHookToken,
			expectPath:  "/api/webhooks/123/" + testHookToken + "/slack",
			contentType: "application/json",
			checkBody: func(t *testing.T, body string) {
				t.Helper()
				var parsed map[string]string
				err := json.Unmarshal([]byte(body), &parsed)
				require.NoError(t, err)
				assert.Equal(t, msg.title+"\n\n"+msg.body, parsed["text"])
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			ts := newTestServer(t, nil)

			n := newTestNotifier(t, config.ConfigNotifications{
				Webhooks: []config.ConfigNotificationWebhook{
					{
						Name:                       "chat",
						Format:                     tc.format,
						URL:                        ts.URL + tc.path,
						ConfigNotificationDelivery: fastDelivery(),
					},
				},
			}, realDeps())

			n.Notify(testEvent(config.EventDNSUpdated))
			shutdownNotifier(t, n)

			reqs := ts.Requests()
			require.Len(t, reqs, 1)
			assert.Equal(t, http.MethodPost, reqs[0].Method)
			assert.Equal(t, tc.expectPath, reqs[0].Path)
			assert.Equal(t, tc.contentType, reqs[0].Header.Get("Content-Type"))
			assert.Empty(t, reqs[0].Header.Get("Authorization"))
			tc.checkBody(t, reqs[0].Body)
		})
	}
}

func TestEventFilter(t *testing.T) {
	ts := newTestServer(t, nil)
	other := newTestServer(t, nil)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "only-failures",
				URL:                        ts.URL + "/ddup",
				Events:                     []config.NotificationEvent{config.EventDNSUpdateFailed, config.EventAllUnhealthy},
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
		Webhooks: []config.ConfigNotificationWebhook{
			{
				Name:                       "all",
				URL:                        other.URL + "/hook",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
	}, realDeps())

	n.Notify(testEvent(config.EventDNSUpdated))
	n.Notify(testEvent(config.EventDNSUpdateFailed))
	n.Notify(testEvent(config.EventAllUnhealthy))
	shutdownNotifier(t, n)

	reqs := ts.Requests()
	require.Len(t, reqs, 2)
	assert.Equal(t, "DNS update failed for example.com", reqs[0].Header.Get("Title"))
	assert.Equal(t, "No healthy endpoints for example.com", reqs[1].Header.Get("Title"))
	assert.Len(t, other.Requests(), 3)
}

func TestRetries(t *testing.T) {
	// Fails twice, then succeeds
	ts := newTestServer(t, func(n int) int {
		if n <= 2 {
			return http.StatusServiceUnavailable
		}
		return http.StatusOK
	})
	logs := captureLogs(t)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
	}, realDeps())

	n.Notify(testEvent(config.EventDNSUpdated))
	shutdownNotifier(t, n)

	assert.Len(t, ts.Requests(), 3)
	assert.NotContains(t, logs.String(), "Failed to send notification")
}

func TestRetriesGiveUp(t *testing.T) {
	ts := newTestServer(t, func(n int) int {
		return http.StatusInternalServerError
	})
	logs := captureLogs(t)

	delivery := fastDelivery()
	delivery.MaxAttempts = 2
	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				ConfigNotificationDelivery: delivery,
			},
		},
	}, realDeps())

	n.Notify(testEvent(config.EventDNSUpdated))
	shutdownNotifier(t, n)

	assert.Len(t, ts.Requests(), 2)
	out := logs.String()
	assert.Contains(t, out, "Failed to send notification")
	assert.Contains(t, out, "notification=alerts")
	assert.Contains(t, out, "invalid response status code: 500")
}

func TestPrivateDestinations(t *testing.T) {
	t.Run("blocked by default", func(t *testing.T) {
		ts := newTestServer(t, nil)
		logs := captureLogs(t)

		delivery := fastDelivery()
		delivery.AllowPrivateDestinations = false
		delivery.MaxAttempts = 1
		n := newTestNotifier(t, config.ConfigNotifications{
			Ntfy: []config.ConfigNotificationNtfy{
				{
					Name:                       "alerts",
					URL:                        ts.URL + "/ddup",
					ConfigNotificationDelivery: delivery,
				},
			},
			Webhooks: []config.ConfigNotificationWebhook{
				{
					Name:                       "chat",
					URL:                        ts.URL + "/hook",
					ConfigNotificationDelivery: delivery,
				},
			},
		}, realDeps())

		n.Notify(testEvent(config.EventDNSUpdated))
		shutdownNotifier(t, n)

		assert.Empty(t, ts.Requests(), "the receiver is on a loopback address and must not be reached")
		out := logs.String()
		assert.Contains(t, out, "notification=alerts")
		assert.Contains(t, out, "notification=chat")
		assert.Equal(t, 2, strings.Count(out, "Failed to send notification"))
	})

	t.Run("allowed with the option", func(t *testing.T) {
		ts := newTestServer(t, nil)

		n := newTestNotifier(t, config.ConfigNotifications{
			Ntfy: []config.ConfigNotificationNtfy{
				{
					Name:                       "alerts",
					URL:                        ts.URL + "/ddup",
					ConfigNotificationDelivery: fastDelivery(),
				},
			},
		}, realDeps())

		n.Notify(testEvent(config.EventDNSUpdated))
		shutdownNotifier(t, n)

		assert.Len(t, ts.Requests(), 1)
	})
}

// fakeEmailer records the emails it is asked to send
type fakeEmailer struct {
	lock  sync.Mutex
	sent  []sentEmail
	fail  map[string]error
	calls int
}

type sentEmail struct {
	To      emailer.EmailAddress
	Subject string
	Message emailer.SendEmailMessage
}

func (f *fakeEmailer) SendEmail(ctx context.Context, to emailer.EmailAddress, subject string, message emailer.SendEmailMessage) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	f.calls++
	err := f.fail[to.Address]
	if err != nil {
		return err
	}
	f.sent = append(f.sent, sentEmail{To: to, Subject: subject, Message: message})
	return nil
}

func (f *fakeEmailer) Sent() []sentEmail {
	f.lock.Lock()
	defer f.lock.Unlock()
	return append([]sentEmail(nil), f.sent...)
}

// fakeEmailerDeps returns the dependencies where the emailer is a fake, and the connection strings it was created with
func fakeEmailerDeps(fake *fakeEmailer, connStrings *[]string) dependencies {
	deps := realDeps()
	deps.newEmailer = func(ctx context.Context, opts emailer.NewEmailerOpts) (emailSender, error) {
		if connStrings != nil {
			*connStrings = append(*connStrings, opts.ConnString)
		}
		return fake, nil
	}
	return deps
}

func TestEmailDelivery(t *testing.T) {
	fake := &fakeEmailer{}
	var connStrings []string

	n := newTestNotifier(t, config.ConfigNotifications{
		Email: []config.ConfigNotificationEmail{
			{
				Name:             "ops",
				ConnectionString: "resend://" + testResendKey + "?fromAddress=ddup@example.com",
				To:               []string{"postmaster@example.com", "Ops Team <ops@example.com>"},
				Events:           []config.NotificationEvent{config.EventDNSUpdated, config.EventAllUnhealthy},
			},
		},
	}, fakeEmailerDeps(fake, &connStrings))

	n.Notify(testEvent(config.EventDNSUpdated))
	n.Notify(testEvent(config.EventDNSUpdateFailed))
	n.Notify(testEvent(config.EventAllUnhealthy))
	shutdownNotifier(t, n)

	assert.Equal(t, []string{"resend://" + testResendKey + "?fromAddress=ddup@example.com"}, connStrings)

	// One email per recipient and per event, and the filtered event was not sent
	sent := fake.Sent()
	require.Len(t, sent, 4)

	updated := newMessage(testEvent(config.EventDNSUpdated))
	assert.Equal(t, emailer.EmailAddress{Address: "postmaster@example.com"}, sent[0].To)
	assert.Equal(t, updated.title, sent[0].Subject)
	assert.Equal(t, updated.body, sent[0].Message.Text)
	assert.Empty(t, sent[0].Message.HTML)
	assert.Equal(t, emailer.EmailAddress{Name: "Ops Team", Address: "ops@example.com"}, sent[1].To)
	assert.Equal(t, updated.title, sent[1].Subject)

	unhealthy := newMessage(testEvent(config.EventAllUnhealthy))
	assert.Equal(t, "postmaster@example.com", sent[2].To.Address)
	assert.Equal(t, unhealthy.title, sent[2].Subject)
	assert.Equal(t, unhealthy.body, sent[2].Message.Text)
	assert.Equal(t, "ops@example.com", sent[3].To.Address)
}

func TestEmailFailureForOneRecipient(t *testing.T) {
	fake := &fakeEmailer{
		fail: map[string]error{
			"first@example.com": errors.New("mailbox unavailable"),
		},
	}
	logs := captureLogs(t)

	n := newTestNotifier(t, config.ConfigNotifications{
		Email: []config.ConfigNotificationEmail{
			{
				Name:             "ops",
				ConnectionString: "resend://" + testResendKey + "?fromAddress=ddup@example.com",
				To:               []string{"first@example.com", "second@example.com"},
			},
		},
	}, fakeEmailerDeps(fake, nil))

	n.Notify(testEvent(config.EventDNSUpdated))
	shutdownNotifier(t, n)

	// The second recipient is notified even if the first failed
	sent := fake.Sent()
	require.Len(t, sent, 1)
	assert.Equal(t, "second@example.com", sent[0].To.Address)
	out := logs.String()
	assert.Contains(t, out, "Failed to send notification")
	assert.Contains(t, out, "mailbox unavailable")
	assert.Contains(t, out, "notification=ops")
}

func TestInitErrorsDoNotIncludeSecrets(t *testing.T) {
	t.Run("emailer", func(t *testing.T) {
		deps := realDeps()
		deps.newEmailer = func(ctx context.Context, opts emailer.NewEmailerOpts) (emailSender, error) {
			return nil, errors.New("emailer connection string is invalid: parse \"" + opts.ConnString + "\": bad")
		}

		cfg := newTestConfig(t, config.ConfigNotifications{
			Email: []config.ConfigNotificationEmail{
				{
					Name:             "ops",
					ConnectionString: "resend://" + testResendKey + "?fromAddress=ddup@example.com",
					To:               []string{"postmaster@example.com"},
				},
			},
		})
		_, err := newNotifier(t.Context(), cfg, deps)
		require.ErrorContains(t, err, `failed to initialize notifications.email "ops"`)
		assert.NotContains(t, err.Error(), testResendKey)
	})

	t.Run("real emailer with a bad connection string", func(t *testing.T) {
		cfg := newTestConfig(t, config.ConfigNotifications{
			Email: []config.ConfigNotificationEmail{
				{
					Name:             "ops",
					ConnectionString: "resend://" + testResendKey + "?fromAddress=not-an-address",
					To:               []string{"postmaster@example.com"},
				},
			},
		})
		_, err := newNotifier(t.Context(), cfg, realDeps())
		require.ErrorContains(t, err, `failed to initialize notifications.email "ops"`)
		assert.NotContains(t, err.Error(), testResendKey)
	})

	t.Run("unparsable connection string", func(t *testing.T) {
		cfg := newTestConfig(t, config.ConfigNotifications{
			Email: []config.ConfigNotificationEmail{
				{
					Name:             "ops",
					ConnectionString: "smtp://user:" + testResendKey + "@bad host:25",
					To:               []string{"postmaster@example.com"},
				},
			},
		})
		_, err := newNotifier(t.Context(), cfg, realDeps())
		require.ErrorContains(t, err, `failed to initialize notifications.email "ops"`)
		assert.NotContains(t, err.Error(), testResendKey)
	})

	t.Run("unknown email provider", func(t *testing.T) {
		cfg := newTestConfig(t, config.ConfigNotifications{
			Email: []config.ConfigNotificationEmail{
				{
					Name:             "ops",
					ConnectionString: "mailgun://" + testResendKey,
					To:               []string{"postmaster@example.com"},
				},
			},
		})
		_, err := newNotifier(t.Context(), cfg, realDeps())
		require.ErrorContains(t, err, "invalid email sender type")
		assert.NotContains(t, err.Error(), testResendKey)
	})
}

func TestNoNotificationsConfigured(t *testing.T) {
	before := runtime.NumGoroutine()

	cfg := newTestConfig(t, config.ConfigNotifications{})
	n, err := New(t.Context(), cfg)
	require.NoError(t, err)
	assert.Nil(t, n)

	// A nil notifier is valid
	n.Notify(testEvent(config.EventDNSUpdated))
	err = n.Shutdown(t.Context())
	require.NoError(t, err)

	assert.LessOrEqual(t, runtime.NumGoroutine(), before)
}

func TestSecretsNotInLogs(t *testing.T) {
	logs := captureLogs(t)

	// Server that is closed, so connections fail and the library logs the errors, which include the URL
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	delivery := fastDelivery()
	delivery.MaxAttempts = 2

	fake := &fakeEmailer{
		fail: map[string]error{
			"postmaster@example.com": errors.New("failed to send request: Post \"https://api.example.com/emails?key=" + testResendKey + "\": Bearer " + testResendKey + " rejected"),
		},
	}
	deps := fakeEmailerDeps(fake, nil)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        closedURL + "/" + testTopic,
				Token:                      testNtfyToken,
				ConfigNotificationDelivery: delivery,
			},
		},
		Webhooks: []config.ConfigNotificationWebhook{
			{
				Name:                       "slack",
				Format:                     "slack",
				URL:                        closedURL + "/services/T000/B000/" + testHookToken,
				ConfigNotificationDelivery: delivery,
			},
			{
				Name:                       "discord",
				Format:                     "discord",
				URL:                        closedURL + "/api/webhooks/123/" + testHookToken,
				ConfigNotificationDelivery: delivery,
			},
		},
		Email: []config.ConfigNotificationEmail{
			{
				Name:             "ops",
				ConnectionString: "resend://" + testResendKey + "?fromAddress=ddup@example.com",
				To:               []string{"postmaster@example.com"},
			},
		},
	}, deps)

	// The error of the DNS provider can include its token
	ev := testEvent(config.EventDNSUpdateFailed)
	ev.Error = "cloudflare: request failed with token " + testCFToken + " and Authorization: Bearer abc.def-123"
	n.Notify(ev)
	shutdownNotifier(t, n)

	out := logs.String()
	require.Contains(t, out, "Failed to send notification")
	for _, name := range []string{"alerts", "slack", "discord", "ops"} {
		assert.Contains(t, out, "notification="+name)
	}
	for _, secret := range []string{testNtfyToken, testTopic, testHookToken, testResendKey, testCFToken, "abc.def-123"} {
		assert.NotContains(t, out, secret)
	}

	// The events sent do not include the DNS provider token either
	sent := fake.Sent()
	assert.Empty(t, sent)
}

func TestEventErrorIsRedacted(t *testing.T) {
	ts := newTestServer(t, nil)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
	}, realDeps())

	ev := testEvent(config.EventDNSUpdateFailed)
	ev.Error = "request to https://api.example.com/zones?token=" + testCFToken + " failed: " + testCFToken
	n.Notify(ev)
	shutdownNotifier(t, n)

	reqs := ts.Requests()
	require.Len(t, reqs, 1)
	assert.NotContains(t, reqs[0].Body, testCFToken)
	assert.Contains(t, reqs[0].Body, "Error: request to https://api.example.com/zones?token=[redacted] failed: [redacted]")
}

func TestSlowReceiverDoesNotBlock(t *testing.T) {
	ts := newTestServer(t, nil)
	ts.block = make(chan struct{})

	delivery := fastDelivery()
	delivery.MaxAttempts = 1
	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				ConfigNotificationDelivery: delivery,
			},
		},
	}, realDeps())

	// More events than the queue holds: Notify must neither block nor fail
	start := time.Now()
	for range queueSize * 3 {
		n.Notify(testEvent(config.EventDNSUpdated))
	}
	assert.Less(t, time.Since(start), time.Second)

	// The first event is being delivered, and the receiver is stuck
	ts.waitForRequests(t, 1)

	// Shutting down with an expired context aborts the delivery
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start = time.Now()
	err := n.Shutdown(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
	close(ts.block)
}

func TestShutdownDrainsQueue(t *testing.T) {
	// Each request takes a little while
	var count int
	var lock sync.Mutex
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		lock.Lock()
		count++
		lock.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(slow.Close)

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        slow.URL + "/ddup",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
	}, realDeps())

	for range 5 {
		n.Notify(testEvent(config.EventDNSUpdated))
	}
	shutdownNotifier(t, n)

	lock.Lock()
	defer lock.Unlock()
	assert.Equal(t, 5, count, "all queued notifications must be delivered before Shutdown returns")

	// Events after shutdown are dropped without panicking, and Shutdown can be called again
	n.Notify(testEvent(config.EventDNSUpdated))
	err := n.Shutdown(t.Context())
	require.NoError(t, err)
}

func TestNoGoroutineLeaks(t *testing.T) {
	ts := newTestServer(t, nil)
	fake := &fakeEmailer{}

	n := newTestNotifier(t, config.ConfigNotifications{
		Ntfy: []config.ConfigNotificationNtfy{
			{
				Name:                       "alerts",
				URL:                        ts.URL + "/ddup",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
		Webhooks: []config.ConfigNotificationWebhook{
			{
				Name:                       "chat",
				Format:                     "slack",
				URL:                        ts.URL + "/hook",
				ConfigNotificationDelivery: fastDelivery(),
			},
		},
		Email: []config.ConfigNotificationEmail{
			{
				Name:             "ops",
				ConnectionString: "console://",
				To:               []string{"postmaster@example.com"},
			},
		},
	}, fakeEmailerDeps(fake, nil))

	// One goroutine per destination
	require.Eventually(t, func() bool {
		return strings.Count(goroutineStacks(), "(*Notifier).run") == 3
	}, 5*time.Second, 5*time.Millisecond, "the goroutines of the notifier must have started")

	n.Notify(testEvent(config.EventDNSUpdated))
	shutdownNotifier(t, n)

	require.Eventually(t, func() bool {
		stacks := goroutineStacks()
		return !strings.Contains(stacks, "(*Notifier).run") &&
			!strings.Contains(stacks, "newNotifier.func") &&
			!strings.Contains(stacks, "(*Notifier).Shutdown.func")
	}, 5*time.Second, 10*time.Millisecond, "the goroutines of the notifier must have ended")
}

func goroutineStacks() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

// The notifications in the sample config must be accepted at startup
func TestSampleConfigNotifications(t *testing.T) {
	t.Setenv("DDUP_CONFIG", filepath.Join("..", "..", "config.sample.yaml"))

	cfg := config.GetDefaultConfig()
	err := configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	require.NoError(t, err)
	err = cfg.Validate(slog.Default())
	require.NoError(t, err)

	n, err := New(t.Context(), cfg)
	require.NoError(t, err)
	require.NotNil(t, n)
	assert.Len(t, n.senders, 3)
	shutdownNotifier(t, n)
}
