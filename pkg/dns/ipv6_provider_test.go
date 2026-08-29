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

func TestProvidersCanonicalizeIPv6BeforeComparison(t *testing.T) {
	const (
		domain       = "canonical.example.com"
		canonicalIP  = "2001:db8::1"
		expandedIP   = "2001:0DB8:0:0:0:0:0:1"
		azureListURL = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/"
	)

	t.Run("Cloudflare", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()
		mockTransport.SetResponse(
			http.MethodGet,
			"/client/v4/zones/test-zone-id/dns_records?name="+domain+"&type=A",
			&MockResponse{StatusCode: http.StatusOK, Body: `{"success":true,"errors":[],"result":[]}`},
		)
		mockTransport.SetResponse(
			http.MethodGet,
			"/client/v4/zones/test-zone-id/dns_records?name="+domain+"&type=AAAA",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body: `{"success":true,"errors":[],"result":[{` +
					`"id":"record-v6","type":"AAAA","name":"canonical.example.com",` +
					`"content":"2001:db8::1","ttl":60}]}`,
			},
		)

		err := provider.UpdateRecords(t.Context(), domain, 60, []string{expandedIP})
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("OVH", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()
		mockTransport.SetResponse(
			http.MethodGet,
			"/1.0/domain/zone/example.com/record?fieldType=A&subDomain=canonical",
			&MockResponse{StatusCode: http.StatusOK, Body: `[]`, Headers: map[string]string{"Content-Type": "application/json"}},
		)
		mockTransport.SetResponse(
			http.MethodGet,
			"/1.0/domain/zone/example.com/record?fieldType=AAAA&subDomain=canonical",
			&MockResponse{StatusCode: http.StatusOK, Body: `[12345]`, Headers: map[string]string{"Content-Type": "application/json"}},
		)
		mockTransport.SetResponse(
			http.MethodGet,
			"/1.0/domain/zone/example.com/record/12345",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body: `{"id":12345,"fieldType":"AAAA","subDomain":"canonical",` +
					`"target":"` + canonicalIP + `","ttl":60,"zone":"example.com"}`,
				Headers: map[string]string{"Content-Type": "application/json"},
			},
		)

		err := provider.UpdateRecords(t.Context(), domain, 60, []string{expandedIP})
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 3)
	})

	t.Run("Azure", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")
		mockTransport.SetResponse(
			http.MethodGet,
			azureListURL+"A?%24recordsetnamesuffix=canonical&api-version=2018-05-01",
			&MockResponse{StatusCode: http.StatusOK, Body: `{"value":[]}`},
		)
		mockTransport.SetResponse(
			http.MethodGet,
			azureListURL+"AAAA?%24recordsetnamesuffix=canonical&api-version=2018-05-01",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body: `{"value":[{"name":"canonical","properties":{"TTL":60,` +
					`"AAAARecords":[{"ipv6Address":"` + canonicalIP + `"}]}}]}`,
			},
		)

		err := provider.UpdateRecords(t.Context(), domain, 60, []string{expandedIP})
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})
}

func TestProvidersCreateBeforeDelete(t *testing.T) {
	const domain = "migration.example.com"

	t.Run("Cloudflare", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()
		mockTransport.SetResponse(
			http.MethodGet,
			"/client/v4/zones/test-zone-id/dns_records?name="+domain+"&type=A",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body: `{"success":true,"errors":[],"result":[{` +
					`"id":"record-v4","type":"A","name":"migration.example.com",` +
					`"content":"192.0.2.1","ttl":60}]}`,
			},
		)
		setCloudflareEmptyAAAAResponse(mockTransport, domain)
		mockTransport.SetResponse(
			http.MethodPost,
			"/client/v4/zones/test-zone-id/dns_records",
			&MockResponse{StatusCode: http.StatusInternalServerError, Body: `{}`},
		)

		err := provider.UpdateRecords(t.Context(), domain, 60, []string{"2001:db8::1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error creating record")

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3)
		assert.Equal(t, http.MethodPost, requests[2].Method)
	})

	t.Run("OVH", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()
		mockTransport.SetResponse(
			http.MethodGet,
			"/1.0/domain/zone/example.com/record?fieldType=A&subDomain=migration",
			&MockResponse{StatusCode: http.StatusOK, Body: `[12345]`, Headers: map[string]string{"Content-Type": "application/json"}},
		)
		setOVHEmptyAAAAResponse(mockTransport, "migration")
		mockTransport.SetResponse(
			http.MethodGet,
			"/1.0/domain/zone/example.com/record/12345",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body:       `{"id":12345,"fieldType":"A","subDomain":"migration","target":"192.0.2.1","ttl":60,"zone":"example.com"}`,
				Headers:    map[string]string{"Content-Type": "application/json"},
			},
		)
		mockTransport.SetResponse(
			http.MethodPost,
			"/1.0/domain/zone/example.com/record",
			&MockResponse{StatusCode: http.StatusInternalServerError, Body: `{}`},
		)

		err := provider.UpdateRecords(t.Context(), domain, 60, []string{"2001:db8::1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error creating record")

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4)
		assert.Equal(t, http.MethodPost, requests[3].Method)
	})

	t.Run("Azure", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")
		baseURL := "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/"
		mockTransport.SetResponse(
			http.MethodGet,
			baseURL+"A?%24recordsetnamesuffix=migration&api-version=2018-05-01",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body: `{"value":[{"name":"migration","properties":{"TTL":60,` +
					`"ARecords":[{"ipv4Address":"192.0.2.1"}]}}]}`,
			},
		)
		mockTransport.SetResponse(
			http.MethodGet,
			baseURL+"AAAA?%24recordsetnamesuffix=migration&api-version=2018-05-01",
			&MockResponse{StatusCode: http.StatusOK, Body: `{"value":[]}`},
		)
		mockTransport.SetResponse(
			http.MethodPut,
			baseURL+"AAAA/migration?api-version=2018-05-01",
			&MockResponse{StatusCode: http.StatusInternalServerError, Body: `{}`},
		)

		err := provider.UpdateRecords(t.Context(), domain, 60, []string{"2001:db8::1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating/updating AAAA record")

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3)
		assert.Equal(t, http.MethodPut, requests[2].Method)
	})
}
