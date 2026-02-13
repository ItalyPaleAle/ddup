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
func TestCloudflareProvider(t *testing.T) {
	t.Run("Create record", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response for getting existing records (empty response)
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=example.com&type=A", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": []
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating a record
		mockTransport.SetResponse(http.MethodPost, "/client/v4/zones/test-zone-id/dns_records", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": {
					"id": "record-123",
					"type": "A",
					"name": "example.com",
					"content": "1.1.1.1",
					"ttl": 300
				}
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
		assert.Contains(t, getReq.URL.Path, "/client/v4/zones/test-zone-id/dns_records")
		assert.Contains(t, getReq.URL.RawQuery, "name=example.com")
		assert.Contains(t, getReq.URL.RawQuery, "type=A")
		assert.Equal(t, "Bearer test-token", getReq.Header.Get("Authorization"))
		assert.Equal(t, "application/json", getReq.Header.Get("Content-Type"))

		// Verify the POST request
		postReq := requests[1]
		assert.Equal(t, http.MethodPost, postReq.Method)
		assert.Equal(t, "/client/v4/zones/test-zone-id/dns_records", postReq.URL.Path)
		assert.Equal(t, "Bearer test-token", postReq.Header.Get("Authorization"))
		assert.Equal(t, "application/json", postReq.Header.Get("Content-Type"))

		// Read and verify the request body
		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)

		var createReq map[string]any
		err = json.Unmarshal(body, &createReq)
		require.NoError(t, err)

		assert.Equal(t, "A", createReq["type"])
		assert.Equal(t, "example.com", createReq["name"])
		assert.Equal(t, "1.1.1.1", createReq["content"])
		assert.EqualValues(t, 300, createReq["ttl"]) // JSON unmarshals numbers as float64
	})

	t.Run("Delete record", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response for getting existing records (has one record)
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=www.example.com&type=A", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": [
					{
						"id": "record-456",
						"type": "A",
						"name": "www.example.com",
						"content": "1.2.3.4",
						"ttl": 300
					}
				]
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting a record
		mockTransport.SetResponse(http.MethodDelete, "/client/v4/zones/test-zone-id/dns_records/record-456", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": {
					"id": "record-456"
				}
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
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
		assert.Equal(t, "/client/v4/zones/test-zone-id/dns_records/record-456", deleteReq.URL.Path)
		assert.Equal(t, "Bearer test-token", deleteReq.Header.Get("Authorization"))
	})

	t.Run("Update existing records", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response for getting existing records (has two records)
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=api.example.com&type=A", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": [
					{
						"id": "record-789",
						"type": "A",
						"name": "api.example.com",
						"content": "1.2.3.4",
						"ttl": 300
					},
					{
						"id": "record-101",
						"type": "A",
						"name": "api.example.com",
						"content": "5.6.7.8",
						"ttl": 300
					}
				]
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting first record (IP no longer healthy)
		mockTransport.SetResponse(http.MethodDelete, "/client/v4/zones/test-zone-id/dns_records/record-789", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": {
					"id": "record-789"
				}
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating new record
		mockTransport.SetResponse(http.MethodPost, "/client/v4/zones/test-zone-id/dns_records", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": {
					"id": "record-999",
					"type": "A",
					"name": "api.example.com",
					"content": "9.10.11.12",
					"ttl": 300
				}
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
		assert.Equal(t, "/client/v4/zones/test-zone-id/dns_records/record-789", deleteReq.URL.Path)

		// Verify we created a new record
		postReq := requests[2]
		assert.Equal(t, http.MethodPost, postReq.Method)
		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)

		var createReq map[string]any
		err = json.Unmarshal(body, &createReq)
		require.NoError(t, err)
		assert.Equal(t, "9.10.11.12", createReq["content"])
	})

	t.Run("No changes needed", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response for getting existing records (has one record matching desired IP)
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=api.example.com&type=A", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": [
					{
						"id": "record-789",
						"type": "A",
						"name": "api.example.com",
						"content": "1.2.3.4",
						"ttl": 300
					}
				]
			}`,
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
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response for getting existing records (empty)
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=multi.example.com&type=A", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": []
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating first record
		mockTransport.SetResponse(http.MethodPost, "/client/v4/zones/test-zone-id/dns_records", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": true,
				"errors": [],
				"result": {
					"id": "record-111",
					"type": "A",
					"name": "multi.example.com",
					"content": "1.1.1.1",
					"ttl": 300
				}
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
		bodies := make([]string, 2)
		body1, _ := io.ReadAll(postReq1.Body)
		body2, _ := io.ReadAll(postReq2.Body)
		bodies[0] = string(body1)
		bodies[1] = string(body2)

		// One should contain 1.1.1.1 and one should contain 2.2.2.2
		op1 := (assert.ObjectsAreEqual(bodies[0], `{"content":"1.1.1.1","name":"multi.example.com","ttl":300,"type":"A"}`) &&
			assert.ObjectsAreEqual(bodies[1], `{"content":"2.2.2.2","name":"multi.example.com","ttl":300,"type":"A"}`))
		op2 := (assert.ObjectsAreEqual(bodies[0], `{"content":"2.2.2.2","name":"multi.example.com","ttl":300,"type":"A"}`) &&
			assert.ObjectsAreEqual(bodies[1], `{"content":"1.1.1.1","name":"multi.example.com","ttl":300,"type":"A"}`))
		assert.True(t, op1 || op2)
	})

	t.Run("API error response", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response with API error
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=error.example.com&type=A", &MockResponse{
			StatusCode: 200,
			Body: `{
				"success": false,
				"errors": [
					{
						"code": 1003,
						"message": "Invalid or missing zone ID."
					}
				],
				"result": []
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test that API errors are properly handled
		err := provider.UpdateRecords(t.Context(), "error.example.com", 300, []string{"1.1.1.1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API error")
		assert.Contains(t, err.Error(), "1003")
		assert.Contains(t, err.Error(), "Invalid or missing zone ID")
	})

	t.Run("HTTP error response", func(t *testing.T) {
		provider, mockTransport := newCloudflareTestProviderWithMock()

		// Mock response with HTTP error
		mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=http-error.example.com&type=A", &MockResponse{
			StatusCode: 401,
			Body:       `{"success": false, "errors": [{"code": 10000, "message": "Authentication error"}]}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Test that HTTP errors are handled (this will succeed in getting records but fail parsing the response)
		err := provider.UpdateRecords(t.Context(), "http-error.example.com", 300, []string{"1.1.1.1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "API error")
	})

	t.Run("Provider configuration validation", func(t *testing.T) {
		tests := []struct {
			name      string
			config    *config.CloudflareConfig
			expectErr string
		}{
			{
				name:      "missing API token",
				config:    &config.CloudflareConfig{ZoneID: "test-zone"},
				expectErr: "API token is required",
			},
			{
				name:      "missing zone ID",
				config:    &config.CloudflareConfig{APIToken: "test-token"},
				expectErr: "zone ID is required",
			},
			{
				name:      "valid config",
				config:    &config.CloudflareConfig{APIToken: "test-token", ZoneID: "test-zone"},
				expectErr: "",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				provider, err := NewCloudflareProvider("test", tt.config, nil)
				if tt.expectErr != "" {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.expectErr)
					assert.Nil(t, provider)
				} else {
					require.NoError(t, err)
					assert.NotNil(t, provider)
					assert.Equal(t, "test", provider.Name())
				}
			})
		}
	})

	t.Run("CloudflareError String method", func(t *testing.T) {
		err := CloudflareError{
			Code:    1003,
			Message: "Invalid or missing zone ID.",
		}
		expected := "(1003) Invalid or missing zone ID."
		assert.Equal(t, expected, err.String())
	})
}

// newCloudflareTestProviderWithMock creates a test Cloudflare provider with a mock HTTP client
func newCloudflareTestProviderWithMock() (*CloudflareProvider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &CloudflareProvider{
		name:       "test",
		apiToken:   "test-token",
		zoneID:     "test-zone-id",
		httpClient: mockClient,
	}

	return provider, mockTransport
}
