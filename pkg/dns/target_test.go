package dns

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

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
		require.ErrorContains(t, err, `provider test can only publish A and AAAA records that are not proxied, got CNAME record "tunnel.example.com"`)
	})

	t.Run("proxied record", func(t *testing.T) {
		_, err := plainIPsFromTargets("test", []Target{{Type: "A", Value: "192.0.2.10", Proxied: true}})
		require.ErrorIs(t, err, ErrUnsupportedTarget)
	})
}

func TestCapabilitiesFor(t *testing.T) {
	assert.Equal(t, Capabilities{Type: "ovh"}, CapabilitiesFor(&config.ConfigProvider{OVH: &config.OVHConfig{}}))
	assert.Equal(t, Capabilities{Type: "azure"}, CapabilitiesFor(&config.ConfigProvider{Azure: &config.AzureConfig{}}))
	assert.Equal(t, Capabilities{Type: "awsRoute53"}, CapabilitiesFor(&config.ConfigProvider{AWSRoute53: &config.AWSRoute53Config{}}))
	assert.Equal(t, Capabilities{Type: "googleCloudDns"}, CapabilitiesFor(&config.ConfigProvider{GoogleCloudDNS: &config.GoogleCloudDNSConfig{}}))

	assert.Panics(t, func() {
		CapabilitiesFor(&config.ConfigProvider{})
	})
}
