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
