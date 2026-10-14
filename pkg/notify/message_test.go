package notify

import (
	"testing"
	"time"

	"github.com/italypaleale/go-kit/webhook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

var testTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func testEndpoints() []EndpointState {
	return []EndpointState{
		{Name: "server1", IP: "192.0.2.10", Healthy: true},
		{Name: "server2", IP: "192.0.2.11", Healthy: true},
		{Name: "server3", IP: "192.0.2.12", Healthy: false},
	}
}

func TestNewMessage(t *testing.T) {
	tests := []struct {
		name         string
		event        Event
		expectTitle  string
		expectBody   string
		expectPrio   webhook.NtfyPriority
		expectedTags []string
	}{
		{
			name: "dns_updated",
			event: Event{
				Type:      config.EventDNSUpdated,
				Domain:    "example.com",
				Time:      testTime,
				Endpoints: testEndpoints(),
				Published: []string{"192.0.2.11", "192.0.2.10"},
				Previous:  []string{"192.0.2.12"},
			},
			expectTitle: "example.com now points to 192.0.2.10, 192.0.2.11",
			expectBody: `Event: dns_updated
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: warning
Now: 192.0.2.10, 192.0.2.11
Before: 192.0.2.12

Endpoints:
  server1 (192.0.2.10): healthy
  server2 (192.0.2.11): healthy
  server3 (192.0.2.12): UNHEALTHY
`,
			expectPrio:   webhook.NtfyPriorityDefault,
			expectedTags: []string{"yellow_circle"},
		},
		{
			name: "dns_updated with all endpoints healthy",
			event: Event{
				Type:   config.EventDNSUpdated,
				Domain: "example.com",
				Time:   testTime,
				Endpoints: []EndpointState{
					{Name: "server1", IP: "192.0.2.10", Healthy: true},
				},
				Published: []string{"192.0.2.10"},
			},
			expectTitle: "example.com now points to 192.0.2.10",
			expectBody: `Event: dns_updated
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: healthy
Now: 192.0.2.10

Endpoints:
  server1 (192.0.2.10): healthy
`,
			expectPrio:   webhook.NtfyPriorityDefault,
			expectedTags: []string{"green_circle"},
		},
		{
			name: "dns_update_failed",
			event: Event{
				Type:      config.EventDNSUpdateFailed,
				Domain:    "example.com",
				Time:      testTime.In(time.FixedZone("test", 3600)),
				Endpoints: testEndpoints(),
				Published: []string{"192.0.2.10", "192.0.2.11"},
				Previous:  []string{"192.0.2.12"},
				Error:     "provider returned status 500",
			},
			expectTitle: "DNS update failed for example.com",
			expectBody: `Event: dns_update_failed
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: unhealthy
Wanted: 192.0.2.10, 192.0.2.11
Before: 192.0.2.12
Error: provider returned status 500

Endpoints:
  server1 (192.0.2.10): healthy
  server2 (192.0.2.11): healthy
  server3 (192.0.2.12): UNHEALTHY
`,
			expectPrio:   webhook.NtfyPriorityHigh,
			expectedTags: []string{"red_circle", "warning"},
		},
		{
			name: "all_unhealthy",
			event: Event{
				Type:   config.EventAllUnhealthy,
				Domain: "example.com",
				Time:   testTime,
				Endpoints: []EndpointState{
					{Name: "server1", IP: "192.0.2.10"},
					{Name: "server2", IP: "192.0.2.11"},
				},
				Previous: []string{"192.0.2.10", "192.0.2.11"},
			},
			expectTitle: "No healthy endpoints for example.com",
			expectBody: `Event: all_unhealthy
Domain: example.com
Time: 2026-01-02T03:04:05Z
Status: unhealthy
Unchanged: 192.0.2.10, 192.0.2.11

Endpoints:
  server1 (192.0.2.10): UNHEALTHY
  server2 (192.0.2.11): UNHEALTHY
`,
			expectPrio:   webhook.NtfyPriorityMax,
			expectedTags: []string{"red_circle", "warning"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := newMessage(tc.event)
			assert.Equal(t, tc.expectTitle, msg.title)
			assert.Equal(t, tc.expectBody, msg.body)

			ntfy, err := msg.GetNtfyMessage()
			require.NoError(t, err)
			assert.Equal(t, webhook.NtfyMessage{
				Message:  tc.expectBody,
				Title:    tc.expectTitle,
				Priority: tc.expectPrio,
				Tags:     tc.expectedTags,
			}, ntfy)

			plain, err := msg.GetPlainMessage()
			require.NoError(t, err)
			assert.Equal(t, tc.expectTitle+"\n\n"+tc.expectBody, plain)

			slack, err := msg.GetSlackMessage()
			require.NoError(t, err)
			assert.Equal(t, tc.expectTitle+"\n\n"+tc.expectBody, slack.Text)
		})
	}
}
