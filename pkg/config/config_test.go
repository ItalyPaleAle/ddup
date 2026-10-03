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

func TestValidateEndpoints_TiersAndTargets(t *testing.T) {
	cloudflare := ConfigProvider{Cloudflare: &CloudflareConfig{}}
	ovh := ConfigProvider{OVH: &OVHConfig{}}

	tests := []struct {
		name      string
		provider  ConfigProvider
		endpoints []*ConfigEndpoint
		errSubstr string
	}{
		{name: "ips with priorities", provider: cloudflare, endpoints: []*ConfigEndpoint{
			{URL: "https://a", IP: "1.1.1.1"}, {URL: "https://b", IP: "2.2.2.2"}, {URL: "https://c", IP: "3.3.3.3", Priority: 1},
		}},
		{name: "cname in its own priority", provider: cloudflare, endpoints: []*ConfigEndpoint{
			{URL: "https://a", IP: "1.1.1.1"}, {URL: "https://c", CNAME: "tunnel.example.com", Proxied: true, Priority: 1},
		}},
		{name: "ip and cname", provider: cloudflare, endpoints: []*ConfigEndpoint{{URL: "https://a", IP: "1.1.1.1", CNAME: "a.example.com"}}, errSubstr: "mutually exclusive"},
		{name: "neither ip nor cname", provider: cloudflare, endpoints: []*ConfigEndpoint{{URL: "https://a"}}, errSubstr: "one of ip, cname and ipLookup is required"},
		{name: "cname with other endpoints in the same priority", provider: cloudflare, endpoints: []*ConfigEndpoint{
			{URL: "https://a", IP: "1.1.1.1"}, {URL: "https://c", CNAME: "tunnel.example.com"},
		}, errSubstr: "can't coexist"},
		{name: "two cnames in the same priority", provider: cloudflare, endpoints: []*ConfigEndpoint{
			{URL: "https://a", CNAME: "a.example.com"}, {URL: "https://c", CNAME: "b.example.com"},
		}, errSubstr: "can't coexist"},
		{name: "mixed proxied in a priority", provider: cloudflare, endpoints: []*ConfigEndpoint{
			{URL: "https://a", IP: "1.1.1.1", Proxied: true}, {URL: "https://b", IP: "2.2.2.2"},
		}, errSubstr: "same value for proxied"},
		{name: "duplicate target", provider: cloudflare, endpoints: []*ConfigEndpoint{
			{URL: "https://a", IP: "1.1.1.1"}, {URL: "https://b", IP: "1.1.1.1", Priority: 1},
		}, errSubstr: "more than one endpoint"},
		{name: "negative priority", provider: cloudflare, endpoints: []*ConfigEndpoint{{URL: "https://a", IP: "1.1.1.1", Priority: -1}}, errSubstr: "must not be negative"},
		{name: "cname is an ip", provider: cloudflare, endpoints: []*ConfigEndpoint{{URL: "https://a", CNAME: "1.1.1.1"}}, errSubstr: "IP address"},
		{name: "invalid cname", provider: cloudflare, endpoints: []*ConfigEndpoint{{URL: "https://a", CNAME: "not a host"}}, errSubstr: "not a valid hostname"},
		{name: "cname needs cloudflare", provider: ovh, endpoints: []*ConfigEndpoint{{URL: "https://a", CNAME: "a.example.com"}}, errSubstr: "only supported by the Cloudflare provider"},
		{name: "proxied needs cloudflare", provider: ovh, endpoints: []*ConfigEndpoint{{URL: "https://a", IP: "1.1.1.1", Proxied: true}}, errSubstr: "only supported by the Cloudflare provider"},
		{name: "priorities work with any provider", provider: ovh, endpoints: []*ConfigEndpoint{
			{URL: "https://a", IP: "1.1.1.1"}, {URL: "https://b", IP: "2.2.2.2", Priority: 1},
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Providers: map[string]ConfigProvider{"p": tc.provider},
				Domains:   []ConfigDomain{{RecordName: "app.example.com", Provider: "p", Endpoints: tc.endpoints}},
			}
			err := cfg.Validate(slog.Default())
			if tc.errSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("cname is normalized", func(t *testing.T) {
		cfg := &Config{
			Providers: map[string]ConfigProvider{"p": cloudflare},
			Domains:   []ConfigDomain{{RecordName: "app.example.com", Provider: "p", Endpoints: []*ConfigEndpoint{{URL: "https://a", CNAME: "Tunnel.Example.COM."}}}},
		}
		require.NoError(t, cfg.Validate(slog.Default()))
		assert.Equal(t, "tunnel.example.com", cfg.Domains[0].Endpoints[0].CNAME)
		assert.Equal(t, "tunnel.example.com", cfg.Domains[0].Endpoints[0].Target())
	})
}

func TestValidateEndpoints_IPLookup(t *testing.T) {
	cloudflare := ConfigProvider{Cloudflare: &CloudflareConfig{}}

	tests := []struct {
		name      string
		endpoint  *ConfigEndpoint
		errSubstr string
	}{
		{name: "lookup without a health check URL", endpoint: &ConfigEndpoint{IPLookup: &ConfigIPLookup{URLs: []string{"https://api.ipify.org"}}}},
		{name: "lookup with family and pattern", endpoint: &ConfigEndpoint{URL: "https://home.example.com/health", Proxied: true, IPLookup: &ConfigIPLookup{
			URLs: []string{"https://api6.ipify.org?format=json", "https://example.com/ip"}, Family: 6, Pattern: `"ip":"([^"]+)"`,
		}}},
		{name: "no URLs", endpoint: &ConfigEndpoint{IPLookup: &ConfigIPLookup{}}, errSubstr: "at least one URL"},
		{name: "relative URL", endpoint: &ConfigEndpoint{IPLookup: &ConfigIPLookup{URLs: []string{"/ip"}}}, errSubstr: "absolute http(s) URL"},
		{name: "bad family", endpoint: &ConfigEndpoint{IPLookup: &ConfigIPLookup{URLs: []string{"https://x.example.com"}, Family: 5}}, errSubstr: "family must be 4 or 6"},
		{name: "bad pattern", endpoint: &ConfigEndpoint{IPLookup: &ConfigIPLookup{URLs: []string{"https://x.example.com"}, Pattern: "("}}, errSubstr: "pattern is invalid"},
		{name: "pattern needs one group", endpoint: &ConfigEndpoint{IPLookup: &ConfigIPLookup{URLs: []string{"https://x.example.com"}, Pattern: `\d+`}}, errSubstr: "exactly one capture group"},
		{name: "lookup and ip", endpoint: &ConfigEndpoint{IP: "1.1.1.1", IPLookup: &ConfigIPLookup{URLs: []string{"https://x.example.com"}}}, errSubstr: "mutually exclusive"},
		{name: "lookup and cname", endpoint: &ConfigEndpoint{CNAME: "a.example.com", URL: "https://a", IPLookup: &ConfigIPLookup{URLs: []string{"https://x.example.com"}}}, errSubstr: "mutually exclusive"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Providers: map[string]ConfigProvider{"p": cloudflare},
				Domains:   []ConfigDomain{{RecordName: "home.example.com", Provider: "p", Endpoints: []*ConfigEndpoint{tc.endpoint}}},
			}
			err := cfg.Validate(slog.Default())
			if tc.errSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errSubstr)
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, tc.endpoint.Name, "name defaults to a URL")
			assert.True(t, tc.endpoint.Dynamic())
			assert.Empty(t, tc.endpoint.Target())
		})
	}

	t.Run("several dynamic endpoints don't collide", func(t *testing.T) {
		lookup := func() *ConfigIPLookup { return &ConfigIPLookup{URLs: []string{"https://api.ipify.org"}} }
		cfg := &Config{
			Providers: map[string]ConfigProvider{"p": cloudflare},
			Domains: []ConfigDomain{{RecordName: "home.example.com", Provider: "p", Endpoints: []*ConfigEndpoint{
				{IPLookup: lookup()}, {IPLookup: lookup(), Priority: 1},
			}}},
		}
		require.NoError(t, cfg.Validate(slog.Default()))
	})
}

func TestRedactURL(t *testing.T) {
	assert.Equal(t, "https://ip.example.com/ip", RedactURL("https://user:pass@ip.example.com/ip?token=s3cret#frag"))
	assert.Equal(t, "https://ip.example.com", RedactURL("https://ip.example.com"))
	assert.Equal(t, "(invalid URL)", RedactURL("not a url token=s3cret"))
}
