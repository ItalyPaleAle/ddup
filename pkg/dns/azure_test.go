package dns

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
)

//nolint:maintidx
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
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "@")

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
		res, err := provider.UpdateRecords(t.Context(), "example.com", 300, testTargets("1.1.1.1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, PUT A

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
		assert.Contains(t, getReq.URL.Path, "/A")
		assert.Equal(t, "Bearer mock-123", getReq.Header.Get("Authorization"))

		// Verify the PUT request
		putReq := requests[3]
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
		provider, mockTransport := newAzureTestProviderWithMock("example.com")

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
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "www")

		// Mock response for deleting a record
		mockTransport.SetResponse(http.MethodDelete, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A/www?api-version=2018-05-01", &MockResponse{
			StatusCode: 200,
			Body:       `{}`,
			Headers:    map[string]string{"Content-Type": "application/json"},
		})

		// Test deleting records (passing empty IPs array)
		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.True(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, DELETE A

		// Verify the DELETE request
		deleteReq := requests[3]
		assert.Equal(t, http.MethodDelete, deleteReq.Method)
		assert.Contains(t, deleteReq.URL.Path, "/A/www")
	})

	t.Run("Does not delete record if already empty", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")

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
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "www")

		// Test deleting records (passing empty IPs array)
		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // GET A, GET AAAA and GET CNAME

		// Verify the GET request
		getReq := requests[0]
		assert.Equal(t, http.MethodGet, getReq.Method)
	})

	t.Run("Update existing record", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")

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
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "api")

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
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("5.6.7.8", "9.10.11.12"))
		require.NoError(t, err)
		assert.True(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, PUT A

		// Verify the PUT request body
		putReq := requests[3]
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
		provider, mockTransport := newAzureTestProviderWithMock("example.com")

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
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "api")

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
		res, err := provider.UpdateRecords(t.Context(), "api.example.com", 300, testTargets("1.2.3.4", "9.8.7.6"))
		require.NoError(t, err)
		assert.False(t, res.Changed)

		// Verify the requests were made
		requests := mockTransport.GetRequests()
		require.Len(t, requests, 3) // GET A, GET AAAA and GET CNAME

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

const testTenantID = "00000000-0000-0000-0000-000000000001"

func TestNewAzureProvider(t *testing.T) {
	valid := func() *config.AzureConfig {
		return &config.AzureConfig{SubscriptionID: "sub", ResourceGroupName: "rg", ZoneName: "example.com", TenantID: testTenantID}
	}

	requiredTests := []struct {
		name      string
		mutate    func(c *config.AzureConfig)
		expectErr string
	}{
		{name: "missing subscription ID", mutate: func(c *config.AzureConfig) { c.SubscriptionID = "" }, expectErr: "subscription ID is required"},
		{name: "missing resource group", mutate: func(c *config.AzureConfig) { c.ResourceGroupName = "" }, expectErr: "resource group name is required"},
		{name: "missing zone name", mutate: func(c *config.AzureConfig) { c.ZoneName = "" }, expectErr: "zone name is required"},
	}

	for _, tc := range requiredTests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(cfg)
			p, err := NewAzureProvider("az", cfg, nil)
			require.ErrorContains(t, err, tc.expectErr)
			assert.Nil(t, p)
		})
	}

	t.Run("service principal", func(t *testing.T) {
		cfg := valid()
		cfg.ClientID = "client"
		cfg.ClientSecret = "secret"
		p, err := NewAzureProvider("az", cfg, nil)
		require.NoError(t, err)
		assert.IsType(t, &azidentity.ClientSecretCredential{}, p.credential)
		assert.Equal(t, "sub", p.subscriptionID)
		assert.Equal(t, "rg", p.resourceGroupName)
		assert.Equal(t, "example.com", p.zoneName)
	})

	t.Run("service principal with invalid tenant ID", func(t *testing.T) {
		cfg := valid()
		cfg.TenantID = "not a tenant"
		cfg.ClientID = "client"
		cfg.ClientSecret = "secret"
		_, err := NewAzureProvider("az", cfg, nil)
		require.ErrorContains(t, err, "error creating service principal credential")
	})

	t.Run("client ID without a secret does not use a service principal", func(t *testing.T) {
		cfg := valid()
		cfg.ClientID = "client"
		p, err := NewAzureProvider("az", cfg, nil)
		require.NoError(t, err)
		assert.IsType(t, &azidentity.DefaultAzureCredential{}, p.credential)
	})

	t.Run("managed identity", func(t *testing.T) {
		cfg := valid()
		cfg.ManagedIdentityClientID = "identity"
		p, err := NewAzureProvider("az", cfg, nil)
		require.NoError(t, err)
		assert.IsType(t, &azidentity.ManagedIdentityCredential{}, p.credential)
	})

	t.Run("default credentials", func(t *testing.T) {
		p, err := NewAzureProvider("az", valid(), nil)
		require.NoError(t, err)
		assert.IsType(t, &azidentity.DefaultAzureCredential{}, p.credential)
	})
}

func TestAzureProviderDeleteError(t *testing.T) {
	setExistingA := func(mockTransport *MockHTTPTransport) {
		mockTransport.SetResponse(http.MethodGet, "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=www&api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusOK,
			Body:       `{"value":[{"name":"www","properties":{"TTL":300,"ARecords":[{"ipv4Address":"1.2.3.4"}]}}]}`,
		})
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "www")
	}
	const deletePath = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A/www?api-version=2018-05-01"

	t.Run("delete fails", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")
		setExistingA(mockTransport)
		mockTransport.SetResponse(http.MethodDelete, deletePath, &MockResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       `{"error":"boom"}`,
		})

		_, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.ErrorContains(t, err, "error deleting A record for domain www.example.com")
		require.ErrorContains(t, err, "HTTP 500")
	})

	t.Run("record already deleted", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")
		setExistingA(mockTransport)
		mockTransport.SetResponse(http.MethodDelete, deletePath, &MockResponse{
			StatusCode: http.StatusNotFound,
			Body:       `{}`,
		})

		res, err := provider.UpdateRecords(t.Context(), "www.example.com", 300, testTargets())
		require.NoError(t, err)
		assert.True(t, res.Changed)
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
//
//nolint:unparam
func newAzureTestProviderWithMock(zoneName string) (*AzureProvider, *MockHTTPTransport) {
	mockClient, mockTransport := NewMockHTTPClient()

	provider := &AzureProvider{
		name:              "test",
		subscriptionID:    "test-sub",
		resourceGroupName: "test-rg",
		zoneName:          zoneName,
		credential:        mockAzureTokenProvider{},
		httpClient:        mockClient,
	}

	return provider, mockTransport
}

func setAzureEmptyAAAAAndCNAMEResponses(mockTransport *MockHTTPTransport, recordName string) {
	for _, recordType := range []string{recordTypeAAAA, recordTypeCNAME} {
		mockTransport.SetResponse(
			http.MethodGet,
			"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/"+recordType+"?%24recordsetnamesuffix="+
				url.QueryEscape(recordName)+"&api-version=2018-05-01",
			&MockResponse{
				StatusCode: http.StatusOK,
				Body:       `{"value":[]}`,
				Headers:    map[string]string{"Content-Type": "application/json"},
			},
		)
	}
}

func TestAzureProviderIPv6(t *testing.T) {
	provider, mockTransport := newAzureTestProviderWithMock("example.com")

	mockTransport.SetResponse(
		http.MethodGet,
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/A?%24recordsetnamesuffix=ipv6&api-version=2018-05-01",
		&MockResponse{StatusCode: 200, Body: `{"value":[]}`},
	)

	setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "ipv6")

	mockTransport.SetResponse(
		http.MethodPut,
		"/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/AAAA/ipv6?api-version=2018-05-01",
		&MockResponse{StatusCode: 200, Body: `{}`},
	)

	_, err := provider.UpdateRecords(t.Context(), "ipv6.example.com", 60, testTargets("2001:db8::30"))
	require.NoError(t, err)

	requests := mockTransport.GetRequests()
	require.Len(t, requests, 4)
	assert.Contains(t, requests[3].URL.Path, "/AAAA/ipv6")

	body, err := io.ReadAll(requests[3].Body)
	require.NoError(t, err)

	var recordSet azureRecordSet
	err = json.Unmarshal(body, &recordSet)
	require.NoError(t, err)

	require.Len(t, recordSet.Properties.AAAARecords, 1)
	assert.Equal(t, "2001:db8::30", recordSet.Properties.AAAARecords[0].IPv6Address)
}

// fakeAzureDNSAPI is a stateful fake of the Azure DNS record sets API, for the zone example.com
// Like the real API, it does not allow a CNAME record set to coexist with other record sets with the same name
type fakeAzureDNSAPI struct {
	lock sync.Mutex
	// Record sets, by type and name
	recordSets map[fakeAzureRecordSetKey]azureRecordProperties
	// Requests that change records, in order, in the form "PUT CNAME app"
	ops []string
}

type fakeAzureRecordSetKey struct {
	recordType string
	name       string
}

const fakeAzureZonePath = "/subscriptions/test-sub/resourceGroups/test-rg/providers/Microsoft.Network/dnsZones/example.com/"

func newFakeAzureDNSAPI() *fakeAzureDNSAPI {
	return &fakeAzureDNSAPI{
		recordSets: make(map[fakeAzureRecordSetKey]azureRecordProperties),
	}
}

// set adds a record set to the fake, in the format of the API
func (f *fakeAzureDNSAPI) set(recordType string, name string, properties string) *fakeAzureDNSAPI {
	var props azureRecordProperties
	err := json.Unmarshal([]byte(properties), &props)
	if err != nil {
		panic(err)
	}
	f.recordSets[fakeAzureRecordSetKey{recordType: recordType, name: name}] = props
	return f
}

func (f *fakeAzureDNSAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	f.lock.Lock()
	defer f.lock.Unlock()

	parts := strings.Split(strings.TrimPrefix(req.URL.Path, fakeAzureZonePath), "/")
	switch {
	case req.Method == http.MethodGet && len(parts) == 1:
		return f.list(parts[0], req.URL.Query().Get("$recordsetnamesuffix")), nil
	case req.Method == http.MethodPut && len(parts) == 2:
		return f.put(req, fakeAzureRecordSetKey{recordType: parts[0], name: parts[1]}), nil
	case req.Method == http.MethodDelete && len(parts) == 2:
		key := fakeAzureRecordSetKey{recordType: parts[0], name: parts[1]}
		f.ops = append(f.ops, "DELETE "+key.recordType+" "+key.name)
		_, ok := f.recordSets[key]
		if !ok {
			return fakeAzureResponse(http.StatusNotFound, `{}`), nil
		}
		delete(f.recordSets, key)
		return fakeAzureResponse(http.StatusOK, `{}`), nil
	default:
		return fakeAzureResponse(http.StatusMethodNotAllowed, `{}`), nil
	}
}

func (f *fakeAzureDNSAPI) list(recordType string, name string) *http.Response {
	res := azureRecordsResponse{Value: []azureRecord{}}
	for key, props := range f.recordSets {
		if key.recordType == recordType && key.name == name {
			res.Value = append(res.Value, azureRecord{Name: key.name, Properties: props})
		}
	}

	body, err := json.Marshal(res)
	if err != nil {
		return fakeAzureResponse(http.StatusInternalServerError, `{}`)
	}
	return fakeAzureResponse(http.StatusOK, string(body))
}

func (f *fakeAzureDNSAPI) put(req *http.Request, key fakeAzureRecordSetKey) *http.Response {
	var rs azureRecordSet
	raw, _ := io.ReadAll(req.Body)
	err := json.Unmarshal(raw, &rs)
	if err != nil {
		return fakeAzureResponse(http.StatusBadRequest, `{}`)
	}

	// A CNAME record set can't coexist with other record sets
	for existing := range f.recordSets {
		if existing.name != key.name || existing.recordType == key.recordType {
			continue
		}
		if existing.recordType == recordTypeCNAME || key.recordType == recordTypeCNAME {
			f.ops = append(f.ops, "PUT "+key.recordType+" "+key.name+" (rejected)")
			return fakeAzureResponse(http.StatusConflict, `{"error":{"code":"Conflict","message":"conflicts with an existing record set"}}`)
		}
	}

	f.ops = append(f.ops, "PUT "+key.recordType+" "+key.name)
	f.recordSets[key] = rs.Properties
	return fakeAzureResponse(http.StatusOK, string(raw))
}

// state returns the record sets as "TYPE name ttl=N values", sorted
func (f *fakeAzureDNSAPI) state() []string {
	res := make([]string, 0, len(f.recordSets))
	for key, props := range f.recordSets {
		values := make([]string, 0)
		for _, r := range props.ARecords {
			values = append(values, r.IPv4Address)
		}
		for _, r := range props.AAAARecords {
			values = append(values, r.IPv6Address)
		}
		if props.CNAMERecord != nil {
			values = append(values, props.CNAMERecord.CNAME)
		}
		res = append(res, fmt.Sprintf("%s %s ttl=%d %s", key.recordType, key.name, props.TTL, strings.Join(values, ",")))
	}
	slices.Sort(res)
	return res
}

func fakeAzureResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newAzureTestProviderWithFake(api *fakeAzureDNSAPI) *AzureProvider {
	provider, _ := newAzureTestProviderWithMock("example.com")
	provider.httpClient = &http.Client{Transport: api}
	return provider
}

func TestAzureProviderCNAME(t *testing.T) {
	tunnel := CNAMETarget("0a1b2c3d.cfargotunnel.com", false)

	t.Run("create a CNAME record set", func(t *testing.T) {
		api := newFakeAzureDNSAPI()
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Empty(t, res.Previous)
		assert.Equal(t, []string{"PUT CNAME app"}, api.ops)
		assert.Equal(t, []string{"CNAME app ttl=120 0a1b2c3d.cfargotunnel.com"}, api.state())
	})

	t.Run("hostnames are compared in canonical form", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("CNAME", "app", `{"TTL":120,"CNAMERecord":{"cname":"0A1B2C3D.cfargotunnel.com."}}`)
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Empty(t, api.ops)
		assert.Equal(t, []string{"0a1b2c3d.cfargotunnel.com"}, res.Previous)
	})

	t.Run("A and AAAA records to a CNAME deletes them first", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("A", "app", `{"TTL":120,"ARecords":[{"ipv4Address":"192.0.2.11"},{"ipv4Address":"192.0.2.10"}]}`).
			set("AAAA", "app", `{"TTL":120,"AAAARecords":[{"ipv6Address":"2001:db8::10"}]}`)
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"192.0.2.10", "192.0.2.11", "2001:db8::10"}, res.Previous)
		assert.Equal(t, []string{"DELETE A app", "DELETE AAAA app", "PUT CNAME app"}, api.ops)
		assert.Equal(t, []string{"CNAME app ttl=120 0a1b2c3d.cfargotunnel.com"}, api.state())
	})

	t.Run("a CNAME to A and AAAA records deletes the CNAME first", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("CNAME", "app", `{"TTL":120,"CNAMERecord":{"cname":"0a1b2c3d.cfargotunnel.com"}}`)
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, testTargets("192.0.2.10", "2001:db8::10"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"0a1b2c3d.cfargotunnel.com"}, res.Previous)
		assert.Equal(t, []string{"DELETE CNAME app", "PUT A app", "PUT AAAA app"}, api.ops)
		assert.Equal(t, []string{
			"A app ttl=120 192.0.2.10",
			"AAAA app ttl=120 2001:db8::10",
		}, api.state())
	})

	t.Run("changing the target of a CNAME overwrites it", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("CNAME", "app", `{"TTL":120,"CNAMERecord":{"cname":"old.cfargotunnel.com"}}`)
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"old.cfargotunnel.com"}, res.Previous)
		assert.Equal(t, []string{"PUT CNAME app"}, api.ops)
		assert.Equal(t, []string{"CNAME app ttl=120 0a1b2c3d.cfargotunnel.com"}, api.state())
	})

	t.Run("without a CNAME, record sets are still created before deleting the others", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("AAAA", "app", `{"TTL":120,"AAAARecords":[{"ipv6Address":"2001:db8::10"}]}`)
		provider := newAzureTestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, testTargets("192.0.2.10"))
		require.NoError(t, err)
		assert.Equal(t, []string{"PUT A app", "DELETE AAAA app"}, api.ops)
	})

	t.Run("a CNAME is deleted when no targets are left", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("CNAME", "app", `{"TTL":120,"CNAMERecord":{"cname":"0a1b2c3d.cfargotunnel.com"}}`)
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, nil)
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"DELETE CNAME app"}, api.ops)
		assert.Empty(t, api.state())
	})

	t.Run("an empty CNAME record set is not deleted", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("CNAME", "app", `{"TTL":120}`)
		provider := newAzureTestProviderWithFake(api)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, nil)
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Empty(t, api.ops)
	})

	t.Run("records for other names are not touched", func(t *testing.T) {
		api := newFakeAzureDNSAPI().
			set("A", "other", `{"TTL":120,"ARecords":[{"ipv4Address":"192.0.2.50"}]}`).
			set("A", "app", `{"TTL":120,"ARecords":[{"ipv4Address":"192.0.2.10"}]}`)
		provider := newAzureTestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.NoError(t, err)
		assert.Equal(t, []string{
			"A other ttl=120 192.0.2.50",
			"CNAME app ttl=120 0a1b2c3d.cfargotunnel.com",
		}, api.state())
	})

	t.Run("an error deleting the A records stops before creating the CNAME", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")
		mockTransport.SetResponse(http.MethodGet, fakeAzureZonePath+"A?%24recordsetnamesuffix=app&api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusOK,
			Body:       `{"value":[{"name":"app","properties":{"TTL":120,"ARecords":[{"ipv4Address":"192.0.2.10"}]}}]}`,
		})
		setAzureEmptyAAAAAndCNAMEResponses(mockTransport, "app")
		mockTransport.SetResponse(http.MethodDelete, fakeAzureZonePath+"A/app?api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       `{"error":"boom"}`,
		})

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.ErrorContains(t, err, "error deleting A record for domain app.example.com")

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, DELETE A
		assert.Equal(t, http.MethodDelete, requests[3].Method)
	})

	t.Run("identities that can't read CNAME records can still publish A records", func(t *testing.T) {
		provider, mockTransport := newAzureTestProviderWithMock("example.com")
		mockTransport.SetResponse(http.MethodGet, fakeAzureZonePath+"A?%24recordsetnamesuffix=app&api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusOK,
			Body:       `{"value":[]}`,
		})
		mockTransport.SetResponse(http.MethodGet, fakeAzureZonePath+"AAAA?%24recordsetnamesuffix=app&api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusOK,
			Body:       `{"value":[]}`,
		})
		mockTransport.SetResponse(http.MethodGet, fakeAzureZonePath+"CNAME?%24recordsetnamesuffix=app&api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusForbidden,
			Body:       `{"error":{"code":"AuthorizationFailed"}}`,
		})
		mockTransport.SetResponse(http.MethodPut, fakeAzureZonePath+"A/app?api-version=2018-05-01", &MockResponse{
			StatusCode: http.StatusOK,
			Body:       `{}`,
		})

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, testTargets("192.0.2.10"))
		require.NoError(t, err)
		assert.True(t, res.Changed)

		requests := mockTransport.GetRequests()
		require.Len(t, requests, 4) // GET A, GET AAAA, GET CNAME, PUT A
		assert.Equal(t, http.MethodPut, requests[3].Method)

		// Publishing a CNAME requires reading CNAME records
		_, err = provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{tunnel})
		require.ErrorContains(t, err, "error getting existing CNAME records: invalid response status code HTTP 403")
	})

	t.Run("proxied targets are not supported", func(t *testing.T) {
		api := newFakeAzureDNSAPI()
		provider := newAzureTestProviderWithFake(api)

		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 120, []Target{CNAMETarget("0a1b2c3d.cfargotunnel.com", true)})
		require.ErrorIs(t, err, ErrUnsupportedTarget)
		assert.Empty(t, api.ops)
	})
}

func TestAzureProviderValidateProviderOptions(t *testing.T) {
	provider := &AzureProvider{zoneName: "example.com"}
	ipEndpoint := &config.ConfigEndpoint{Name: "vps", URL: "https://vps.example.com/health", IP: "192.0.2.10"}
	cnameEndpoint := &config.ConfigEndpoint{Name: "tunnel", URL: "https://tunnel.example.com/health", CNAME: "0a1b2c3d.cfargotunnel.com", Priority: 1}

	tests := []struct {
		name       string
		recordName string
		endpoints  []*config.ConfigEndpoint
		expectErr  string
	}{
		{name: "cname on a subdomain", recordName: "app.example.com", endpoints: []*config.ConfigEndpoint{ipEndpoint, cnameEndpoint}},
		{name: "ip at the apex", recordName: "example.com", endpoints: []*config.ConfigEndpoint{ipEndpoint}},
		{
			name:       "cname at the apex",
			recordName: "example.com",
			endpoints:  []*config.ConfigEndpoint{ipEndpoint, cnameEndpoint},
			expectErr:  "domain example.com endpoint 1 (tunnel) is invalid: cname can't be used at the apex of the zone with provider 'az' (azure)",
		},
		{
			name:       "cname at the apex with a trailing dot",
			recordName: "example.com.",
			endpoints:  []*config.ConfigEndpoint{cnameEndpoint},
			expectErr:  "cname can't be used at the apex of the zone",
		},
		{
			name:       "provider options",
			recordName: "app.example.com",
			endpoints: []*config.ConfigEndpoint{{
				Name:            "vps",
				URL:             "https://vps.example.com/health",
				IP:              "192.0.2.10",
				ProviderOptions: config.ConfigEndpointProviderOptions{Cloudflare: &config.ConfigEndpointCloudflareOptions{}},
			}},
			expectErr: "providerOptions.cloudflare can only be used with a cloudflare provider",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := provider.ValidateProviderOptions(&config.ConfigDomain{RecordName: tc.recordName, Provider: "az", Endpoints: tc.endpoints})
			if tc.expectErr != "" {
				require.ErrorContains(t, err, tc.expectErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
