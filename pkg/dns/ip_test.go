package dns

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordTypeForIP(t *testing.T) {
	tests := []struct {
		name       string
		ip         string
		recordType string
		wantErr    bool
	}{
		{name: "IPv4", ip: "192.0.2.10", recordType: recordTypeA},
		{name: "IPv6", ip: "2001:db8::10", recordType: recordTypeAAAA},
		{name: "invalid", ip: "not-an-ip", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := recordTypeForIP(tc.ip)

			if tc.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.recordType, got)
		})
	}
}

func TestSplitIPs(t *testing.T) {
	ipv4, ipv6, err := splitIPs([]string{
		"192.0.2.1",
		"2001:db8::1",
		"198.51.100.2",
		"2001:db8::2",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"192.0.2.1", "198.51.100.2"}, ipv4)
	assert.Equal(t, []string{"2001:db8::1", "2001:db8::2"}, ipv6)
}
