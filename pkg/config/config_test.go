package config

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEndpointIP(t *testing.T) {
	tests := []struct {
		name      string
		ip        string
		expected  string
		expectErr bool
	}{
		{name: "IPv4", ip: "192.0.2.1", expected: "192.0.2.1"},
		{name: "IPv6 is canonicalized", ip: "2001:0DB8:0:0:0:0:0:1", expected: "2001:db8::1"},
		{name: "IPv4-mapped IPv6 remains IPv6", ip: "::ffff:192.0.2.1", expected: "::ffff:192.0.2.1"},
		{name: "invalid", ip: "not-an-ip", expectErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Providers: map[string]ConfigProvider{
					"test": {
						Cloudflare: &CloudflareConfig{},
					},
				},
				Domains: []ConfigDomain{
					{
						RecordName: "test.example.com",
						Provider:   "test",
						Endpoints: []*ConfigEndpoint{
							{
								URL: "https://test.example.com/health",
								IP:  tc.ip,
							},
						},
					},
				},
			}

			err := cfg.Validate(slog.Default())
			if tc.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), `IP "not-an-ip" is not a valid IPv4 or IPv6 address`)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, cfg.Domains[0].Endpoints[0].IP)
		})
	}
}

func TestStatusMatcher(t *testing.T) {
	m, err := ParseStatusMatcher([]string{"2xx", "301-302", "418"})
	require.NoError(t, err)
	for _, code := range []int{200, 299, 301, 302, 418} {
		assert.True(t, m.Match(code), code)
	}
	for _, code := range []int{199, 300, 303, 404, 500} {
		assert.False(t, m.Match(code), code)
	}

	assert.True(t, StatusMatcher{}.Match(204), "zero value uses default")
	for _, bad := range []string{"", "abc", "6xx", "299-200", "99", "600"} {
		_, err = ParseStatusMatcher([]string{bad})
		require.Error(t, err, bad)
	}
}

func TestValidateWebhooks(t *testing.T) {
	cfg := &Config{Webhooks: []ConfigWebhook{{URL: "https://ntfy.example.com/topic", Events: []string{"dns_updated"}, Body: "{{ .Domain }}"}}}
	require.NoError(t, cfg.validateWebhooks())
	assert.Equal(t, "ntfy.example.com", cfg.Webhooks[0].Name)
	assert.Equal(t, "POST", cfg.Webhooks[0].Method)
	assert.Equal(t, 5, cfg.Webhooks[0].Attempts)

	for name, w := range map[string]ConfigWebhook{
		"bad url":      {URL: "ftp://x"},
		"bad event":    {URL: "https://x.example.com", Events: []string{"nope"}},
		"bad template": {URL: "https://x.example.com", Body: "{{ .Domain"},
		"bad header":   {URL: "https://x.example.com", Headers: map[string]string{"X": "{{"}},
	} {
		cfg = &Config{Webhooks: []ConfigWebhook{w}}
		require.Error(t, cfg.validateWebhooks(), name)
	}
}

func TestValidateWebhooks_TemplateFuncs(t *testing.T) {
	cfg := &Config{Webhooks: []ConfigWebhook{{URL: "https://x.example.com", Body: `{{ join .Healthy ", " }} {{ json .Endpoints }}`}}}
	require.NoError(t, cfg.validateWebhooks())
}
