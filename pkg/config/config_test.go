package config

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	configkit "github.com/italypaleale/go-kit/config"
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

func TestValidateEndpointPriority(t *testing.T) {
	tests := []struct {
		name      string
		priority  int
		expectErr string
	}{
		{name: "default", priority: 0},
		{name: "positive", priority: 1},
		{name: "large", priority: 1000},
		{name: "negative", priority: -1, expectErr: "domain test.example.com endpoint 1 is invalid: priority must not be negative"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
				Domains: []ConfigDomain{
					{
						RecordName: "test.example.com",
						Provider:   "test",
						Endpoints: []*ConfigEndpoint{
							{
								URL: "https://a.example.com/health",
								IP:  "192.0.2.1",
							},
							{
								URL:      "https://b.example.com/health",
								IP:       "192.0.2.2",
								Priority: tc.priority,
							},
						},
					},
				},
			}

			err := cfg.Validate(slog.Default())
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.priority, cfg.Domains[0].Endpoints[1].Priority)
		})
	}
}

func TestValidate(t *testing.T) {
	// Returns a valid config, which each test case changes
	newConfig := func() *Config {
		return &Config{
			Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
			Domains: []ConfigDomain{{
				RecordName: "test.example.com",
				Provider:   "test",
				Endpoints:  []*ConfigEndpoint{{URL: "https://test.example.com/health", IP: "192.0.2.1"}},
			}},
		}
	}

	errorTests := []struct {
		name      string
		mutate    func(c *Config)
		expectErr string
	}{
		{
			name:      "no providers",
			mutate:    func(c *Config) { c.Providers = nil },
			expectErr: "at least one provider must be configured",
		},
		{
			name:      "provider without a backend",
			mutate:    func(c *Config) { c.Providers["test"] = ConfigProvider{} },
			expectErr: "provider 'test' is invalid: exactly one provider must be configured",
		},
		{
			name:      "provider with two backends",
			mutate:    func(c *Config) { c.Providers["test"] = ConfigProvider{Cloudflare: &CloudflareConfig{}, OVH: &OVHConfig{}} },
			expectErr: "provider 'test' is invalid: exactly one provider must be configured",
		},
		{
			name:      "no domains",
			mutate:    func(c *Config) { c.Domains = nil },
			expectErr: "no domains configured",
		},
		{
			name:      "empty record name",
			mutate:    func(c *Config) { c.Domains[0].RecordName = "" },
			expectErr: "domain 0 is invalid: recordName is empty",
		},
		{
			name:      "no endpoints",
			mutate:    func(c *Config) { c.Domains[0].Endpoints = nil },
			expectErr: "domain test.example.com is invalid: endpoints list is empty",
		},
		{
			name:      "empty provider",
			mutate:    func(c *Config) { c.Domains[0].Provider = "" },
			expectErr: "domain 0 is invalid: provider is empty",
		},
		{
			name:      "unknown provider",
			mutate:    func(c *Config) { c.Domains[0].Provider = "other" },
			expectErr: "domain 0 is invalid: provider 'other' does not exist in the provider configuration",
		},
		{
			name:      "unsupported method",
			mutate:    func(c *Config) { c.Domains[0].HealthChecks.Method = "POST" },
			expectErr: "domain test.example.com is invalid: healthChecks.method must be GET or HEAD",
		},
		{
			name:      "negative recoverAfter",
			mutate:    func(c *Config) { c.Domains[0].HealthChecks.RecoverAfter = -1 },
			expectErr: "domain test.example.com is invalid: healthChecks.recoverAfter must not be negative",
		},
		{
			name:      "empty URL",
			mutate:    func(c *Config) { c.Domains[0].Endpoints[0].URL = "" },
			expectErr: "domain test.example.com endpoint 0 is invalid: URL is empty",
		},
		{
			name:      "empty IP",
			mutate:    func(c *Config) { c.Domains[0].Endpoints[0].IP = "" },
			expectErr: "domain test.example.com endpoint 0 is invalid: exactly one of 'ip' or 'cname' must be set",
		},
	}

	for _, tc := range errorTests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newConfig()
			tc.mutate(cfg)
			err := cfg.Validate(slog.Default())
			require.ErrorContains(t, err, tc.expectErr)
		})
	}

	t.Run("valid config", func(t *testing.T) {
		require.NoError(t, newConfig().Validate(slog.Default()))
	})

	t.Run("defaults and sanitization", func(t *testing.T) {
		cfg := newConfig()
		cfg.Domains[0].HealthChecks.Method = "head"
		cfg.Domains[0].Endpoints = append(cfg.Domains[0].Endpoints, &ConfigEndpoint{Name: "named", URL: "https://b.example.com/health", IP: "192.0.2.2"})

		err := cfg.Validate(slog.Default())
		require.NoError(t, err)

		d := cfg.Domains[0]
		assert.Equal(t, 120, d.TTL)
		assert.Equal(t, http.MethodHead, d.HealthChecks.Method)
		// Endpoint names default to the URL
		assert.Equal(t, "https://test.example.com/health", d.Endpoints[0].Name)
		assert.Equal(t, "named", d.Endpoints[1].Name)
	})

	t.Run("configured TTL is kept", func(t *testing.T) {
		cfg := newConfig()
		cfg.Domains[0].TTL = 300

		err := cfg.Validate(slog.Default())
		require.NoError(t, err)
		assert.Equal(t, 300, cfg.Domains[0].TTL)
	})
}

func TestCountSetProperties(t *testing.T) {
	assert.Equal(t, 0, countSetProperties(ConfigProvider{}))
	assert.Equal(t, 1, countSetProperties(ConfigProvider{Azure: &AzureConfig{}}))
	assert.Equal(t, 2, countSetProperties(&ConfigProvider{Azure: &AzureConfig{}, OVH: &OVHConfig{}}))
	assert.Equal(t, 1, countSetProperties(ConfigProvider{AWSRoute53: &AWSRoute53Config{}}))
	assert.Equal(t, 1, countSetProperties(ConfigProvider{GoogleCloudDNS: &GoogleCloudDNSConfig{}}))
	assert.Equal(t, 2, countSetProperties(ConfigProvider{AWSRoute53: &AWSRoute53Config{}, GoogleCloudDNS: &GoogleCloudDNSConfig{}}))
	assert.Panics(t, func() {
		countSetProperties("not a struct")
	})
}

// The sample config documents the options, so it must load and pass validation
func TestSampleConfig(t *testing.T) {
	samplePath := filepath.Join("..", "..", "config.sample.yaml")
	t.Setenv("DDUP_CONFIG", samplePath)

	// Load it the same way as the app, which rejects unknown keys
	cfg := GetDefaultConfig()
	err := configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	require.NoError(t, err)
	assert.Equal(t, samplePath, cfg.GetLoadedConfigPath())

	err = cfg.Validate(slog.Default())
	require.NoError(t, err)
	assert.NotEmpty(t, cfg.Domains)
	assert.NotEmpty(t, cfg.Providers)
}

// newTargetsTestConfig returns a valid config with the endpoints, all for the same domain
func newTargetsTestConfig(endpoints ...*ConfigEndpoint) *Config {
	for _, e := range endpoints {
		if e.URL == "" {
			e.URL = "https://health.example.com/health"
		}
	}
	return &Config{
		Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
		Domains: []ConfigDomain{{
			RecordName: "test.example.com",
			Provider:   "test",
			Endpoints:  endpoints,
		}},
	}
}

func proxiedOptions(proxied bool) ConfigEndpointProviderOptions {
	return ConfigEndpointProviderOptions{
		Cloudflare: &ConfigEndpointCloudflareOptions{Proxied: proxied},
	}
}

func TestValidateEndpointTarget(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  ConfigEndpoint
		expected  string
		expectErr string
	}{
		{
			name:      "neither ip nor cname",
			endpoint:  ConfigEndpoint{},
			expectErr: "domain test.example.com endpoint 0 is invalid: exactly one of 'ip' or 'cname' must be set",
		},
		{
			name:      "both ip and cname",
			endpoint:  ConfigEndpoint{IP: "192.0.2.10", CNAME: "tunnel.example.net"},
			expectErr: "domain test.example.com endpoint 0 is invalid: exactly one of 'ip' or 'cname' must be set",
		},
		{
			name:     "cname",
			endpoint: ConfigEndpoint{CNAME: "0a1b2c3d.cfargotunnel.com"},
			expected: "0a1b2c3d.cfargotunnel.com",
		},
		{
			name:     "cname is lowercased and loses the trailing dot",
			endpoint: ConfigEndpoint{CNAME: "Tunnel.Example.NET."},
			expected: "tunnel.example.net",
		},
		{
			name:     "cname with underscores and digits",
			endpoint: ConfigEndpoint{CNAME: "svc_1-a.example.net"},
			expected: "svc_1-a.example.net",
		},
		{
			name:      "cname that is an IPv4 address",
			endpoint:  ConfigEndpoint{CNAME: "192.0.2.10"},
			expectErr: `domain test.example.com endpoint 0 is invalid: cname "192.0.2.10" is an IP address; use ip instead`,
		},
		{
			name:      "cname that is an IPv6 address",
			endpoint:  ConfigEndpoint{CNAME: "2001:db8::10"},
			expectErr: `cname "2001:db8::10" is an IP address; use ip instead`,
		},
		{
			name:      "cname with a scheme",
			endpoint:  ConfigEndpoint{CNAME: "https://tunnel.example.net"},
			expectErr: `cname "https://tunnel.example.net" is not a valid hostname`,
		},
		{
			name:      "cname with an empty label",
			endpoint:  ConfigEndpoint{CNAME: "tunnel..example.net"},
			expectErr: `cname "tunnel..example.net" is not a valid hostname`,
		},
		{
			name:      "cname with a label starting with a hyphen",
			endpoint:  ConfigEndpoint{CNAME: "-tunnel.example.net"},
			expectErr: `cname "-tunnel.example.net" is not a valid hostname`,
		},
		{
			name:      "cname with a label that is too long",
			endpoint:  ConfigEndpoint{CNAME: strings.Repeat("a", 64) + ".example.net"},
			expectErr: "is not a valid hostname",
		},
		{
			name:      "cname that is too long",
			endpoint:  ConfigEndpoint{CNAME: strings.Repeat("a.", 130) + "net"},
			expectErr: "is not a valid hostname",
		},
		{
			name:      "cname with only a dot",
			endpoint:  ConfigEndpoint{CNAME: "."},
			expectErr: `cname "" is not a valid hostname`,
		},
		{
			name:      "cname that points to the record itself",
			endpoint:  ConfigEndpoint{CNAME: "Test.Example.com."},
			expectErr: `cname "test.example.com" is the domain's own record name`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := tc.endpoint
			cfg := newTargetsTestConfig(&endpoint)

			err := cfg.Validate(slog.Default())
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, cfg.Domains[0].Endpoints[0].CNAME)
			assert.Equal(t, tc.expected, cfg.Domains[0].Endpoints[0].Target())
		})
	}
}

func TestValidateDuplicateTargets(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []*ConfigEndpoint
		expectErr string
	}{
		{
			name: "same IP",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10"},
				{IP: "192.0.2.11"},
				{IP: "192.0.2.10", Priority: 1},
			},
			expectErr: "domain test.example.com endpoint 2 is invalid: 192.0.2.10 is already used by endpoint 0",
		},
		{
			name: "same IPv6 address written differently",
			endpoints: []*ConfigEndpoint{
				{IP: "2001:db8::10"},
				{IP: "2001:0DB8:0:0:0:0:0:10"},
			},
			expectErr: "domain test.example.com endpoint 1 is invalid: 2001:db8::10 is already used by endpoint 0",
		},
		{
			name: "same cname written differently",
			endpoints: []*ConfigEndpoint{
				{CNAME: "tunnel.example.net", Priority: 1},
				{CNAME: "TUNNEL.example.net.", Priority: 2},
			},
			expectErr: "domain test.example.com endpoint 1 is invalid: tunnel.example.net is already used by endpoint 0",
		},
		{
			name: "different targets",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10"},
				{IP: "2001:db8::10"},
				{CNAME: "tunnel.example.net", Priority: 1},
				{CNAME: "other.example.net", Priority: 2},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := newTargetsTestConfig(tc.endpoints...).Validate(slog.Default())
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("the same IP in different domains", func(t *testing.T) {
		cfg := newTargetsTestConfig(&ConfigEndpoint{IP: "192.0.2.10"})
		cfg.Domains = append(cfg.Domains, ConfigDomain{
			RecordName: "other.example.com",
			Provider:   "test",
			Endpoints:  []*ConfigEndpoint{{URL: "https://health.example.com/health", IP: "192.0.2.10"}},
		})
		require.NoError(t, cfg.Validate(slog.Default()))
	})
}

func TestValidateCNAMEAloneInPriority(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []*ConfigEndpoint
		expectErr string
	}{
		{
			name: "cname in its own priority",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10"},
				{IP: "192.0.2.11"},
				{CNAME: "tunnel.example.net", Priority: 1},
			},
		},
		{
			name: "cname with the lowest priority",
			endpoints: []*ConfigEndpoint{
				{CNAME: "tunnel.example.net"},
				{IP: "192.0.2.10", Priority: 1},
			},
		},
		{
			name: "cname alone",
			endpoints: []*ConfigEndpoint{
				{CNAME: "tunnel.example.net"},
			},
		},
		{
			name: "cname with an IP in the same priority",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10", Priority: 1},
				{CNAME: "tunnel.example.net", Priority: 1},
			},
			expectErr: "domain test.example.com is invalid: priority 1 has an endpoint with cname and other endpoints, but a CNAME record can't coexist with other records; give the endpoint with cname its own priority",
		},
		{
			name: "cname with an IP in the default priority",
			endpoints: []*ConfigEndpoint{
				{CNAME: "tunnel.example.net"},
				{IP: "192.0.2.10"},
			},
			expectErr: "priority 0 has an endpoint with cname",
		},
		{
			name: "two cnames in the same priority",
			endpoints: []*ConfigEndpoint{
				{CNAME: "tunnel.example.net"},
				{CNAME: "other.example.net"},
			},
			expectErr: "priority 0 has an endpoint with cname",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := newTargetsTestConfig(tc.endpoints...).Validate(slog.Default())
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestEndpointProviderOptions(t *testing.T) {
	assert.Empty(t, ConfigEndpointProviderOptions{}.Keys())
	assert.Equal(t, []string{"cloudflare"}, proxiedOptions(false).Keys())
}
