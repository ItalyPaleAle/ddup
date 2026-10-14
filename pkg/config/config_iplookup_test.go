package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	configkit "github.com/italypaleale/go-kit/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateIPLookup(t *testing.T) {
	// Returns a valid config with a dynamic endpoint, which each test case changes
	newConfig := func() *Config {
		return &Config{
			IPLookupURLs: []ConfigIPLookupURL{
				{URL: "https://ip.example.com/v4", Family: 4},
				{URL: "https://ip.example.com/v6", Family: 6},
			},
			Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
			Domains: []ConfigDomain{{
				RecordName: "home.example.com",
				Provider:   "test",
				Endpoints:  []*ConfigEndpoint{{IPLookupFamily: 4}},
			}},
		}
	}
	overrides := func(urls ...ConfigIPLookupURL) func(c *Config) {
		return func(c *Config) { c.IPLookupURLs = urls }
	}

	tests := []struct {
		name      string
		mutate    func(c *Config)
		expectErr string
	}{
		{
			name:   "valid: family 4 without a URL",
			mutate: func(c *Config) {},
		},
		{
			name:   "valid: family 6",
			mutate: func(c *Config) { c.Domains[0].Endpoints[0].IPLookupFamily = 6 },
		},
		{
			name:   "valid: with a health check URL",
			mutate: func(c *Config) { c.Domains[0].Endpoints[0].URL = "https://home.example.com/health" },
		},
		{
			name:   "valid: overridden list",
			mutate: overrides(ConfigIPLookupURL{URL: "https://ip.example.com/?token=abc", Family: 4, Pattern: `"ip":"([^"]+)"`}),
		},
		{
			name: "valid: list of the endpoint, without top-level list",
			mutate: func(c *Config) {
				c.IPLookupURLs = nil
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{{URL: "https://ip.example.com/a"}, {URL: "https://ip.example.com/b", Pattern: `"ip":"([^"]+)"`}}
			},
		},
		{
			name: "valid: family of the entries matches",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{{URL: "https://ip.example.com/a", Family: 4}}
			},
		},
		{
			name: "family of an entry doesn't match the endpoint",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{
					{URL: "https://ip.example.com/a"},
					{URL: "https://ip.example.com/b?token=s3cret", Family: 6},
				}
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupURLs[1] (https://ip.example.com/b) is invalid: family is 6, but the endpoint has ipLookupFamily 4; an endpoint looks up one IP version, so IPv4 and IPv6 services can't be mixed in its list",
		},
		{
			name: "empty list on the endpoint",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{}
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupURLs is empty: add at least one service, or remove the key to use the top-level ipLookupURLs",
		},
		{
			name: "list on a static endpoint",
			mutate: func(c *Config) {
				ep := c.Domains[0].Endpoints[0]
				ep.IPLookupFamily = 0
				ep.IP = "192.0.2.1"
				ep.URL = "https://home.example.com/health"
				ep.IPLookupURLs = []ConfigIPLookupURL{{URL: "https://ip.example.com/a"}}
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupURLs is set, but the endpoint has no ipLookupFamily",
		},
		{
			name: "http URL on the endpoint",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{{URL: "http://ip.example.com/?token=s3cret"}}
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupURLs[0] is invalid: url must be an absolute https URL",
		},
		{
			name: "bad pattern on the endpoint",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{{URL: "https://ip.example.com", Pattern: `(\d+)\.(\d+)`}}
			},
			expectErr: "ipLookupURLs[0] (https://ip.example.com) is invalid: pattern must have exactly one capture group",
		},
		{
			name: "duplicate on the endpoint, with and without family",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupURLs = []ConfigIPLookupURL{
					{URL: "https://ip.example.com/?token=s3cret"},
					{URL: "https://ip.example.com/?token=s3cret", Family: 4},
				}
			},
			expectErr: "ipLookupURLs[1] (https://ip.example.com/) is invalid: the same url and family are already in the list",
		},
		{
			name: "no service at all",
			mutate: func(c *Config) {
				c.IPLookupURLs = nil
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupFamily is 4, but no service is configured for it: set ipLookupURLs on the endpoint, or add an entry with family 4 to the top-level ipLookupURLs",
		},
		{
			name:      "bad family",
			mutate:    func(c *Config) { c.Domains[0].Endpoints[0].IPLookupFamily = 5 },
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupFamily must be 4 or 6, got 5",
		},
		{
			name:      "negative family",
			mutate:    func(c *Config) { c.Domains[0].Endpoints[0].IPLookupFamily = -4 },
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupFamily must be 4 or 6, got -4",
		},
		{
			name: "ip and ipLookupFamily",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IP = "192.0.2.1"
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ip and ipLookupFamily are mutually exclusive",
		},
		{
			name:      "neither ip nor ipLookupFamily",
			mutate:    func(c *Config) { c.Domains[0].Endpoints[0].IPLookupFamily = 0 },
			expectErr: "domain home.example.com endpoint 0 is invalid: one of ip and ipLookupFamily is required",
		},
		{
			name: "static endpoint without a URL",
			mutate: func(c *Config) {
				c.Domains[0].Endpoints[0].IPLookupFamily = 0
				c.Domains[0].Endpoints[0].IP = "192.0.2.1"
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: URL is empty",
		},
		{
			name: "no service for the family in the top-level list",
			mutate: func(c *Config) {
				overrides(ConfigIPLookupURL{URL: "https://ip.example.com", Family: 6})(c)
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupFamily is 4, but no service is configured for it: set ipLookupURLs on the endpoint, or add an entry with family 4 to the top-level ipLookupURLs",
		},
		{
			name:      "http URL",
			mutate:    overrides(ConfigIPLookupURL{URL: "http://ip.example.com/?token=s3cret", Family: 4}),
			expectErr: "ipLookupURLs[0] is invalid: url must be an absolute https URL",
		},
		{
			name:      "relative URL",
			mutate:    overrides(ConfigIPLookupURL{URL: "/ip", Family: 4}),
			expectErr: "ipLookupURLs[0] is invalid: url must be an absolute https URL",
		},
		{
			name:      "empty URL",
			mutate:    overrides(ConfigIPLookupURL{URL: "", Family: 4}),
			expectErr: "ipLookupURLs[0] is invalid: url is empty",
		},
		{
			name:      "top-level entry without a family",
			mutate:    overrides(ConfigIPLookupURL{URL: "https://ip.example.com/?token=s3cret"}),
			expectErr: "ipLookupURLs[0] (https://ip.example.com/) is invalid: family must be 4 or 6, got 0",
		},
		{
			name:      "bad pattern",
			mutate:    overrides(ConfigIPLookupURL{URL: "https://ip.example.com", Family: 4, Pattern: "("}),
			expectErr: "ipLookupURLs[0] (https://ip.example.com) is invalid: pattern is not a valid regular expression",
		},
		{
			name:      "pattern without a capture group",
			mutate:    overrides(ConfigIPLookupURL{URL: "https://ip.example.com", Family: 4, Pattern: `\d+`}),
			expectErr: "ipLookupURLs[0] (https://ip.example.com) is invalid: pattern must have exactly one capture group",
		},
		{
			name:      "pattern with two capture groups",
			mutate:    overrides(ConfigIPLookupURL{URL: "https://ip.example.com", Family: 4, Pattern: `(\d+)\.(\d+)`}),
			expectErr: "ipLookupURLs[0] (https://ip.example.com) is invalid: pattern must have exactly one capture group",
		},
		{
			name: "duplicate",
			mutate: overrides(
				ConfigIPLookupURL{URL: "https://ip.example.com/?token=s3cret", Family: 4},
				ConfigIPLookupURL{URL: "https://ip.example.com/?token=s3cret", Family: 4},
			),
			expectErr: "ipLookupURLs[1] (https://ip.example.com/) is invalid: the same url and family are already in the list",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newConfig()
			tc.mutate(cfg)

			err := cfg.Validate(slog.Default())
			if tc.expectErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.expectErr)
			assert.NotContains(t, err.Error(), "s3cret", "URLs can contain secrets")
		})
	}
}

func TestValidateIPLookup_EndpointsOfOneDomain(t *testing.T) {
	cfg := &Config{
		IPLookupURLs: []ConfigIPLookupURL{{URL: "https://ip.example.com", Family: 4}},
		Providers:    map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
		Domains: []ConfigDomain{{
			RecordName: "home.example.com",
			Provider:   "test",
			Endpoints: []*ConfigEndpoint{
				{IPLookupFamily: 4},
				{IPLookupFamily: 6},
			},
		}},
	}
	err := cfg.Validate(slog.Default())
	require.ErrorContains(t, err, "domain home.example.com endpoint 1 is invalid: ipLookupFamily is 6, but no service is configured for it: set ipLookupURLs on the endpoint, or add an entry with family 6 to the top-level ipLookupURLs")

	// With a list of its own the second endpoint is valid
	cfg.Domains[0].Endpoints[1].IPLookupURLs = []ConfigIPLookupURL{{URL: "https://ip6.example.com"}}
	require.NoError(t, cfg.Validate(slog.Default()))
}

func TestValidateIPLookup_Names(t *testing.T) {
	cfg := &Config{
		Providers:    map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
		IPLookupURLs: []ConfigIPLookupURL{{URL: "https://ip.example.com/v4", Family: 4}, {URL: "https://ip.example.com/v6", Family: 6}},
		Domains: []ConfigDomain{{
			RecordName: "home.example.com",
			Provider:   "test",
			Endpoints: []*ConfigEndpoint{
				{IPLookupFamily: 4},
				{IPLookupFamily: 6, Name: "home-v6"},
				{IPLookupFamily: 4, URL: "https://home.example.com/health"},
			},
		}},
	}
	require.NoError(t, cfg.Validate(slog.Default()))

	names := make([]string, 0, 3)
	for _, ep := range cfg.Domains[0].Endpoints {
		names = append(names, ep.Name)
	}
	assert.Equal(t, []string{"IPv4 lookup", "home-v6", "https://home.example.com/health"}, names)
}

func TestIPLookupSources(t *testing.T) {
	cfg := &Config{IPLookupURLs: []ConfigIPLookupURL{
		{URL: "https://ip.example.com/v4", Family: 4},
		{URL: "https://ip6.example.com", Family: 6, Pattern: `ip=(\S+)`},
		{URL: "https://ip.example.com/v4-2", Family: 4},
	}}

	t.Run("no list on the endpoint: the top-level entries with its family", func(t *testing.T) {
		sources, err := cfg.IPLookupSources(&ConfigEndpoint{IPLookupFamily: 4})
		require.NoError(t, err)
		require.Len(t, sources, 2)
		assert.Equal(t, "https://ip.example.com/v4", sources[0].URL)
		assert.Equal(t, 4, sources[0].Family)
		assert.Nil(t, sources[0].Pattern)
		assert.Equal(t, "https://ip.example.com/v4-2", sources[1].URL)

		sources, err = cfg.IPLookupSources(&ConfigEndpoint{IPLookupFamily: 6})
		require.NoError(t, err)
		require.Len(t, sources, 1)
		assert.Equal(t, 6, sources[0].Family)
		require.NotNil(t, sources[0].Pattern)
		assert.Equal(t, `ip=(\S+)`, sources[0].Pattern.String())
	})

	t.Run("the list of the endpoint replaces the top-level one", func(t *testing.T) {
		ep := &ConfigEndpoint{
			IPLookupFamily: 6,
			IPLookupURLs:   []ConfigIPLookupURL{{URL: "https://own.example.com/a"}, {URL: "https://own.example.com/b", Family: 6}},
		}
		sources, err := cfg.IPLookupSources(ep)
		require.NoError(t, err)
		require.Len(t, sources, 2)
		assert.Equal(t, "https://own.example.com/a", sources[0].URL)
		assert.Equal(t, 6, sources[0].Family, "the family comes from the endpoint")
		assert.Equal(t, "https://own.example.com/b", sources[1].URL)
	})

	t.Run("invalid pattern", func(t *testing.T) {
		bad := &Config{IPLookupURLs: []ConfigIPLookupURL{{URL: "https://ip.example.com", Family: 4, Pattern: "("}}}
		_, err := bad.IPLookupSources(&ConfigEndpoint{IPLookupFamily: 4})
		require.ErrorContains(t, err, "entry 0 in ipLookupURLs has an invalid pattern")
	})
}

// Loads a config file the same way as the app, which rejects unknown keys
func TestLoadIPLookupConfig(t *testing.T) {
	const content = `
domains:
  - recordName: "home.example.com"
    provider: "test"
    endpoints:
      - name: "home"
        ipLookupFamily: 6
      - name: "home-v4"
        ipLookupFamily: 4
        ipLookupURLs:
          - url: "https://own.example.com/a"
          - url: "https://own.example.com/b"
            family: 4
            pattern: 'ip=(\S+)'
ipLookupURLs:
  - url: "https://ip.example.com/v4"
    family: 4
  - url: "https://ip.example.com/v6?token=abc"
    family: 6
    pattern: '"ip":\s*"([^"]+)"'
providers:
  test:
    cloudflare:
      apiToken: "token"
      zoneId: "zone"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	t.Setenv("DDUP_CONFIG", path)

	cfg := GetDefaultConfig()
	err := configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	require.NoError(t, err)
	require.NoError(t, cfg.Validate(slog.Default()))

	assert.Equal(t, 6, cfg.Domains[0].Endpoints[0].IPLookupFamily)
	assert.Equal(t, []ConfigIPLookupURL{
		{URL: "https://own.example.com/a"},
		{URL: "https://own.example.com/b", Family: 4, Pattern: `ip=(\S+)`},
	}, cfg.Domains[0].Endpoints[1].IPLookupURLs)
	assert.Nil(t, cfg.Domains[0].Endpoints[0].IPLookupURLs)
	assert.Equal(t, []ConfigIPLookupURL{
		{URL: "https://ip.example.com/v4", Family: 4},
		{URL: "https://ip.example.com/v6?token=abc", Family: 6, Pattern: `"ip":\s*"([^"]+)"`},
	}, cfg.IPLookupURLs)
}

// An empty list on an endpoint is different from no list: it must be rejected, not fall back to the top-level one
func TestLoadIPLookupConfig_EmptyEndpointList(t *testing.T) {
	const content = `
domains:
  - recordName: "home.example.com"
    provider: "test"
    endpoints:
      - name: "home"
        ipLookupFamily: 4
        ipLookupURLs: []
ipLookupURLs:
  - url: "https://ip.example.com/v4"
    family: 4
providers:
  test:
    cloudflare:
      apiToken: "token"
      zoneId: "zone"
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	t.Setenv("DDUP_CONFIG", path)

	cfg := GetDefaultConfig()
	err := configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	require.NoError(t, err)

	err = cfg.Validate(slog.Default())
	require.ErrorContains(t, err, "ipLookupURLs is empty")
}
