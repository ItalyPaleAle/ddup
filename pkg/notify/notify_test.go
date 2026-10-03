package notify

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "sigs.k8s.io/yaml/goyaml.v3"

	"github.com/italypaleale/ddup/pkg/config"
)

func TestNotifier_FilteringTemplatesAndRetry(t *testing.T) {
	var calls atomic.Int32
	type got struct{ body, header, ctype string }
	ch := make(chan got, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		// Fail the first call to exercise retries
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ch <- got{string(b), r.Header.Get("Title"), r.Header.Get("Content-Type")}
	}))
	defer srv.Close()

	hooks := []config.ConfigWebhook{
		{Name: "tmpl", URL: config.SecretString(srv.URL), Method: "POST", Events: []string{EventDNSUpdated}, Attempts: 3, Timeout: time.Second,
			Body: "{{ .Domain }} -> {{ join .Healthy \",\" }}", Headers: map[string]config.SecretString{"Title": "{{ .Subject }}"}},
		{Name: "other", URL: config.SecretString(srv.URL), Method: "POST", Events: []string{EventAllUnhealthy}, Attempts: 1, Timeout: time.Second},
	}
	n, err := New(hooks)
	require.NoError(t, err)
	n.baseBackoff = time.Millisecond

	n.Notify(t.Context(), Event{Type: EventDNSUpdated, Domain: "a.example.com", Healthy: []string{"1.1.1.1", "2.2.2.2"}})
	n.Wait(5 * time.Second)

	select {
	case g := <-ch:
		assert.Equal(t, "a.example.com -> 1.1.1.1,2.2.2.2", g.body)
		assert.Equal(t, "a.example.com now points to 1.1.1.1, 2.2.2.2", g.header)
		assert.Equal(t, "text/plain; charset=utf-8", g.ctype)
	default:
		t.Fatal("expected a delivery")
	}
	assert.EqualValues(t, 2, calls.Load(), "one failure, one retry, and the non-subscribed hook is skipped")
}

func TestNotifier_DefaultJSONAndNil(t *testing.T) {
	var nilNotifier *Notifier
	nilNotifier.Notify(t.Context(), Event{})
	nilNotifier.Wait(time.Millisecond)

	ch := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- string(b)
	}))
	defer srv.Close()

	n, err := New([]config.ConfigWebhook{{Name: "j", URL: config.SecretString(srv.URL), Method: "POST", Attempts: 1, Timeout: time.Second}})
	require.NoError(t, err)
	n.Notify(t.Context(), Event{Type: EventAllUnhealthy, Domain: "a.example.com"})
	n.Wait(5 * time.Second)
	assert.Contains(t, <-ch, `"event":"all_unhealthy"`)
}

func TestSampleEmailWebhook(t *testing.T) {
	// Load config.sample.yaml, ensure it validates, then render its email body template and ensure it produces valid JSON, even with quotes in the data
	raw, err := os.ReadFile("../../config.sample.yaml")
	require.NoError(t, err)
	cfg := config.GetDefaultConfig()
	require.NoError(t, yaml.Unmarshal(raw, cfg))
	require.NoError(t, cfg.Validate(slog.Default()), "config.sample.yaml must validate")

	var emailIdx = -1
	for i := range cfg.Webhooks {
		if cfg.Webhooks[i].Name == "email" {
			emailIdx = i
		}
	}
	require.GreaterOrEqual(t, emailIdx, 0, "sample config must include an email webhook")

	n, err := New(cfg.Webhooks)
	require.NoError(t, err)
	body, ctype, headers, err := n.hooks[emailIdx].render(Event{
		Type: EventAllUnhealthy, Domain: "a.example.com", Time: time.Unix(0, 0).UTC(),
		Previous:  []string{"1.1.1.1"},
		Endpoints: []EndpointState{{Name: `say "hi"`, IP: "1.1.1.1", Error: `status "500"`}},
	})
	require.NoError(t, err)
	assert.Equal(t, "application/json", headers["Content-Type"])
	_ = ctype

	var parsed struct {
		Subject string `json:"subject"`
		Text    string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed), string(body))
	assert.Equal(t, "[ddup] No healthy endpoints for a.example.com", parsed.Subject)
	assert.Contains(t, parsed.Text, `say "hi" (1.1.1.1): UNHEALTHY (status "500")`)
}

func TestNew_DryRunCatchesTemplateErrors(t *testing.T) {
	_, err := New([]config.ConfigWebhook{{Name: "w", URL: "https://x.example.com", Body: `{{ env "DDUP_TEST_UNSET_VAR" }}`}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DDUP_TEST_UNSET_VAR is not set")

	_, err = New([]config.ConfigWebhook{{Name: "w", URL: "https://x.example.com", Body: `{{ .NoSuchField }}`}})
	require.Error(t, err)

	t.Setenv("DDUP_TEST_SET_VAR", "ops@example.com")
	n, err := New([]config.ConfigWebhook{{Name: "w", URL: "https://x.example.com", Body: `to={{ env "DDUP_TEST_SET_VAR" }}`}})
	require.NoError(t, err)
	body, _, _, err := n.hooks[0].render(Event{})
	require.NoError(t, err)
	assert.Equal(t, "to=ops@example.com", string(body))
}
