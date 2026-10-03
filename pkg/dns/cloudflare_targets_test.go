//go:build unit

package dns

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type cfRecordFixture struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func cfRecordJSON(id, recordType, content string, proxied bool) string {
	b, err := json.Marshal(cfRecordFixture{ID: id, Type: recordType, Name: "app.example.com", Content: content, TTL: 60, Proxied: proxied})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func cfOK(mockTransport *MockHTTPTransport, method, path string) {
	mockTransport.SetResponse(method, path, &MockResponse{StatusCode: http.StatusOK, Body: `{"success":true,"errors":[],"result":{}}`})
}

// writes returns the requests that are not GETs, as "METHOD path-suffix" plus the decoded body
type cfWrite struct {
	Method string
	ID     string
	Body   map[string]any
}

func cfWrites(t *testing.T, mockTransport *MockHTTPTransport) []cfWrite {
	t.Helper()
	var res []cfWrite
	for _, req := range mockTransport.GetRequests() {
		if req.Method == http.MethodGet {
			continue
		}
		w := cfWrite{Method: req.Method}
		const prefix = "/client/v4/zones/test-zone-id/dns_records"
		if len(req.URL.Path) > len(prefix) {
			w.ID = req.URL.Path[len(prefix)+1:]
		}
		if req.Body != nil {
			raw, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			if len(raw) > 0 {
				require.NoError(t, json.Unmarshal(raw, &w.Body))
			}
		}
		res = append(res, w)
	}
	return res
}

// cfSetExisting mocks the existing A records of the domain (there are never AAAA records in these tests)
func cfSetExisting(mockTransport *MockHTTPTransport, a []string) {
	setCloudflareRecordsResponse(mockTransport, "app.example.com", "A", strings.Join(a, ","))
	setCloudflareRecordsResponse(mockTransport, "app.example.com", "AAAA", "")
}

func TestCloudflareProvider_Targets(t *testing.T) {
	const dnsPath = "/client/v4/zones/test-zone-id/dns_records"

	t.Run("create proxied A record", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, nil)
		cfOK(mt, http.MethodPost, dnsPath)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "1.1.1.1", Proxied: true}})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 1)
		assert.Equal(t, http.MethodPost, w[0].Method)
		assert.Equal(t, "A", w[0].Body["type"])
		assert.Equal(t, "1.1.1.1", w[0].Body["content"])
		assert.Equal(t, true, w[0].Body["proxied"])
		assert.EqualValues(t, 1, w[0].Body["ttl"], "proxied records use automatic TTL")
	})

	t.Run("unchanged proxied record writes nothing", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, []string{cfRecordJSON("a1", "A", "1.1.1.1", true)})

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "1.1.1.1", Proxied: true}})
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"1.1.1.1"}, res.Previous)
		assert.Empty(t, cfWrites(t, mt))
	})

	t.Run("changing proxied status updates the record in place", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, []string{cfRecordJSON("a1", "A", "1.1.1.1", false)})
		cfOK(mt, http.MethodPut, dnsPath+"/a1")

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "1.1.1.1", Proxied: true}})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 1)
		assert.Equal(t, http.MethodPut, w[0].Method)
		assert.Equal(t, true, w[0].Body["proxied"])
	})

	t.Run("replaces records that are no longer desired", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, []string{cfRecordJSON("a1", "A", "1.1.1.1", false), cfRecordJSON("a2", "A", "2.2.2.2", false)})
		cfOK(mt, http.MethodPost, dnsPath)
		cfOK(mt, http.MethodDelete, dnsPath+"/a2")

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, IPTargets("1.1.1.1", "3.3.3.3"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 2)
		assert.Equal(t, http.MethodPost, w[0].Method, "the new record is created before the stale one is removed")
		assert.Equal(t, cfWrite{Method: http.MethodDelete, ID: "a2"}, w[1])
	})
}

func TestProviders_RejectUnsupportedTargets(t *testing.T) {
	ovh, _ := newOVHTestProviderWithMock()
	_, err := ovh.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "1.1.1.1", Proxied: true}})
	require.ErrorIs(t, err, ErrUnsupportedTarget)
}
