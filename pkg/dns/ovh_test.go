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
func TestOVHProvider(t *testing.T) {
	t.Run("Create record", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()

		// Mock response for getting existing records (empty response)
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record?fieldType=A&subDomain=", &MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating a record
		mockTransport.SetResponse(http.MethodPost, "/1.0/domain/zone/example.com/record", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 12345,
				"fieldType": "A",
				"subDomain": "",
				"target": "1.1.1.1",
				"ttl": 300,
				"zone": "example.com"
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
		assert.Contains(t, getReq.URL.Path, "/1.0/domain/zone/example.com/record")
		assert.Contains(t, getReq.URL.RawQuery, "fieldType=A")
		assert.Contains(t, getReq.URL.RawQuery, "subDomain=")
		assert.Equal(t, "test-key", getReq.Header.Get("X-Ovh-Application"))
		assert.Equal(t, "test-consumer", getReq.Header.Get("X-Ovh-Consumer"))
		assert.NotEmpty(t, getReq.Header.Get("X-Ovh-Signature"))
		assert.NotEmpty(t, getReq.Header.Get("X-Ovh-Timestamp"))

		// Verify the POST request
		postReq := requests[1]
		assert.Equal(t, http.MethodPost, postReq.Method)
		assert.Equal(t, "/1.0/domain/zone/example.com/record", postReq.URL.Path)
		assert.Equal(t, "application/json", postReq.Header.Get("Content-Type"))

		// Read and verify the request body
		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)

		var createReq OVHCreateRecordRequest
		err = json.Unmarshal(body, &createReq)
		require.NoError(t, err)

		assert.Equal(t, "A", createReq.FieldType)
		assert.Empty(t, createReq.SubDomain)
		assert.Equal(t, "1.1.1.1", createReq.Target)
		assert.Equal(t, 300, createReq.TTL)
	})

	t.Run("Delete record", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()

		// Mock response for getting existing records (has one record)
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record?fieldType=A&subDomain=www", &MockResponse{
			StatusCode: 200,
			Body:       `[12345]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for getting record details
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record/12345", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 12345,
				"fieldType": "A",
				"subDomain": "www",
				"target": "1.2.3.4",
				"ttl": 300,
				"zone": "example.com"
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting a record
		mockTransport.SetResponse(http.MethodDelete, "/1.0/domain/zone/example.com/record/12345", &MockResponse{
			StatusCode: 200,
			Body:       `{}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Test deleting records (passing empty IPs array)
		err := provider.UpdateRecords(t.Context(), "www.example.com", 300, []string{})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // Should have made 3 requests: GET (list), GET (details), DELETE

		// Verify the DELETE request
		deleteReq := requests[2]
		assert.Equal(t, http.MethodDelete, deleteReq.Method)
		assert.Equal(t, "/1.0/domain/zone/example.com/record/12345", deleteReq.URL.Path)
	})

	t.Run("Update existing records", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()

		// Mock response for getting existing records (has two records)
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record?fieldType=A&subDomain=api", &MockResponse{
			StatusCode: 200,
			Body:       `[12345, 67890]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for getting first record details
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record/12345", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 12345,
				"fieldType": "A",
				"subDomain": "api",
				"target": "1.2.3.4",
				"ttl": 300,
				"zone": "example.com"
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for getting second record details
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record/67890", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 67890,
				"fieldType": "A",
				"subDomain": "api",
				"target": "5.6.7.8",
				"ttl": 300,
				"zone": "example.com"
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for deleting first record (IP no longer healthy)
		mockTransport.SetResponse(http.MethodDelete, "/1.0/domain/zone/example.com/record/12345", &MockResponse{
			StatusCode: 200,
			Body:       `{}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating new record
		mockTransport.SetResponse(http.MethodPost, "/1.0/domain/zone/example.com/record", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 11111,
				"fieldType": "A",
				"subDomain": "api",
				"target": "9.10.11.12",
				"ttl": 300,
				"zone": "example.com"
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating records with new IPs (keep 5.6.7.8, remove 1.2.3.4, add 9.10.11.12)
		err := provider.UpdateRecords(t.Context(), "api.example.com", 300, []string{"5.6.7.8", "9.10.11.12"})
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 5) // GET (list), GET (details1), GET (details2), DELETE, POST

		// Verify we deleted the right record
		deleteReq := requests[3]
		assert.Equal(t, http.MethodDelete, deleteReq.Method)
		assert.Equal(t, "/1.0/domain/zone/example.com/record/12345", deleteReq.URL.Path)

		// Verify we created a new record
		postReq := requests[4]
		assert.Equal(t, http.MethodPost, postReq.Method)
		body, err := io.ReadAll(postReq.Body)
		require.NoError(t, err)

		var createReq OVHCreateRecordRequest
		err = json.Unmarshal(body, &createReq)
		require.NoError(t, err)
		assert.Equal(t, "9.10.11.12", createReq.Target)
	})

	t.Run("No changes needed", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()

		// Mock response for getting existing records (has one record)
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record?fieldType=A&subDomain=api", &MockResponse{
			StatusCode: 200,
			Body:       `[12345]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for getting record details
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record/12345", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 12345,
				"fieldType": "A",
				"subDomain": "api",
				"target": "1.2.3.4",
				"ttl": 300,
				"zone": "example.com"
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test updating with the same IP (no changes needed)
		err := provider.UpdateRecords(t.Context(), "api.example.com", 300, []string{"1.2.3.4"})
		require.NoError(t, err)

		// Verify only the GET requests were made (no DELETE or POST)
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // GET (list), GET (details)
	})

	t.Run("Multiple IPs for subdomain", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()

		// Mock response for getting existing records (empty)
		mockTransport.SetResponse(http.MethodGet, "/1.0/domain/zone/example.com/record?fieldType=A&subDomain=multi", &MockResponse{
			StatusCode: 200,
			Body:       `[]`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Mock response for creating first record
		mockTransport.SetResponse(http.MethodPost, "/1.0/domain/zone/example.com/record", &MockResponse{
			StatusCode: 200,
			Body: `{
				"id": 11111,
				"fieldType": "A",
				"subDomain": "multi",
				"target": "1.1.1.1",
				"ttl": 300,
				"zone": "example.com"
			}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		})

		// Test creating multiple records for the same subdomain
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
		op1 := (assert.ObjectsAreEqual(bodies[0], `{"fieldType":"A","subDomain":"multi","target":"1.1.1.1","ttl":300}`) &&
			assert.ObjectsAreEqual(bodies[1], `{"fieldType":"A","subDomain":"multi","target":"2.2.2.2","ttl":300}`))
		op2 := (assert.ObjectsAreEqual(bodies[0], `{"fieldType":"A","subDomain":"multi","target":"2.2.2.2","ttl":300}`) &&
			assert.ObjectsAreEqual(bodies[1], `{"fieldType":"A","subDomain":"multi","target":"1.1.1.1","ttl":300}`))
		assert.True(t, op1 || op2)
	})

	t.Run("Different OVH endpoints", func(t *testing.T) {
		tests := []struct {
			name         string
			endpoint     string
			expectedBase string
		}{
			{
				name:         "EU endpoint (default)",
				endpoint:     "eu",
				expectedBase: "https://eu.api.ovh.com/1.0",
			},
			{
				name:         "Empty endpoint (defaults to EU)",
				endpoint:     "",
				expectedBase: "https://eu.api.ovh.com/1.0",
			},
			{
				name:         "CA endpoint",
				endpoint:     "ca",
				expectedBase: "https://ca.api.ovh.com/1.0",
			},
			{
				name:         "US endpoint",
				endpoint:     "us",
				expectedBase: "https://api.us.ovhcloud.com/1.0",
			},
			{
				name:         "Custom endpoint",
				endpoint:     "https://custom.api.example.com/v1",
				expectedBase: "https://custom.api.example.com/v1",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				provider, err := NewOVHProvider("test", &config.OVHConfig{
					APIKey:      "test-key",
					APISecret:   "test-secret",
					ConsumerKey: "test-consumer",
					ZoneName:    "example.com",
					Endpoint:    tt.endpoint,
				}, nil)
				require.NoError(t, err)

				assert.Equal(t, tt.expectedBase, provider.endpoint)
			})
		}
	})

	t.Run("Domain validation", func(t *testing.T) {
		provider, mockTransport := newOVHTestProviderWithMock()

		// Test with domain not in zone
		err := provider.UpdateRecords(t.Context(), "other.com", 300, []string{"1.1.1.1"})
		require.Error(t, err)
		require.ErrorContains(t, err, "is not a subdomain of zone")

		// Verify no requests were made
		requests := mockTransport.GetRequests()
		require.Empty(t, requests)
	})

	t.Run("Signature calculation", func(t *testing.T) {
		provider := &OVHProvider{
			apiSecret:   "test-secret",
			consumerKey: "test-consumer",
		}

		// Test signature calculation with known values
		signature := provider.calculateSignature("GET", "https://eu.api.ovh.com/1.0/domain/zone/example.com/record", "", "1609459200")

		// We just verify it starts with $1$ and has the right format
		assert.True(t, len(signature) > 3 && signature[:3] == "$1$")
		assert.Len(t, signature, 43) // $1$ + 40 char hex string
	})
}

func TestOVHEndpoints(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "https://eu.api.ovh.com/1.0"},
		{"eu", "https://eu.api.ovh.com/1.0"},
		{"ca", "https://ca.api.ovh.com/1.0"},
		{"us", "https://api.us.ovhcloud.com/1.0"},
		{"https://custom.api.example.com/v1", "https://custom.api.example.com/v1"},
		{"https://custom.api.example.com/v1/", "https://custom.api.example.com/v1"}, // Trailing slash removed
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := getOVHEndpoint(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// newOVHTestProviderWithMock creates a test OVH provider with a mock HTTP client
func newOVHTestProviderWithMock() (*OVHProvider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &OVHProvider{
		name:        "test",
		apiKey:      "test-key",
		apiSecret:   "test-secret",
		consumerKey: "test-consumer",
		zoneName:    "example.com",
		endpoint:    getOVHEndpoint("eu"),
		httpClient:  mockClient,
	}

	return provider, mockTransport
}
