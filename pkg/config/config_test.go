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

func TestStatusMatches(t *testing.T) {
	tests := []struct {
		name   string
		expect string
		match  []int
		reject []int
	}{
		{name: "empty uses 2xx", expect: "", match: []int{200, 204, 299}, reject: []int{199, 300, 404, 500}},
		{name: "2xx", expect: "2xx", match: []int{200, 204, 299}, reject: []int{199, 300, 404, 500}},
		{name: "exact code", expect: "204", match: []int{204}, reject: []int{200, 203, 205, 404}},
		{name: "exact non-2xx code", expect: "418", match: []int{418}, reject: []int{200, 417, 419}},
		{name: "lowest valid code", expect: "100", match: []int{100}, reject: []int{101, 200}},
		{name: "highest valid code", expect: "599", match: []int{599}, reject: []int{598, 200}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hc := ConfigHealthChecks{ExpectStatus: tc.expect}
			for _, code := range tc.match {
				assert.True(t, hc.StatusMatches(code), "code %d", code)
			}
			for _, code := range tc.reject {
				assert.False(t, hc.StatusMatches(code), "code %d", code)
			}
		})
	}
}

func TestValidateExpectStatus(t *testing.T) {
	for _, ok := range []string{"", "2xx", "200", "204", "301", "404", "100", "599"} {
		require.NoError(t, validateExpectStatus(ok), "%q", ok)
	}

	// Only 2xx is supported as a class, and codes must be in the valid range
	for _, bad := range []string{"3xx", "4xx", "5xx", "2XX", "2x", "abc", "99", "600", "0", "-200", "200-299", "200,204", " 200", "20.0"} {
		err := validateExpectStatus(bad)
		require.Error(t, err, "%q", bad)
		assert.Contains(t, err.Error(), "expectStatus")
	}
}

func TestValidateExpectStatus_InConfig(t *testing.T) {
	newConfig := func(expect string) *Config {
		return &Config{
			Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
			Domains: []ConfigDomain{{
				RecordName:   "test.example.com",
				Provider:     "test",
				HealthChecks: ConfigHealthChecks{ExpectStatus: expect},
				Endpoints:    []*ConfigEndpoint{{URL: "https://test.example.com/health", IP: "192.0.2.1"}},
			}},
		}
	}

	require.NoError(t, newConfig("").Validate(slog.Default()))
	require.NoError(t, newConfig("204").Validate(slog.Default()))

	err := newConfig("3xx").Validate(slog.Default())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test.example.com")
	assert.Contains(t, err.Error(), "healthChecks.expectStatus")
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
