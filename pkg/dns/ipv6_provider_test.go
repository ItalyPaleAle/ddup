package dns

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		mockTransport.SetResponse(
			http.MethodGet,
			"/client/v4/zones/test-zone-id/dns_records?name="+domain+"&type=CNAME",
			&MockResponse{StatusCode: http.StatusOK, Body: `{"success":true,"errors":[],"result":[]}`},
		)

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets(expandedIP))
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 3)
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

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets(expandedIP))
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

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets(expandedIP))
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("AWS Route53", func(t *testing.T) {
		provider, mockTransport := newAWSRoute53TestProviderWithMock()
		setAWSRoute53ListResponse(mockTransport, domain+".", recordTypeA, awsRoute53ListBody())
		setAWSRoute53ListResponse(mockTransport, domain+".", recordTypeAAAA, awsRoute53ListBody(
			awsRoute53RecordSetXML(domain+".", recordTypeAAAA, 60, canonicalIP),
		))

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets(expandedIP))
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("Google Cloud DNS", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, domain+".", `{"rrsets":[`+
			`{"name":"`+domain+`.","type":"AAAA","ttl":60,"rrdatas":["`+canonicalIP+`"]}]}`)

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets(expandedIP))
		require.NoError(t, err)
		assert.Len(t, mockTransport.GetRequests(), 1)
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
		setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, domain)
		mockTransport.SetResponse(
			http.MethodPost,
			"/client/v4/zones/test-zone-id/dns_records",
			&MockResponse{StatusCode: http.StatusInternalServerError, Body: `{}`},
		)

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets("2001:db8::1"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "error creating record")

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4)
		assert.Equal(t, http.MethodPost, requests[3].Method)
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

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets("2001:db8::1"))
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

		_, err := provider.UpdateRecords(t.Context(), domain, 60, testTargets("2001:db8::1"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating/updating AAAA record")

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3)
		assert.Equal(t, http.MethodPut, requests[2].Method)
	})
}
