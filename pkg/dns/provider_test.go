package dns

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

func TestNewProvider(t *testing.T) {
	t.Run("Cloudflare", func(t *testing.T) {
		p, err := NewProvider("cf", &config.ConfigProvider{
			Cloudflare: &config.CloudflareConfig{APIToken: "token", ZoneID: "zone"},
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &CloudflareProvider{}, p)
		assert.Equal(t, "cf", p.Name())

		_, err = NewProvider("cf", &config.ConfigProvider{Cloudflare: &config.CloudflareConfig{}}, nil)
		require.ErrorContains(t, err, "error initializing Cloudflare provider")
	})

	t.Run("OVH", func(t *testing.T) {
		p, err := NewProvider("ovh", &config.ConfigProvider{
			OVH: &config.OVHConfig{APIKey: "key", APISecret: "secret", ConsumerKey: "consumer", ZoneName: "example.com"},
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &OVHProvider{}, p)
		assert.Equal(t, "ovh", p.Name())

		_, err = NewProvider("ovh", &config.ConfigProvider{OVH: &config.OVHConfig{}}, nil)
		require.ErrorContains(t, err, "error initializing OVH provider")
	})

	t.Run("Azure", func(t *testing.T) {
		p, err := NewProvider("az", &config.ConfigProvider{
			Azure: &config.AzureConfig{
				SubscriptionID:    "sub",
				ResourceGroupName: "rg",
				ZoneName:          "example.com",
				TenantID:          testTenantID,
				ClientID:          "client",
				ClientSecret:      "secret",
			},
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &AzureProvider{}, p)
		assert.Equal(t, "az", p.Name())

		_, err = NewProvider("az", &config.ConfigProvider{Azure: &config.AzureConfig{}}, nil)
		require.ErrorContains(t, err, "error initializing Azure provider")
	})

	t.Run("AWS Route53", func(t *testing.T) {
		p, err := NewProvider("r53", &config.ConfigProvider{
			AWSRoute53: &config.AWSRoute53Config{HostedZoneID: "ZTEST", AccessKeyID: "AKIDTEST", SecretAccessKey: "secret"},
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &AWSRoute53Provider{}, p)
		assert.Equal(t, "r53", p.Name())

		_, err = NewProvider("r53", &config.ConfigProvider{AWSRoute53: &config.AWSRoute53Config{}}, nil)
		require.ErrorContains(t, err, "error initializing AWS Route53 provider")
	})

	t.Run("Google Cloud DNS", func(t *testing.T) {
		p, err := NewProvider("gcp", &config.ConfigProvider{
			GoogleCloudDNS: &config.GoogleCloudDNSConfig{
				ProjectID:       "test-project",
				ManagedZone:     "test-zone",
				CredentialsFile: writeGoogleCredentialsFile(t, "service_account"),
			},
		}, nil)
		require.NoError(t, err)
		assert.IsType(t, &GoogleCloudDNSProvider{}, p)
		assert.Equal(t, "gcp", p.Name())

		_, err = NewProvider("gcp", &config.ConfigProvider{GoogleCloudDNS: &config.GoogleCloudDNSConfig{}}, nil)
		require.ErrorContains(t, err, "error initializing Google Cloud DNS provider")
	})

	t.Run("no provider configured", func(t *testing.T) {
		// Config validation ensures this does not happen
		assert.Panics(t, func() {
			_, _ = NewProvider("none", &config.ConfigProvider{}, nil)
		})
	})
}

func TestIPTarget(t *testing.T) {
	t.Run("IPv4 is an A record", func(t *testing.T) {
		target, err := IPTarget("192.0.2.10", false)
		require.NoError(t, err)
		assert.Equal(t, Target{Type: "A", Value: "192.0.2.10"}, target)
	})

	t.Run("IPv6 is an AAAA record with a canonical value", func(t *testing.T) {
		target, err := IPTarget("2001:0db8:0000:0000:0000:0000:0000:0010", true)
		require.NoError(t, err)
		assert.Equal(t, Target{Type: "AAAA", Value: "2001:db8::10", Proxied: true}, target)
	})

	t.Run("invalid IP", func(t *testing.T) {
		_, err := IPTarget("not-an-ip", false)
		require.ErrorContains(t, err, `invalid IP address "not-an-ip"`)
	})
}

func TestCNAMETarget(t *testing.T) {
	target := CNAMETarget("0A1B2C3D.cfargotunnel.com.", true)
	assert.Equal(t, Target{Type: "CNAME", Value: "0a1b2c3d.cfargotunnel.com", Proxied: true}, target)
	assert.Equal(t, "CNAME 0a1b2c3d.cfargotunnel.com", target.String())
}

func TestPlainIPsFromTargets(t *testing.T) {
	t.Run("A and AAAA records", func(t *testing.T) {
		ips, err := plainIPsFromTargets("test", testTargets("192.0.2.10", "2001:db8::10"))
		require.NoError(t, err)
		assert.Equal(t, []string{"192.0.2.10", "2001:db8::10"}, ips)
	})

	t.Run("CNAME record", func(t *testing.T) {
		_, err := plainIPsFromTargets("test", []Target{CNAMETarget("tunnel.example.com", false)})
		require.ErrorIs(t, err, ErrUnsupportedTarget)
		require.ErrorContains(t, err, `provider test can only publish A and AAAA records, got CNAME record "tunnel.example.com"`)
	})

	t.Run("proxied record", func(t *testing.T) {
		_, err := plainIPsFromTargets("test", []Target{{Type: "A", Value: "192.0.2.10", Proxied: true}})
		require.ErrorIs(t, err, ErrUnsupportedTarget)
	})
}

func TestProviderTypeAndCNAME(t *testing.T) {
	tests := []struct {
		provider     Provider
		providerType string
		cname        bool
	}{
		{&CloudflareProvider{}, "cloudflare", true},
		{&OVHProvider{}, "ovh", false},
		{&AzureProvider{}, "azure", false},
		{&AWSRoute53Provider{}, "awsRoute53", false},
		{&GoogleCloudDNSProvider{}, "googleCloudDns", false},
	}
	for _, tc := range tests {
		t.Run(tc.providerType, func(t *testing.T) {
			assert.Equal(t, tc.providerType, tc.provider.ProviderType())
			assert.Equal(t, tc.cname, tc.provider.HasCNAME())
		})
	}
}
