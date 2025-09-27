package dns

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MockHTTPTransport provides a mock HTTP transport for testing
type MockHTTPTransport struct {
	responses map[string]*MockResponse
	requests  []*http.Request
}

// MockResponse represents a mock HTTP response
type MockResponse struct {
	StatusCode int
	Body       string
	Headers    map[string]string
}

// NewMockHTTPClient creates a new HTTP client with mock transport
func NewMockHTTPClient() (*http.Client, *MockHTTPTransport) {
	transport := &MockHTTPTransport{
		responses: make(map[string]*MockResponse),
		requests:  make([]*http.Request, 0),
	}

	client := &http.Client{
		Transport: transport,
	}

	return client, transport
}

// RoundTrip implements the http.RoundTripper interface
func (m *MockHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Store the request for inspection
	m.requests = append(m.requests, req)

	// Create a key for the request (method + URL path)
	key := req.Method + " " + req.URL.Path
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}

	// Look for a matching response
	response, exists := m.responses[key]
	if !exists {
		// Return a default 404 response if no mock is configured
		response = &MockResponse{
			StatusCode: 404,
			Body:       `{"error": "Not Found"}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}
	}

	// Create the HTTP response
	httpResp := &http.Response{
		StatusCode: response.StatusCode,
		Status:     fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode)),
		Header:     make(http.Header, len(response.Headers)),
		Body:       io.NopCloser(strings.NewReader(response.Body)),
		Request:    req,
	}

	// Set headers
	for key, value := range response.Headers {
		httpResp.Header.Set(key, value)
	}

	return httpResp, nil
}

// SetResponse sets a mock response for a specific HTTP method and URL path
func (m *MockHTTPTransport) SetResponse(method, urlPath string, response *MockResponse) {
	key := method + " " + urlPath
	m.responses[key] = response
}

// GetRequests returns all requests made to the mock client
func (m *MockHTTPTransport) GetRequests() []*http.Request {
	return m.requests
}

// Reset clears all responses and requests
func (m *MockHTTPTransport) Reset() {
	m.responses = make(map[string]*MockResponse)
	m.requests = make([]*http.Request, 0)
}
