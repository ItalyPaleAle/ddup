package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "sigs.k8s.io/yaml/goyaml.v3"
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
		"bad header":   {URL: "https://x.example.com", Headers: map[string]SecretString{"X": "{{"}},
	} {
		cfg = &Config{Webhooks: []ConfigWebhook{w}}
		require.Error(t, cfg.validateWebhooks(), name)
	}
}

func TestValidateWebhooks_TemplateFuncs(t *testing.T) {
	cfg := &Config{Webhooks: []ConfigWebhook{{URL: "https://x.example.com", Body: `{{ join .Healthy ", " }} {{ json .Endpoints }}`}}}
	require.NoError(t, cfg.validateWebhooks())
}

func TestSecretString(t *testing.T) {
	t.Setenv("DDUP_TEST_TOKEN", "from-env")
	secretFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(secretFile, []byte("from-file\n"), 0o600))

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(`
providers:
  literal:
    cloudflare: {apiToken: "plain", zoneId: "zone"}
  env:
    cloudflare: {apiToken: !env DDUP_TEST_TOKEN, zoneId: "zone"}
  file:
    cloudflare: {apiToken: !file ` + secretFile + `, zoneId: "zone"}
webhooks:
  - url: !env DDUP_TEST_TOKEN
    headers:
      Authorization: !env DDUP_TEST_TOKEN
`))
	dec.KnownFields(true)
	require.NoError(t, dec.Decode(&cfg))

	assert.Equal(t, "plain", cfg.Providers["literal"].Cloudflare.APIToken.String())
	assert.Equal(t, "from-env", cfg.Providers["env"].Cloudflare.APIToken.String())
	assert.Equal(t, "from-file", cfg.Providers["file"].Cloudflare.APIToken.String(), "trailing newline is removed")
	assert.Equal(t, "from-env", cfg.Webhooks[0].URL.String())
	assert.Equal(t, "from-env", cfg.Webhooks[0].Headers["Authorization"].String())

	for _, bad := range []string{
		"apiToken: !env DDUP_TEST_NOT_SET_ANYWHERE",
		"apiToken: !env",
		"apiToken: !file /nonexistent/ddup/secret",
	} {
		var c ConfigProvider
		dec = yaml.NewDecoder(strings.NewReader("cloudflare: {" + bad + "}"))
		dec.KnownFields(true)
		require.Error(t, dec.Decode(&c), bad)
	}
}
