package dns

import (
	"log/slog"
	"path/filepath"
	"testing"

	configkit "github.com/italypaleale/go-kit/config"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

func TestValidateConfig(t *testing.T) {
	providers := map[string]Provider{
		"cf":     &CloudflareProvider{},
		"ovh":    &OVHProvider{},
		"azure":  &AzureProvider{},
		"aws":    &AWSRoute53Provider{},
		"google": &GoogleCloudDNSProvider{},
	}
	cnameEndpoint := &config.ConfigEndpoint{
		Name:  "tunnel",
		URL:   "https://tunnel-health.example.com/health",
		CNAME: "0a1b2c3d.cfargotunnel.com",
	}
	proxiedEndpoint := &config.ConfigEndpoint{
		Name: "vps-us",
		URL:  "https://vps-us.example.com/health",
		IP:   "192.0.2.10",
		ProviderOptions: config.ConfigEndpointProviderOptions{
			Cloudflare: &config.ConfigEndpointCloudflareOptions{Proxied: true},
		},
	}
	plainEndpoint := &config.ConfigEndpoint{
		Name: "vps-eu",
		URL:  "https://vps-eu.example.com/health",
		IP:   "192.0.2.11",
	}

	tests := []struct {
		name      string
		provider  string
		endpoints []*config.ConfigEndpoint
		expectErr string
	}{
		{name: "cname on Cloudflare", provider: "cf", endpoints: []*config.ConfigEndpoint{cnameEndpoint}},
		{name: "proxied on Cloudflare", provider: "cf", endpoints: []*config.ConfigEndpoint{proxiedEndpoint}},
		{
			name:     "proxied and not proxied in the same priority",
			provider: "cf",
			endpoints: []*config.ConfigEndpoint{
				proxiedEndpoint,
				plainEndpoint,
			},
			expectErr: "domain app.example.com endpoint 1 is invalid: endpoints with priority 0 must all use the same value for providerOptions.cloudflare.proxied",
		},
		{
			name:     "not proxied and proxied in the same priority",
			provider: "cf",
			endpoints: []*config.ConfigEndpoint{
				{Name: "a", URL: "https://a.example.com", IP: "192.0.2.20", Priority: 2},
				{Name: "b", URL: "https://b.example.com", IP: "192.0.2.21", Priority: 2, ProviderOptions: proxiedEndpoint.ProviderOptions},
			},
			expectErr: "endpoint 1 is invalid: endpoints with priority 2 must all use the same value",
		},
		{
			name:     "different values in different priorities",
			provider: "cf",
			endpoints: []*config.ConfigEndpoint{
				plainEndpoint,
				{Name: "tunnel", URL: "https://t.example.com", CNAME: "tunnel.example.net", Priority: 1, ProviderOptions: proxiedEndpoint.ProviderOptions},
			},
		},
		{
			name:     "explicit proxied false next to no options",
			provider: "cf",
			endpoints: []*config.ConfigEndpoint{
				plainEndpoint,
				{Name: "b", URL: "https://b.example.com", IP: "192.0.2.21", ProviderOptions: config.ConfigEndpointProviderOptions{Cloudflare: &config.ConfigEndpointCloudflareOptions{}}},
			},
		},
		{name: "plain endpoints on every provider", provider: "ovh", endpoints: []*config.ConfigEndpoint{plainEndpoint}},
		{
			name:      "cname on OVH",
			provider:  "ovh",
			endpoints: []*config.ConfigEndpoint{plainEndpoint, cnameEndpoint},
			expectErr: "domain app.example.com endpoint 1 (tunnel) is invalid: cname is not supported by provider 'ovh' (ovh)",
		},
		{
			name:      "cname on Azure",
			provider:  "azure",
			endpoints: []*config.ConfigEndpoint{cnameEndpoint},
			expectErr: "domain app.example.com endpoint 0 (tunnel) is invalid: cname is not supported by provider 'azure' (azure)",
		},
		{
			name:      "cname on AWS Route53",
			provider:  "aws",
			endpoints: []*config.ConfigEndpoint{cnameEndpoint},
			expectErr: "cname is not supported by provider 'aws' (awsRoute53)",
		},
		{
			name:      "cname on Google Cloud DNS",
			provider:  "google",
			endpoints: []*config.ConfigEndpoint{cnameEndpoint},
			expectErr: "cname is not supported by provider 'google' (googleCloudDns)",
		},
		{
			name:      "providerOptions.cloudflare on OVH",
			provider:  "ovh",
			endpoints: []*config.ConfigEndpoint{plainEndpoint, proxiedEndpoint},
			expectErr: "domain app.example.com endpoint 1 (vps-us) is invalid: providerOptions.cloudflare can only be used with a cloudflare provider, but provider 'ovh' is ovh",
		},
		{
			name:      "providerOptions.cloudflare on Azure",
			provider:  "azure",
			endpoints: []*config.ConfigEndpoint{proxiedEndpoint},
			expectErr: "providerOptions.cloudflare can only be used with a cloudflare provider, but provider 'azure' is azure",
		},
		{
			name:      "providerOptions.cloudflare on AWS Route53",
			provider:  "aws",
			endpoints: []*config.ConfigEndpoint{proxiedEndpoint},
			expectErr: "providerOptions.cloudflare can only be used with a cloudflare provider, but provider 'aws' is awsRoute53",
		},
		{
			name:      "providerOptions.cloudflare on Google Cloud DNS",
			provider:  "google",
			endpoints: []*config.ConfigEndpoint{proxiedEndpoint},
			expectErr: "providerOptions.cloudflare can only be used with a cloudflare provider, but provider 'google' is googleCloudDns",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				Domains: []config.ConfigDomain{{
					RecordName: "app.example.com",
					Provider:   tc.provider,
					Endpoints:  tc.endpoints,
				}},
			}

			err := ValidateConfig(cfg, providers)
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}

	t.Run("provider that does not exist", func(t *testing.T) {
		cfg := &config.Config{
			Domains: []config.ConfigDomain{{RecordName: "app.example.com", Provider: "missing"}},
		}
		require.ErrorContains(t, ValidateConfig(cfg, providers), "domain app.example.com references provider 'missing' that does not exist")
	})
}

// The sample config documents the options, so the providers must be able to publish what it asks for
func TestValidateConfig_SampleConfig(t *testing.T) {
	samplePath := filepath.Join("..", "..", "config.sample.yaml")
	t.Setenv("DDUP_CONFIG", samplePath)

	cfg := config.GetDefaultConfig()
	err := configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	require.NoError(t, err)
	require.NoError(t, cfg.Validate(slog.Default()))

	providers := make(map[string]Provider, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		providers[name], err = NewProvider(name, &pc, nil)
		require.NoError(t, err)
	}

	require.NoError(t, ValidateConfig(cfg, providers))
}
