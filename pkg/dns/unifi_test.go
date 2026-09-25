package dns

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

//nolint:maintidx
func TestUnifiProvider(t *testing.T) {
	t.Run("Create record", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response for getting existing records (empty response)
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating a record
		mockTransport.SetResponse(http.MethodPost, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `{
				"_id": "record-123",
				"key": "example.com",
				"record_type": "A",
				"value": "1.1.1.1",
				"ttl": 300,
				"enabled": true
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test creating records
		err := provider.UpdateRecords(t.Context(), "example.com", 300, []string{"1.1.1.1"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // Should have made 2 requests: GET and POST

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Equal(t, "/proxy/network/v2/api/site/default/static-dns", getReq.URL.Path)
		assert.Equal(t, "test-api-key", getReq.Header.Get("X-Api-Key"))
		assert.Equal(t, "application/json", getReq.Header.Get("Content-Type"))

		// Verify the POST request
		postReq := requests[1]
		assert.Equal(t, http.MethodPost, postReq.Method)
		assert.Equal(t, "/proxy/network/v2/api/site/default/static-dns", postReq.URL.Path)
		assert.Equal(t, "test-api-key", postReq.Header.Get("X-Api-Key"))
		assert.Equal(t, "application/json", postReq.Header.Get("Content-Type"))

		// Read and verify the request body
		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)

		var createReq UnifiDNSRecord
		err = json.Unmarshal(body, &createReq)
		require.NoError(t, err)

		assert.Equal(t, "A", createReq.RecordType)
		assert.Equal(t, "example.com", createReq.Key)
		assert.Equal(t, "1.1.1.1", createReq.Value)
		assert.Equal(t, 300, createReq.TTL)
		assert.True(t, createReq.Enabled)
	})

	t.Run("Delete record", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response for getting existing records (has one record)
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `[
				{
					"_id": "record-456",
					"key": "www.example.com",
					"record_type": "A",
					"value": "1.2.3.4",
					"ttl": 300,
					"enabled": true
				}
			]`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting a record
		mockTransport.SetResponse(http.MethodDelete, "/proxy/network/v2/api/site/default/static-dns/record-456", &MockResponse{
			StatusCode: 200,
			Body:       `{"_id": "record-456"}`,
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
		assert.Equal(t, "/proxy/network/v2/api/site/default/static-dns/record-456", deleteReq.URL.Path)
		assert.Equal(t, "test-api-key", deleteReq.Header.Get("X-Api-Key"))
	})

	t.Run("Update existing records", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response for getting existing records (has two records)
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `[
				{
					"_id": "record-789",
					"key": "api.example.com",
					"record_type": "A",
					"value": "1.2.3.4",
					"ttl": 300,
					"enabled": true
				},
				{
					"_id": "record-101",
					"key": "api.example.com",
					"record_type": "A",
					"value": "5.6.7.8",
					"ttl": 300,
					"enabled": true
				}
			]`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting first record (IP no longer healthy)
		mockTransport.SetResponse(http.MethodDelete, "/proxy/network/v2/api/site/default/static-dns/record-789", &MockResponse{
			StatusCode: 200,
			Body:       `{"_id": "record-789"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating new record
		mockTransport.SetResponse(http.MethodPost, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `{
				"_id": "record-999",
				"key": "api.example.com",
				"record_type": "A",
				"value": "9.10.11.12",
				"ttl": 300,
				"enabled": true
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating records with new IPs (keep 5.6.7.8, remove 1.2.3.4, add 9.10.11.12)
		err := provider.UpdateRecords(t.Context(), "api.example.com", 300, []string{"5.6.7.8", "9.10.11.12"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // GET, DELETE, POST

		// Verify we deleted the right record
		deleteReq := requests[1]
		assert.Equal(t, http.MethodDelete, deleteReq.Method)
		assert.Equal(t, "/proxy/network/v2/api/site/default/static-dns/record-789", deleteReq.URL.Path)

		// Verify we created a new record
		postReq := requests[2]
		assert.Equal(t, http.MethodPost, postReq.Method)
		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)

		var createReq UnifiDNSRecord
		err = json.Unmarshal(body, &createReq)
		require.NoError(t, err)
		assert.Equal(t, "9.10.11.12", createReq.Value)
	})

	t.Run("No changes needed", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response for getting existing records (has one record matching desired IP)
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `[
				{
					"_id": "record-789",
					"key": "api.example.com",
					"record_type": "A",
					"value": "1.2.3.4",
					"ttl": 300,
					"enabled": true
				}
			]`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating with the same IP (no changes needed)
		err := provider.UpdateRecords(t.Context(), "api.example.com", 300, []string{"1.2.3.4"})
		require.NoError(t, err)

		// Verify only the GET request was made (no DELETE or POST)
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 1) // GET only
	})

	t.Run("Multiple IPs for domain", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response for getting existing records (empty)
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating records
		mockTransport.SetResponse(http.MethodPost, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `{
				"_id": "record-111",
				"key": "multi.example.com",
				"record_type": "A",
				"value": "1.1.1.1",
				"ttl": 300,
				"enabled": true
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test creating multiple records for the same domain
		err := provider.UpdateRecords(t.Context(), "multi.example.com", 300, []string{"1.1.1.1", "2.2.2.2"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // GET + 2 POST requests

		// Verify both POST requests
		postReq1 := requests[1]
		postReq2 := requests[2]
		assert.Equal(t, http.MethodPost, postReq1.Method)
		assert.Equal(t, http.MethodPost, postReq2.Method)

		// Check that we created records for both IPs
		body1, _ := io.ReadAll(postReq1.Body)
		body2, _ := io.ReadAll(postReq2.Body)

		var rec1, rec2 UnifiDNSRecord
		_ = json.Unmarshal(body1, &rec1)
		_ = json.Unmarshal(body2, &rec2)

		// One should contain 1.1.1.1 and one should contain 2.2.2.2
		ips := []string{rec1.Value, rec2.Value}
		assert.Contains(t, ips, "1.1.1.1")
		assert.Contains(t, ips, "2.2.2.2")
	})

	t.Run("Filter disabled and non-A records", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response with mixed record types
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `[
				{
					"_id": "record-1",
					"key": "example.com",
					"record_type": "A",
					"value": "1.1.1.1",
					"ttl": 300,
					"enabled": true
				},
				{
					"_id": "record-2",
					"key": "example.com",
					"record_type": "A",
					"value": "2.2.2.2",
					"ttl": 300,
					"enabled": false
				},
				{
					"_id": "record-3",
					"key": "example.com",
					"record_type": "CNAME",
					"value": "other.com",
					"ttl": 300,
					"enabled": true
				}
			]`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock delete response
		mockTransport.SetResponse(http.MethodDelete, "/proxy/network/v2/api/site/default/static-dns/record-1", &MockResponse{
			StatusCode: 200,
			Body:       `{"_id": "record-1"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Test updating - should only see the enabled A record
		err := provider.UpdateRecords(t.Context(), "example.com", 300, []string{})
		require.NoError(t, err)

		// Should have deleted only the enabled A record (record-1)
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // GET + DELETE

		deleteReq := requests[1]
		assert.Contains(t, deleteReq.URL.Path, "record-1")
	})

	t.Run("External controller paths", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(true)

		// Mock response for getting existing records (empty response)
		mockTransport.SetResponse(http.MethodGet, "/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating a record
		mockTransport.SetResponse(http.MethodPost, "/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 200,
			Body: `{
				"_id": "record-ext-123",
				"key": "example.com",
				"record_type": "A",
				"value": "1.1.1.1",
				"ttl": 300,
				"enabled": true
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test with external controller
		err := provider.UpdateRecords(t.Context(), "example.com", 300, []string{"1.1.1.1"})
		require.NoError(t, err)

		// Verify external controller paths were used
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2)

		getReq := requests[0]
		assert.Equal(t, "/v2/api/site/default/static-dns", getReq.URL.Path)

		postReq := requests[1]
		assert.Equal(t, "/v2/api/site/default/static-dns", postReq.URL.Path)
	})

	t.Run("Custom site name", func(t *testing.T) {
		mockClient, mockTransport := NewMockHTTPClient()

		provider := &UnifiProvider{
			name:               "test",
			host:               "https://unifi.example.com",
			apiKey:             "test-api-key",
			site:               "custom-site",
			externalController: false,
			httpClient:         mockClient,
		}

		// Mock response
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/custom-site/static-dns", &MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		err := provider.UpdateRecords(t.Context(), "example.com", 300, []string{})
		require.NoError(t, err)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 1)
		assert.Contains(t, requests[0].URL.Path, "custom-site")
	})

	t.Run("HTTP error response", func(t *testing.T) {
		provider, mockTransport := newUnifiTestProviderWithMock(false)

		// Mock response with HTTP error
		mockTransport.SetResponse(http.MethodGet, "/proxy/network/v2/api/site/default/static-dns", &MockResponse{
			StatusCode: 401,
			Body:       `{"code": "unauthorized", "message": "Invalid API key"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Test that HTTP errors are handled
		err := provider.UpdateRecords(t.Context(), "error.example.com", 300, []string{"1.1.1.1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid response status code HTTP 401")
	})

	t.Run("Provider configuration validation", func(t *testing.T) {
		tests := []struct {
			name      string
			config    *config.UnifiConfig
			expectErr string
		}{
			{
				name:      "missing host",
				config:    &config.UnifiConfig{APIKey: "test-key"},
				expectErr: "host is required",
			},
			{
				name:      "missing API key",
				config:    &config.UnifiConfig{Host: "https://unifi.example.com"},
				expectErr: "API key is required",
			},
			{
				name: "valid config",
				config: &config.UnifiConfig{
					Host:   "https://unifi.example.com",
					APIKey: "test-key",
				},
				expectErr: "",
			},
			{
				name: "valid config with defaults",
				config: &config.UnifiConfig{
					Host:   "https://unifi.example.com",
					APIKey: "test-key",
					// Site defaults to "default"
					// ExternalController defaults to false
				},
				expectErr: "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				provider, err := NewUnifiProvider("test", tt.config, nil)
				if tt.expectErr != "" {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.expectErr)
					assert.Nil(t, provider)
				} else {
					require.NoError(t, err)
					assert.NotNil(t, provider)
					assert.Equal(t, "test", provider.Name())
					// Verify site defaults to "default"
					assert.Equal(t, "default", provider.site)
				}
			})
		}
	})

	t.Run("UnifiErrorResponse String method", func(t *testing.T) {
		err := UnifiErrorResponse{
			Code:      "invalid_request",
			Message:   "Invalid request parameters",
			ErrorCode: 400,
		}
		expected := "(400) invalid_request: Invalid request parameters"
		assert.Equal(t, expected, err.String())
	})
}

// newUnifiTestProviderWithMock creates a test Unifi provider with a mock HTTP client
func newUnifiTestProviderWithMock(externalController bool) (*UnifiProvider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &UnifiProvider{
		name:               "test",
		host:               "https://unifi.example.com",
		apiKey:             "test-api-key",
		site:               "default",
		externalController: externalController,
		httpClient:         mockClient,
	}

	return provider, mockTransport
}
