package dns

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudflareProviderIPv6(t *testing.T) {
	provider, mockTransport := newCloudflareTestProviderWithMock()

	mockTransport.SetResponse(
		http.MethodGet,
		"/client/v4/zones/test-zone-id/dns_records?name=ipv6.example.com&type=A",
		&MockResponse{
			StatusCode: 200,
			Body:       `{"success":true,"errors":[],"result":[]}`,
		},
	)

	mockTransport.SetResponse(
		http.MethodGet,
		"/client/v4/zones/test-zone-id/dns_records?name=ipv6.example.com&type=AAAA",
		&MockResponse{
			StatusCode: 200,
			Body:       `{"success":true,"errors":[],"result":[]}`,
		},
	)

	mockTransport.SetResponse(
		http.MethodPost,
		"/client/v4/zones/test-zone-id/dns_records",
		&MockResponse{
			StatusCode: 200,
			Body:       `{"success":true,"errors":[],"result":{}}`,
		},
	)

	err := provider.UpdateRecords(t.Context(), "ipv6.example.com", 60, []string{"2001:db8::10"})
	require.NoError(t, err)

	requests := mockTransport.GetRequests()
	require.Len(t, requests, 3)

	body, err := io.ReadAll(requests[2].Body)
	require.NoError(t, err)

	var req map[string]any
	err = json.Unmarshal(body, &req)
	require.NoError(t, err)

	assert.Equal(t, recordTypeAAAA, req["type"])
	assert.Equal(t, "2001:db8::10", req["content"])
}

func TestOVHProviderIPv6(t *testing.T) {
	provider, mockTransport := newOVHTestProviderWithMock()

	mockTransport.SetResponse(
		http.MethodGet,
		"/1.0/domain/zone/example.com/record?fieldType=A&subDomain=ipv6",
		&MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
	)

	mockTransport.SetResponse(
		http.MethodGet,
		"/1.0/domain/zone/example.com/record?fieldType=AAAA&subDomain=ipv6",
		&MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
	)

	mockTransport.SetResponse(
		http.MethodPost,
		"/1.0/domain/zone/example.com/record",
		&MockResponse{
			StatusCode: 200,
			Body:       `{}`,
			Headers: map[string]string{
				"Content-Type": "application/json",
			},
		},
	)

	err := provider.UpdateRecords(t.Context(), "ipv6.example.com", 60, []string{"2001:db8::20"})
	require.NoError(t, err)

	requests := mockTransport.GetRequests()
	require.Len(t, requests, 3)

	body, err := io.ReadAll(requests[2].Body)
	require.NoError(t, err)

	var req OVHCreateRecordRequest
	err = json.Unmarshal(body, &req)
	require.NoError(t, err)

	assert.Equal(t, recordTypeAAAA, req.FieldType)
	assert.Equal(t, "2001:db8::20", req.Target)
}

func TestAzureProviderIPv6(t *testing.T) {
	provider, mockTransport := newAzureTestProviderWithMock("example.com")

	mockTransport.SetResponse(
		http.MethodGet,
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=ipv6&api-version=2018-05-01",
		&MockResponse{StatusCode: 200, Body: `{"value":[]}`},
	)

	mockTransport.SetResponse(
		http.MethodGet,
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/AAAA?%24recordsetnamesuffix=ipv6&api-version=2018-05-01",
		&MockResponse{StatusCode: 200, Body: `{"value":[]}`},
	)

	mockTransport.SetResponse(
		http.MethodPut,
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/AAAA/ipv6?api-version=2018-05-01",
		&MockResponse{StatusCode: 200, Body: `{}`},
	)

	err := provider.UpdateRecords(t.Context(), "ipv6.example.com", 60, []string{"2001:db8::30"})
	require.NoError(t, err)

	requests := mockTransport.GetRequests()
	require.Len(t, requests, 3)
	assert.Contains(t, requests[2].URL.Path, "/AAAA/ipv6")

	body, err := io.ReadAll(requests[2].Body)
	require.NoError(t, err)

	var recordSet azureRecordSet
	err = json.Unmarshal(body, &recordSet)
	require.NoError(t, err)

	require.Len(t, recordSet.Properties.AAAARecords, 1)
	assert.Equal(t, "2001:db8::30", recordSet.Properties.AAAARecords[0].IPv6Address)
}
