package notify

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
		{Name: "tmpl", URL: srv.URL, Method: "POST", Events: []string{EventDNSUpdated}, Attempts: 3, Timeout: time.Second,
			Body: "{{ .Domain }} -> {{ join .Healthy \",\" }}", Headers: map[string]string{"Title": "{{ .Subject }}"}},
		{Name: "other", URL: srv.URL, Method: "POST", Events: []string{EventAllUnhealthy}, Attempts: 1, Timeout: time.Second},
	}
	n, err := New(t.Context(), hooks)
	require.NoError(t, err)
	n.baseBackoff = time.Millisecond

	n.Notify(Event{Type: EventDNSUpdated, Domain: "a.example.com", Healthy: []string{"1.1.1.1", "2.2.2.2"}})
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
	nilNotifier.Notify(Event{})
	nilNotifier.Wait(time.Millisecond)

	ch := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- string(b)
	}))
	defer srv.Close()

	n, err := New(t.Context(), []config.ConfigWebhook{{Name: "j", URL: srv.URL, Method: "POST", Attempts: 1, Timeout: time.Second}})
	require.NoError(t, err)
	n.Notify(Event{Type: EventAllUnhealthy, Domain: "a.example.com"})
	n.Wait(5 * time.Second)
	assert.Contains(t, <-ch, `"event":"all_unhealthy"`)
}
