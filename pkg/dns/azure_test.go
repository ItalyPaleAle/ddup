package dns

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzureProvider(t *testing.T) {
	t.Run("Create record", func(t *testing.T) {
		// Create a mock HTTP client
		mockClient, mockTransport := NewMockHTTPClient()

		// Create a test provider with mock credential
		provider := &AzureProvider{
			name:              "test",
			subscriptionID:    "test-sub",
			resourceGroupName: "test-rg",
			zoneName:          "example.com",
			credential:        mockAzureTokenProvider{},
			httpClient:        mockClient,
		}

		// Mock response for getting existing records (empty response)
		mockTransport.SetResponse(http.MethodGet, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=%40&api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
				"value": []
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating/updating a record
		mockTransport.SetResponse(http.MethodPut, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A/@?api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
				"name": "@",
				"properties": {
					"TTL": 300,
					"ARecords": [
						{"ipv4Address": "1.1.1.1"}
					]
				}
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating records
		err := provider.UpdateRecords(t.Context(), "example.com", 300, []string{"1.1.1.1"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // Should have made 2 requests: GET and PUT

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Contains(t, getReq.URL.Path, "/A")
		assert.Equal(t, "Bearer mock-123", getReq.Header.Get("Authorization"))

		// Verify the PUT request
		putReq := requests[1]
		assert.Equal(t, http.MethodPut, putReq.Method)
		assert.Contains(t, putReq.URL.Path, "/A/@")
		assert.Equal(t, "Bearer mock-123", putReq.Header.Get("Authorization"))

		// Read and verify the request body
		body, err := io.ReadAll(putReq.Body)
		require.NoError(t, err)

		var recordSet azureRecordSet
		err = json.Unmarshal(body, &recordSet)
		require.NoError(t, err)

		assert.Equal(t, 300, recordSet.Properties.TTL)
		require.Len(t, recordSet.Properties.ARecords, 1)
		assert.Equal(t, "1.1.1.1", recordSet.Properties.ARecords[0].IPv4Address)
	})

	t.Run("Delete record", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("test-sub", "test-rg", "example.com")

		// Mock response for getting existing records (has one record)
		mockTransport.SetResponse(http.MethodGet, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=www&api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
				"value": [
					{
						"name": "www",
						"properties": {
							"TTL": 300,
							"ARecords": [
								{"ipv4Address": "1.2.3.4"}
							]
						}
					}
				]
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting a record
		mockTransport.SetResponse(http.MethodDelete, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A/www?api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body:       `{}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Test deleting records (passing empty IPs array)
		err := provider.UpdateRecords(t.Context(), "www.example.com", 300, []string{})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // Should have made 2 requests: GET and DELETE

		// Verify the DELETE request
		deleteReq := requests[1]
		assert.Equal(t, http.MethodDelete, deleteReq.Method)
		assert.Contains(t, deleteReq.URL.Path, "/A/www")
	})

	t.Run("Does not delete record if already empty", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("test-sub", "test-rg", "example.com")

		// Mock response for getting existing records (has one record)
		mockTransport.SetResponse(http.MethodGet, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=www&api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
				"value": [
					{
						"name": "www",
						"properties": {
							"TTL": 300,
							"ARecords": []
						}
					}
				]
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test deleting records (passing empty IPs array)
		err := provider.UpdateRecords(t.Context(), "www.example.com", 300, []string{})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 1) // Should have made GET request only

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
	})

	t.Run("Update existing record", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("test-sub", "test-rg", "example.com")

		// Mock response for getting existing records (has one record with different IP)
		mockTransport.SetResponse(http.MethodGet, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=api&api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
			"value": [
				{
					"name": "api",
					"properties": {
						"TTL": 300,
						"ARecords": [
							{"ipv4Address": "1.2.3.4"}
						]
					}
				}
			]
		}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for updating the record
		mockTransport.SetResponse(http.MethodPut, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A/api?api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
			"name": "api",
			"properties": {
				"TTL": 300,
				"ARecords": [
					{"ipv4Address": "5.6.7.8"},
					{"ipv4Address": "9.10.11.12"}
				]
			}
		}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating records with new IPs
		err := provider.UpdateRecords(t.Context(), "api.example.com", 300, []string{"5.6.7.8", "9.10.11.12"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // Should have made 2 requests: GET and PUT

		// Verify the PUT request body
		putReq := requests[1]
		body, err := io.ReadAll(putReq.Body)
		require.NoError(t, err)

		var recordSet azureRecordSet
		err = json.Unmarshal(body, &recordSet)
		require.NoError(t, err)

		assert.Equal(t, 300, recordSet.Properties.TTL)
		require.Len(t, recordSet.Properties.ARecords, 2)
		assert.Equal(t, "5.6.7.8", recordSet.Properties.ARecords[0].IPv4Address)
		assert.Equal(t, "9.10.11.12", recordSet.Properties.ARecords[1].IPv4Address)
	})

	t.Run("No changes with existing record", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("test-sub", "test-rg", "example.com")

		// Mock response for getting existing records (has one record with different IP)
		mockTransport.SetResponse(http.MethodGet, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=api&api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
			"value": [
				{
					"name": "api",
					"properties": {
						"TTL": 300,
						"ARecords": [
							{"ipv4Address": "9.8.7.6"},
							{"ipv4Address": "1.2.3.4"}
						]
					}
				}
			]
		}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for updating the record
		mockTransport.SetResponse(http.MethodPut, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A/api?api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body: `{
			"name": "api",
			"properties": {
				"TTL": 300,
				"ARecords": [
					{"ipv4Address": "1.2.3.4"},
					{"ipv4Address": "9.8.7.6"}
				]
			}
		}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating records with new IPs
		// Note the order is reversed from the current state
		err := provider.UpdateRecords(t.Context(), "api.example.com", 300, []string{"1.2.3.4", "9.8.7.6"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 1) // Should have made GET request only

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
	})

	t.Run("getRecordName method", func(t *testing.T) {
		// Create a test provider
		provider := &AzureProvider{
			zoneName: "example.com",
		}

		tests := []struct {
			name     string
			domain   string
			zoneName string
			expected string
		}{
			{
				name:     "root domain",
				domain:   "example.com",
				zoneName: "example.com",
				expected: "@",
			},
			{
				name:     "root domain with trailing dot",
				domain:   "example.com.",
				zoneName: "example.com",
				expected: "@",
			},
			{
				name:     "subdomain",
				domain:   "www.example.com",
				zoneName: "example.com",
				expected: "www",
			},
			{
				name:     "subdomain with trailing dot",
				domain:   "www.example.com.",
				zoneName: "example.com",
				expected: "www",
			},
			{
				name:     "nested subdomain",
				domain:   "api.v1.example.com",
				zoneName: "example.com",
				expected: "api.v1",
			},
			{
				name:     "nested subdomain with trailing dot",
				domain:   "api.v1.example.com.",
				zoneName: "example.com",
				expected: "api.v1",
			},
			{
				name:     "domain not in zone",
				domain:   "other.com",
				zoneName: "example.com",
				expected: "other.com",
			},
			{
				name:     "partial match not in zone",
				domain:   "notexample.com",
				zoneName: "example.com",
				expected: "notexample.com",
			},
			{
				name:     "empty domain",
				domain:   "",
				zoneName: "example.com",
				expected: "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Update the provider's zone name for this test
				provider.zoneName = tt.zoneName

				result := provider.getRecordName(tt.domain)
				assert.Equal(t, tt.expected, result)
			})
		}
	})
}

type mockAzureTokenProvider struct{}

func (mockAzureTokenProvider) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "mock-123",
		ExpiresOn: time.Now().Add(time.Hour),
	}, nil
}

// newAzureTestProviderWithMock creates a test Azure provider with a mock HTTP client
func newAzureTestProviderWithMock(subscriptionID, resourceGroup, zoneName string) (*AzureProvider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &AzureProvider{
		name:              "test",
		subscriptionID:    subscriptionID,
		resourceGroupName: resourceGroup,
		zoneName:          zoneName,
		credential:        mockAzureTokenProvider{},
		httpClient:        mockClient,
	}

	return provider, mockTransport
}
