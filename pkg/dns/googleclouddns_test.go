package dns

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/italypaleale/ddup/pkg/config"
)

const (
	googleCloudDNSTestRRSetsPath  = "/dns/v1/projects/test-project/managedZones/test-zone/rrsets"
	googleCloudDNSTestChangesPath = "/dns/v1/projects/test-project/managedZones/test-zone/changes"
)

//nolint:maintidx
func TestGoogleCloudDNSProvider(t *testing.T) {
	t.Run("Create record", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "www.example.com.", `{"rrsets":[]}`)
		setGoogleCloudDNSChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("2.2.2.2", "1.1.1.1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2) // GET, POST

		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Equal(t, "dns.googleapis.com", getReq.URL.Host)
		assert.Equal(t, "Bearer mock-token", getReq.Header.Get("Authorization"))

		postReq := requests[1]
		assert.Equal(t, http.MethodPost, postReq.Method)
		assert.Equal(t, googleCloudDNSTestChangesPath, postReq.URL.Path)
		assert.Equal(t, "Bearer mock-token", postReq.Header.Get("Authorization"))
		assert.Equal(t, "application/json", postReq.Header.Get("Content-Type"))

		change := parseGoogleCloudDNSChange(t, postReq)
		assert.Empty(t, change.Deletions)
		require.Len(t, change.Additions, 1)
		assert.Equal(t, googleCloudDNSRecordSet{
			Name:    "www.example.com.",
			Type:    recordTypeA,
			TTL:     300,
			Rrdatas: []string{"1.1.1.1", "2.2.2.2"},
		}, change.Additions[0])
	})

	t.Run("Delete record", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "www.example.com.", `{"rrsets":[
			{"kind":"dns#resourceRecordSet","name":"www.example.com.","type":"A","ttl":600,"rrdatas":["9.8.7.6","1.2.3.4"]}
		]}`)
		setGoogleCloudDNSChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "9.8.7.6"}, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2)

		// Deletions must match the existing record set exactly
		change := parseGoogleCloudDNSChange(t, requests[1])
		assert.Empty(t, change.Additions)
		require.Len(t, change.Deletions, 1)
		assert.Equal(t, googleCloudDNSRecordSet{
			Name:    "www.example.com.",
			Type:    recordTypeA,
			TTL:     600,
			Rrdatas: []string{"9.8.7.6", "1.2.3.4"},
		}, change.Deletions[0])
	})

	t.Run("Does not delete record if already empty", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "www.example.com.", `{"rrsets":[]}`)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Len(t, mockTransport.GetRequests(), 1)
	})

	t.Run("No changes with existing record", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "api.example.com.", `{"rrsets":[
			{"name":"api.example.com.","type":"A","ttl":300,"rrdatas":["9.8.7.6","1.2.3.4"]}
		]}`)

		// The order is reversed from the current state, and there's a duplicate
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("1.2.3.4", "9.8.7.6", "1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "9.8.7.6"}, res.Previous)
		assert.Len(t, mockTransport.GetRequests(), 1)
	})

	t.Run("Replaces and deletes in a single change", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "api.example.com.", `{"rrsets":[
			{"name":"api.example.com.","type":"A","ttl":300,"rrdatas":["1.2.3.4"]},
			{"name":"api.example.com.","type":"AAAA","ttl":300,"rrdatas":["2001:db8::1"]},
			{"name":"api.example.com.","type":"TXT","ttl":300,"rrdatas":["\"hello\""]}
		]}`)
		setGoogleCloudDNSChangeResponse(mockTransport, http.StatusOK)

		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 60, testTargets("5.6.7.8", "1.2.3.4"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4", "2001:db8::1"}, res.Previous)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 2)

		change := parseGoogleCloudDNSChange(t, requests[1])
		assert.Equal(t, []googleCloudDNSRecordSet{
			{Name: "api.example.com.", Type: recordTypeA, TTL: 60, Rrdatas: []string{"1.2.3.4", "5.6.7.8"}},
		}, change.Additions)
		assert.Equal(t, []googleCloudDNSRecordSet{
			{Name: "api.example.com.", Type: recordTypeA, TTL: 300, Rrdatas: []string{"1.2.3.4"}},
			{Name: "api.example.com.", Type: recordTypeAAAA, TTL: 300, Rrdatas: []string{"2001:db8::1"}},
		}, change.Deletions)
	})

	t.Run("Follows pagination", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "api.example.com.", `{"rrsets":[
			{"name":"api.example.com.","type":"TXT","ttl":300,"rrdatas":["\"hello\""]}
		],"nextPageToken":"page2"}`)
		mockTransport.SetResponse(http.MethodGet, googleCloudDNSTestRRSetsPath+"?name=api.example.com.&pageToken=page2", &MockResponse{
			StatusCode: http.StatusOK,
			Body:       `{"rrsets":[{"name":"api.example.com.","type":"A","ttl":300,"rrdatas":["1.2.3.4"]}]}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("1.2.3.4"))
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.2.3.4"}, res.Previous)
		assert.Len(t, mockTransport.GetRequests(), 2)
	})

	t.Run("Routing policy is not supported", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "www.example.com.", `{"rrsets":[
			{"name":"www.example.com.","type":"A","ttl":300,"routingPolicy":{"wrr":{"items":[{"weight":1,"rrdatas":["1.2.3.4"]}]}}}
		]}`)

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.2.3.4"))
		require.ErrorContains(t, err, "A record set www.example.com. uses a routing policy, which is not supported")
		assert.Len(t, mockTransport.GetRequests(), 1)
	})

	t.Run("Change fails", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "www.example.com.", `{"rrsets":[]}`)
		setGoogleCloudDNSChangeResponse(mockTransport, http.StatusConflict)

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "error updating records for domain www.example.com")
		require.ErrorContains(t, err, "HTTP 409")
		assert.False(t, res.Changed)
	})

	t.Run("List fails", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "error getting existing records")
		require.ErrorContains(t, err, "HTTP 404")
		assert.Len(t, mockTransport.GetRequests(), 1)
	})

	t.Run("Invalid IP in existing record", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		setGoogleCloudDNSListResponse(mockTransport, "www.example.com.", `{"rrsets":[
			{"name":"www.example.com.","type":"AAAA","ttl":300,"rrdatas":["not-an-ip"]}
		]}`)

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "invalid IP address in existing AAAA record")
	})

	t.Run("Token error", func(t *testing.T) {
		provider, mockTransport := newGoogleCloudDNSTestProviderWithMock()
		provider.tokenSource = errTokenSource{err: errors.New("no token")}

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets("1.1.1.1"))
		require.ErrorContains(t, err, "error getting access token: no token")
		assert.Empty(t, mockTransport.GetRequests())
	})
}

func TestNewGoogleCloudDNSProvider(t *testing.T) {
	valid := func() *config.GoogleCloudDNSConfig {
		return &config.GoogleCloudDNSConfig{ProjectID: "test-project", ManagedZone: "test-zone"}
	}

	t.Run("missing project ID", func(t *testing.T) {
		cfg := valid()
		cfg.ProjectID = ""
		p, err := NewGoogleCloudDNSProvider("gcp", cfg, nil)
		require.ErrorContains(t, err, "project ID is required")
		assert.Nil(t, p)
	})

	t.Run("missing managed zone", func(t *testing.T) {
		cfg := valid()
		cfg.ManagedZone = ""
		p, err := NewGoogleCloudDNSProvider("gcp", cfg, nil)
		require.ErrorContains(t, err, "managed zone is required")
		assert.Nil(t, p)
	})

	t.Run("service account key file", func(t *testing.T) {
		cfg := valid()
		cfg.CredentialsFile = writeGoogleCredentialsFile(t, "service_account")
		p, err := NewGoogleCloudDNSProvider("gcp", cfg, nil)
		require.NoError(t, err)
		assert.NotNil(t, p.tokenSource)
		assert.Equal(t, "test-project", p.projectID)
		assert.Equal(t, "test-zone", p.managedZone)
	})

	t.Run("credentials file that is not a service account key", func(t *testing.T) {
		cfg := valid()
		cfg.CredentialsFile = writeGoogleCredentialsFile(t, "authorized_user")
		_, err := NewGoogleCloudDNSProvider("gcp", cfg, nil)
		require.ErrorContains(t, err, "error loading service account key")
	})

	t.Run("credentials file does not exist", func(t *testing.T) {
		cfg := valid()
		cfg.CredentialsFile = filepath.Join(t.TempDir(), "missing.json")
		_, err := NewGoogleCloudDNSProvider("gcp", cfg, nil)
		require.ErrorContains(t, err, "error reading credentials file")
	})

	t.Run("application default credentials", func(t *testing.T) {
		t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", writeGoogleCredentialsFile(t, "service_account"))
		p, err := NewGoogleCloudDNSProvider("gcp", valid(), nil)
		require.NoError(t, err)
		assert.NotNil(t, p.tokenSource)
	})
}

type errTokenSource struct {
	err error
}

func (e errTokenSource) Token() (*oauth2.Token, error) {
	return nil, e.err
}

// newGoogleCloudDNSTestProviderWithMock creates a test Google Cloud DNS provider with a mock HTTP client
func newGoogleCloudDNSTestProviderWithMock() (*GoogleCloudDNSProvider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &GoogleCloudDNSProvider{
		name:        "test",
		projectID:   "test-project",
		managedZone: "test-zone",
		tokenSource: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "mock-token"}),
		httpClient:  mockClient,
	}

	return provider, mockTransport
}

func setGoogleCloudDNSListResponse(mockTransport *MockHTTPTransport, recordName string, body string) {
	mockTransport.SetResponse(http.MethodGet, googleCloudDNSTestRRSetsPath+"?name="+recordName, &MockResponse{
		StatusCode: http.StatusOK,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "application/json"},
	})
}

func setGoogleCloudDNSChangeResponse(mockTransport *MockHTTPTransport, statusCode int) {
	body := `{"kind":"dns#change","id":"1","status":"pending"}`
	if statusCode != http.StatusOK {
		body = `{"error":{"code":` + strconv.Itoa(statusCode) + `,"message":"boom"}}`
	}
	mockTransport.SetResponse(http.MethodPost, googleCloudDNSTestChangesPath, &MockResponse{
		StatusCode: statusCode,
		Body:       body,
		Headers:    map[string]string{"Content-Type": "application/json"},
	})
}

func parseGoogleCloudDNSChange(t *testing.T, req *http.Request) googleCloudDNSChange {
	t.Helper()

	body, err := io.ReadAll(req.Body)
	require.NoError(t, err)

	var change googleCloudDNSChange
	err = json.Unmarshal(body, &change)
	require.NoError(t, err)
	return change
}

// writeGoogleCredentialsFile writes a credentials file of the given type
// The private key isn't parsed until a token is requested, so it doesn't need to be valid
func writeGoogleCredentialsFile(t *testing.T, credType string) string {
	t.Helper()

	// Disable the "G101: Potential hardcoded credentials" gosec warning because these are fake values
	// #nosec G101
	data, err := json.Marshal(map[string]string{
		"type":          credType,
		"project_id":    "test-project",
		"private_key":   "not-a-real-key",
		"client_email":  "ddup@test-project.iam.gserviceaccount.com",
		"client_id":     "123",
		"client_secret": "secret",
		"refresh_token": "refresh",
		"token_uri":     "https://oauth2.googleapis.com/token",
	})
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "credentials.json")
	err = os.WriteFile(path, data, 0o600)
	require.NoError(t, err)
	return path
}
