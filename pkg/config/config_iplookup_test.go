package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	configkit "github.com/italypaleale/go-kit/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/iplookup"
)

func TestValidateIPLookup(t *testing.T) {
	// Returns a valid config with a dynamic endpoint, which each test case changes
	newConfig := func() *Config {
		return &Config{
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
			name:   "valid: family 6 with the default services",
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
			name: "family with no service in the overridden list",
			mutate: func(c *Config) {
				overrides(ConfigIPLookupURL{URL: "https://ip.example.com", Family: 6})(c)
			},
			expectErr: "domain home.example.com endpoint 0 is invalid: ipLookupFamily is 4, but there is no service for IPv4 in the list of ipLookupURLs",
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
			name:      "bad family in the list",
			mutate:    overrides(ConfigIPLookupURL{URL: "https://ip.example.com/?token=s3cret", Family: 0}),
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

func TestValidateIPLookup_FamilyWithoutServiceInDefaults(t *testing.T) {
	iplookup.SetDefaultSources(t, []iplookup.Source{{URL: "https://ip.example.com", Family: 4}})

	cfg := &Config{
		Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
		Domains: []ConfigDomain{{
			RecordName: "home.example.com",
			Provider:   "test",
			Endpoints:  []*ConfigEndpoint{{IPLookupFamily: 4}, {IPLookupFamily: 6}},
		}},
	}
	err := cfg.Validate(slog.Default())
	require.ErrorContains(t, err, "domain home.example.com endpoint 1 is invalid: ipLookupFamily is 6, but there is no service for IPv6 in the list of ipLookupURLs")
}

func TestValidateIPLookup_Names(t *testing.T) {
	cfg := &Config{
		Providers: map[string]ConfigProvider{"test": {Cloudflare: &CloudflareConfig{}}},
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
	t.Run("defaults", func(t *testing.T) {
		cfg := &Config{}
		sources, err := cfg.IPLookupSources()
		require.NoError(t, err)
		assert.Equal(t, iplookup.DefaultSources(), sources)
	})

	t.Run("override replaces the defaults", func(t *testing.T) {
		cfg := &Config{IPLookupURLs: []ConfigIPLookupURL{
			{URL: "https://ip.example.com", Family: 4},
			{URL: "https://ip6.example.com", Family: 6, Pattern: `ip=(\S+)`},
		}}
		sources, err := cfg.IPLookupSources()
		require.NoError(t, err)
		require.Len(t, sources, 2)
		assert.Equal(t, "https://ip.example.com", sources[0].URL)
		assert.Nil(t, sources[0].Pattern)
		assert.Equal(t, 6, sources[1].Family)
		require.NotNil(t, sources[1].Pattern)
		assert.Equal(t, `ip=(\S+)`, sources[1].Pattern.String())
	})

	t.Run("invalid pattern", func(t *testing.T) {
		cfg := &Config{IPLookupURLs: []ConfigIPLookupURL{{URL: "https://ip.example.com", Family: 4, Pattern: "("}}}
		_, err := cfg.IPLookupSources()
		require.ErrorContains(t, err, "ipLookupURLs[0] has an invalid pattern")
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
		{URL: "https://ip.example.com/v4", Family: 4},
		{URL: "https://ip.example.com/v6?token=abc", Family: 6, Pattern: `"ip":\s*"([^"]+)"`},
	}, cfg.IPLookupURLs)
}
