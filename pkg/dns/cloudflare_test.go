package dns

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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
		setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, "example.com")

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
		res, err := provider.UpdateRecords(t.Context(), "example.com", 300, testTargets("1.1.1.1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, POST

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Contains(t, getReq.URL.Path, "/client/v4/zones/test-zone-id/dns_records")
		assert.Contains(t, getReq.URL.RawQuery, "name=example.com")
		assert.Contains(t, getReq.URL.RawQuery, "type=A")
		assert.Equal(t, "Bearer test-token", getReq.Header.Get("Authorization"))
		assert.Equal(t, "application/json", getReq.Header.Get("Content-Type"))

		// Verify the POST request
		postReq := requests[3]
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
		setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, "www.example.com")

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
		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.True(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, DELETE

		deleteReq := requests[3]
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
		setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, "api.example.com")

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
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("5.6.7.8", "9.10.11.12"))
		require.NoError(t, err)
		assert.True(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 5) // GET A, GET AAAA, GET CNAME, POST, DELETE

		deleteReq := requests[4]
		assert.Equal(t, http.MethodDelete, deleteReq.Method)
		assert.Equal(t, "/client/v4/zones/test-zone-id/dns_records/record-789", deleteReq.URL.Path)

		// Verify we created a new record
		postReq := requests[3]
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
		setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, "api.example.com")

		// Test updating with the same IP (no changes needed)
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4"}, res.Previous)

		// Verify only the GET request was made (no DELETE or POST)
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // GET A, GET AAAA and GET CNAME
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
		setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, "multi.example.com")

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
		_, err := provider.UpdateRecords(t.Context(), "multi.example.com", 300, testTargets("1.1.1.1", "2.2.2.2"))
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 5) // GET A, GET AAAA, GET CNAME + 2 POST requests

		postReq1 := requests[3]
		postReq2 := requests[4]
		assert.Equal(t, http.MethodPost, postReq1.Method)
		assert.Equal(t, http.MethodPost, postReq2.Method)

		// Check that we created records for both IPs
		bodies := make([]string, 2)
		body1, _ := io.ReadAll(postReq1.Body)
		body2, _ := io.ReadAll(postReq2.Body)
		bodies[0] = string(body1)
		bodies[1] = string(body2)

		// One should contain 1.1.1.1 and one should contain 2.2.2.2
		op1 := (assert.ObjectsAreEqual(bodies[0], `{"content":"1.1.1.1","name":"multi.example.com","proxied":false,"ttl":300,"type":"A"}`) &&
			assert.ObjectsAreEqual(bodies[1], `{"content":"2.2.2.2","name":"multi.example.com","proxied":false,"ttl":300,"type":"A"}`))
		op2 := (assert.ObjectsAreEqual(bodies[0], `{"content":"2.2.2.2","name":"multi.example.com","proxied":false,"ttl":300,"type":"A"}`) &&
			assert.ObjectsAreEqual(bodies[1], `{"content":"1.1.1.1","name":"multi.example.com","proxied":false,"ttl":300,"type":"A"}`))
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
		_, err := provider.UpdateRecords(t.Context(), "error.example.com", 300, testTargets("1.1.1.1"))
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
		_, err := provider.UpdateRecords(t.Context(), "http-error.example.com", 300, testTargets("1.1.1.1"))
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

func TestCloudflareProviderDeleteError(t *testing.T) {
	provider, mockTransport := newCloudflareTestProviderWithMock()
	mockTransport.SetResponse(http.MethodGet, "/client/v4/zones/test-zone-id/dns_records?name=www.example.com&type=A", &MockResponse{
		StatusCode: http.StatusOK,
		Body:       `{"success":true,"errors":[],"result":[{"id":"record-456","type":"A","name":"www.example.com","content":"1.2.3.4","ttl":300}]}`,
	})
	setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport, "www.example.com")
	mockTransport.SetResponse(http.MethodDelete, "/client/v4/zones/test-zone-id/dns_records/record-456", &MockResponse{
		StatusCode: http.StatusInternalServerError,
		Body:       `{"success":false}`,
	})

	_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
	require.ErrorContains(t, err, "error deleting record record-456 for A 1.2.3.4")
	require.ErrorContains(t, err, "HTTP 500")
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

func setCloudflareEmptyAAAAAndCNAMEResponses(mockTransport *MockHTTPTransport, domain string) {
	for _, recordType := range []string{"AAAA", "CNAME"} {
		mockTransport.SetResponse(
			http.MethodGet,
			"/client/v4/zones/test-zone-id/dns_records?name="+domain+"&type="+recordType,
			&MockResponse{
				StatusCode: http.StatusOK,
				Body:       `{"success":true,"errors":[],"result":[]}`,
				Headers:    map[string]string{"Content-Type": "application/json"},
			},
		)
	}
}

// fakeCloudflareAPI is a stateful fake of the Cloudflare DNS records API
// Like the real API, it does not allow a CNAME record to coexist with other records at the same name
type fakeCloudflareAPI struct {
	lock    sync.Mutex
	records []CloudflareRecord
	nextID  int
	// Operations received, in order, in the form "POST CNAME tunnel.example.net"
	ops []string
	// If true, requests to overwrite a record fail
	failPut bool
}

func newFakeCloudflareAPI(records ...CloudflareRecord) *fakeCloudflareAPI {
	f := &fakeCloudflareAPI{records: records}
	for i := range f.records {
		if f.records[i].ID == "" {
			f.records[i].ID = fmt.Sprintf("existing-%d", i)
		}
		if f.records[i].Name == "" {
			f.records[i].Name = "app.example.com"
		}
	}
	return f
}

func (f *fakeCloudflareAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	switch req.Method {
	case http.MethodGet:
		return f.list(req), nil
	case http.MethodPost:
		return f.write(req, ""), nil
	case http.MethodPut:
		if f.failPut {
			f.ops = append(f.ops, "PUT (failed)")
			return fakeCloudflareResponse(http.StatusInternalServerError, `{"success":false}`), nil
		}
		return f.write(req, req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]), nil
	case http.MethodDelete:
		id := req.URL.Path[strings.LastIndex(req.URL.Path, "/")+1:]
		for i, r := range f.records {
			if r.ID == id {
				f.ops = append(f.ops, "DELETE "+r.Type+" "+r.Content)
				f.records = append(f.records[:i], f.records[i+1:]...)
				return fakeCloudflareResponse(http.StatusOK, `{"success":true,"result":{}}`), nil
			}
		}
		return fakeCloudflareResponse(http.StatusNotFound, `{"success":false}`), nil
	default:
		return fakeCloudflareResponse(http.StatusMethodNotAllowed, `{}`), nil
	}
}

func (f *fakeCloudflareAPI) list(req *http.Request) *http.Response {
	name := req.URL.Query().Get("name")
	recordType := req.URL.Query().Get("type")
	result := make([]CloudflareRecord, 0)
	for _, r := range f.records {
		if r.Name == name && r.Type == recordType {
			result = append(result, r)
		}
	}
	body, err := json.Marshal(CloudflareResponse{Success: true, Result: result})
	if err != nil {
		return fakeCloudflareResponse(http.StatusInternalServerError, `{"success":false}`)
	}
	return fakeCloudflareResponse(http.StatusOK, string(body))
}

// write creates a record, or overwrites the one with the given ID
func (f *fakeCloudflareAPI) write(req *http.Request, id string) *http.Response {
	raw, _ := io.ReadAll(req.Body)
	rec := CloudflareRecord{ID: id}
	_ = json.Unmarshal(raw, &rec)

	// A CNAME can't coexist with other records
	for _, r := range f.records {
		if r.ID == id || r.Name != rec.Name {
			continue
		}
		if r.Type == "CNAME" || rec.Type == "CNAME" {
			f.ops = append(f.ops, fmt.Sprintf("%s %s %s (rejected)", req.Method, rec.Type, rec.Content))
			return fakeCloudflareResponse(http.StatusBadRequest, `{"success":false,"errors":[{"code":81053,"message":"record conflict"}]}`)
		}
	}

	f.ops = append(f.ops, fmt.Sprintf("%s %s %s", req.Method, rec.Type, rec.Content))
	if id == "" {
		f.nextID++
		rec.ID = fmt.Sprintf("new-%d", f.nextID)
		f.records = append(f.records, rec)
		return fakeCloudflareResponse(http.StatusOK, `{"success":true,"result":{}}`)
	}
	for i := range f.records {
		if f.records[i].ID == id {
			f.records[i] = rec
		}
	}
	return fakeCloudflareResponse(http.StatusOK, `{"success":true,"result":{}}`)
}

func fakeCloudflareResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// writes returns the operations that changed records, leaving out the ones that failed
func (f *fakeCloudflareAPI) writes() []string {
	return f.ops
}

// state returns the records as "TYPE content proxied ttl", sorted by insertion
func (f *fakeCloudflareAPI) state() []string {
	res := make([]string, len(f.records))
	for i, r := range f.records {
		res[i] = fmt.Sprintf("%s %s proxied=%t ttl=%d", r.Type, r.Content, r.Proxied, r.TTL)
	}
	return res
}

func newCloudflareTestProviderWithFake(api *fakeCloudflareAPI) *CloudflareProvider {
	provider, _ := newCloudflareTestProviderWithMock()
	provider.httpClient = &http.Client{Transport: api}
	return provider
}

func tunnelTarget(proxied bool) Target {
	return CNAMETarget("0a1b2c3d.cfargotunnel.com", proxied)
}

func TestCloudflareProviderTargets(t *testing.T) {
	t.Run("create a proxied CNAME record with automatic TTL", func(t *testing.T) {
		api := newFakeCloudflareAPI()
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(true)})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)
		assert.Equal(t, []string{"POST CNAME 0a1b2c3d.cfargotunnel.com"}, api.writes())
		assert.Equal(t, []string{"CNAME 0a1b2c3d.cfargotunnel.com proxied=true ttl=1"}, api.state())
	})

	t.Run("create a CNAME record that is not proxied with the configured TTL", func(t *testing.T) {
		api := newFakeCloudflareAPI()
		provider := newCloudflareTestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(false)})
		require.NoError(t, err)
		assert.Equal(t, []string{"CNAME 0a1b2c3d.cfargotunnel.com proxied=false ttl=120"}, api.state())
	})

	t.Run("hostnames are compared in canonical form", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "CNAME", Content: "0A1B2C3D.cfargotunnel.com.", TTL: 120})
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(false)})
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Empty(t, api.writes())
		assert.Equal(t, []string{"0a1b2c3d.cfargotunnel.com"}, res.Previous)
	})

	t.Run("proxied A and AAAA records", func(t *testing.T) {
		api := newFakeCloudflareAPI()
		provider := newCloudflareTestProviderWithFake(api)

		targets := []Target{
			{Type: "A", Value: "192.0.2.10", Proxied: true},
			{Type: "AAAA", Value: "2001:db8::10", Proxied: true},
		}
		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, targets)
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{
			"A 192.0.2.10 proxied=true ttl=1",
			"AAAA 2001:db8::10 proxied=true ttl=1",
		}, api.state())
	})

	t.Run("turning proxying on and off overwrites the record, with the right TTL", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "A", Content: "192.0.2.10", TTL: 120})
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{{Type: "A", Value: "192.0.2.10", Proxied: true}})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"PUT A 192.0.2.10"}, api.writes())
		assert.Equal(t, []string{"A 192.0.2.10 proxied=true ttl=1"}, api.state())

		res, err = provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{{Type: "A", Value: "192.0.2.10"}})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"A 192.0.2.10 proxied=false ttl=120"}, api.state())
	})

	t.Run("no writes when records already match", func(t *testing.T) {
		api := newFakeCloudflareAPI(
			CloudflareRecord{Type: "A", Content: "192.0.2.10", TTL: 1, Proxied: true},
			CloudflareRecord{Type: "AAAA", Content: "2001:db8::10", TTL: 1, Proxied: true},
		)
		provider := newCloudflareTestProviderWithFake(api)

		targets := []Target{
			{Type: "A", Value: "192.0.2.10", Proxied: true},
			{Type: "AAAA", Value: "2001:db8::10", Proxied: true},
		}
		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, targets)
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Empty(t, api.writes())
		assert.Equal(t, []string{"192.0.2.10", "2001:db8::10"}, res.Previous)
	})

	t.Run("A records to a CNAME overwrites one record and deletes the others", func(t *testing.T) {
		api := newFakeCloudflareAPI(
			CloudflareRecord{Type: "A", Content: "192.0.2.10", TTL: 120},
			CloudflareRecord{Type: "A", Content: "192.0.2.11", TTL: 120},
			CloudflareRecord{Type: "AAAA", Content: "2001:db8::10", TTL: 120},
		)
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(true)})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"192.0.2.10", "192.0.2.11", "2001:db8::10"}, res.Previous)
		// The record that is overwritten is deleted last, so the name always has records
		assert.Equal(t, []string{
			"DELETE A 192.0.2.11",
			"DELETE AAAA 2001:db8::10",
			"PUT CNAME 0a1b2c3d.cfargotunnel.com",
		}, api.writes())
		assert.Equal(t, []string{"CNAME 0a1b2c3d.cfargotunnel.com proxied=true ttl=1"}, api.state())
	})

	t.Run("a CNAME to A and AAAA records overwrites the CNAME and creates the rest", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "CNAME", Content: "0a1b2c3d.cfargotunnel.com", TTL: 1, Proxied: true})
		provider := newCloudflareTestProviderWithFake(api)

		targets := []Target{
			{Type: "A", Value: "192.0.2.10"},
			{Type: "A", Value: "192.0.2.11"},
			{Type: "AAAA", Value: "2001:db8::10"},
		}
		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, targets)
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"0a1b2c3d.cfargotunnel.com"}, res.Previous)
		assert.Equal(t, []string{
			"PUT A 192.0.2.10",
			"POST A 192.0.2.11",
			"POST AAAA 2001:db8::10",
		}, api.writes())
		assert.Equal(t, []string{
			"A 192.0.2.10 proxied=false ttl=120",
			"A 192.0.2.11 proxied=false ttl=120",
			"AAAA 2001:db8::10 proxied=false ttl=120",
		}, api.state())
	})

	t.Run("changing the target of a CNAME overwrites it", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "CNAME", Content: "old.cfargotunnel.com", TTL: 1, Proxied: true})
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(true)})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"PUT CNAME 0a1b2c3d.cfargotunnel.com"}, api.writes())
	})

	t.Run("A records are replaced next to existing A records, creating before deleting", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "A", Content: "192.0.2.10", TTL: 120})
		provider := newCloudflareTestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{{Type: "A", Value: "192.0.2.11"}})
		require.NoError(t, err)
		assert.Equal(t, []string{"POST A 192.0.2.11", "DELETE A 192.0.2.10"}, api.writes())
	})

	t.Run("a CNAME is deleted when no targets are left", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "CNAME", Content: "0a1b2c3d.cfargotunnel.com", TTL: 1, Proxied: true})
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, nil)
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, api.state())
	})

	t.Run("records for other names are not touched", func(t *testing.T) {
		api := newFakeCloudflareAPI(
			CloudflareRecord{Type: "A", Content: "192.0.2.50", Name: "other.example.com"},
			CloudflareRecord{Type: "A", Content: "192.0.2.10"},
		)
		provider := newCloudflareTestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(true)})
		require.NoError(t, err)
		assert.Equal(t, []string{
			"A 192.0.2.50 proxied=false ttl=0",
			"CNAME 0a1b2c3d.cfargotunnel.com proxied=true ttl=1",
		}, api.state())
	})

	t.Run("falls back to deleting a record that can't be overwritten", func(t *testing.T) {
		api := newFakeCloudflareAPI(CloudflareRecord{Type: "A", Content: "192.0.2.10", TTL: 120})
		api.failPut = true
		provider := newCloudflareTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnelTarget(true)})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{
			"PUT (failed)",
			"DELETE A 192.0.2.10",
			"POST CNAME 0a1b2c3d.cfargotunnel.com",
		}, api.writes())
		assert.Equal(t, []string{"CNAME 0a1b2c3d.cfargotunnel.com proxied=true ttl=1"}, api.state())
	})

	t.Run("duplicate targets are published once", func(t *testing.T) {
		api := newFakeCloudflareAPI()
		provider := newCloudflareTestProviderWithFake(api)

		targets := []Target{
			{Type: "A", Value: "192.0.2.10"},
			{Type: "A", Value: "192.0.2.10"},
		}
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, targets)
		require.NoError(t, err)
		assert.Equal(t, []string{"POST A 192.0.2.10"}, api.writes())
	})
}

func TestCloudflareProviderInvalidTargets(t *testing.T) {
	provider := newCloudflareTestProviderWithFake(newFakeCloudflareAPI())

	t.Run("CNAME with other targets", func(t *testing.T) {
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{
			tunnelTarget(false),
			{Type: "A", Value: "192.0.2.10"},
		})
		require.ErrorContains(t, err, "a CNAME target can't be published together with other targets")
	})

	t.Run("two CNAMEs", func(t *testing.T) {
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{
			tunnelTarget(false),
			CNAMETarget("other.example.net", false),
		})
		require.ErrorContains(t, err, "a CNAME target can't be published together with other targets")
	})

	t.Run("record type does not match the IP address", func(t *testing.T) {
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{{Type: "A", Value: "2001:db8::10"}})
		require.ErrorContains(t, err, `target "2001:db8::10" is not valid for a A record`)
	})

	t.Run("invalid IP address", func(t *testing.T) {
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{{Type: "A", Value: "nope"}})
		require.ErrorContains(t, err, `invalid IP address "nope"`)
	})

	t.Run("unknown record type", func(t *testing.T) {
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{{Type: "TXT", Value: "x"}})
		require.ErrorIs(t, err, ErrUnsupportedTarget)
		require.ErrorContains(t, err, `unknown record type "TXT"`)
	})
}
