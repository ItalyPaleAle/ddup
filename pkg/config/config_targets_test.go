package config

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			expectErr: "domain test.example.com endpoint 0 is invalid: exactly one of ip or cname must be set",
		},
		{
			name:      "both ip and cname",
			endpoint:  ConfigEndpoint{IP: "192.0.2.10", CNAME: "tunnel.example.net"},
			expectErr: "domain test.example.com endpoint 0 is invalid: exactly one of ip or cname must be set",
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

func TestValidateProxiedPerPriority(t *testing.T) {
	tests := []struct {
		name      string
		endpoints []*ConfigEndpoint
		expectErr string
	}{
		{
			name: "all proxied",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10", ProviderOptions: proxiedOptions(true)},
				{IP: "192.0.2.11", ProviderOptions: proxiedOptions(true)},
			},
		},
		{
			name: "none proxied, with and without options",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10"},
				{IP: "192.0.2.11", ProviderOptions: proxiedOptions(false)},
			},
		},
		{
			name: "different values in different priorities",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10"},
				{IP: "192.0.2.11"},
				{CNAME: "tunnel.example.net", Priority: 1, ProviderOptions: proxiedOptions(true)},
			},
		},
		{
			name: "proxied and not proxied in the same priority",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10", ProviderOptions: proxiedOptions(true)},
				{IP: "192.0.2.11"},
			},
			expectErr: "domain test.example.com endpoint 1 is invalid: endpoints with priority 0 must all use the same value for providerOptions.cloudflare.proxied",
		},
		{
			name: "not proxied and proxied in the same priority",
			endpoints: []*ConfigEndpoint{
				{IP: "192.0.2.10", Priority: 2},
				{IP: "192.0.2.11", Priority: 2, ProviderOptions: proxiedOptions(true)},
			},
			expectErr: "endpoint 1 is invalid: endpoints with priority 2 must all use the same value",
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

	assert.False(t, (&ConfigEndpoint{}).Proxied())
	assert.False(t, (&ConfigEndpoint{ProviderOptions: proxiedOptions(false)}).Proxied())
	assert.True(t, (&ConfigEndpoint{ProviderOptions: proxiedOptions(true)}).Proxied())
}
