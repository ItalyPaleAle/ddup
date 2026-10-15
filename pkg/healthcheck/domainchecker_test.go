package healthcheck

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
)

func TestDedupeResults(t *testing.T) {
	a := &config.ConfigEndpoint{Name: "a", IPLookupFamily: 4}
	b := &config.ConfigEndpoint{Name: "b", IPLookupFamily: 4}
	static := &config.ConfigEndpoint{Name: "static", IP: "198.51.100.1"}
	noAddr := &config.ConfigEndpoint{Name: "none", IPLookupFamily: 6}

	in := []checker.Result{
		{Endpoint: a, Target: "203.0.113.1", Healthy: false},
		{Endpoint: b, Target: "203.0.113.1", Healthy: true},
		{Endpoint: static, Healthy: true},
		{Endpoint: noAddr},
		{Endpoint: noAddr},
	}
	out := dedupeResults(in)
	require.Len(t, out, 4)
	assert.Equal(t, "b", out[0].Endpoint.Name, "a healthy result wins over an unhealthy one")
	assert.Equal(t, "static", out[1].Endpoint.Name)
	assert.Equal(t, "none", out[2].Endpoint.Name, "results without an address are kept")

	assert.Equal(t, "203.0.113.1", resultIP(in[0]))
	assert.Equal(t, "198.51.100.1", resultIP(in[2]))
	assert.Empty(t, resultIP(in[3]))
}

// Uses the real checker and a real lookup against a test service: the address changes between cycles and DNS follows
