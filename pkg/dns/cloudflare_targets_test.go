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

// cfSetExisting mocks the existing A and CNAME records of the domain (there are never AAAA records in these tests)
func cfSetExisting(mockTransport *MockHTTPTransport, a, cname []string) {
	setCloudflareRecordsResponse(mockTransport, "app.example.com", "A", strings.Join(a, ","))
	setCloudflareRecordsResponse(mockTransport, "app.example.com", "AAAA", "")
	setCloudflareRecordsResponse(mockTransport, "app.example.com", "CNAME", strings.Join(cname, ","))
}

func TestCloudflareProvider_Targets(t *testing.T) {
	const dnsPath = "/client/v4/zones/test-zone-id/dns_records"
	cname := Target{Value: "Tunnel.Example.com.", Proxied: true}

	t.Run("create proxied CNAME", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, nil, nil)
		cfOK(mt, http.MethodPost, dnsPath)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{cname})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 1)
		assert.Equal(t, http.MethodPost, w[0].Method)
		assert.Equal(t, "CNAME", w[0].Body["type"])
		assert.Equal(t, "tunnel.example.com", w[0].Body["content"], "hostname is canonicalized")
		assert.Equal(t, true, w[0].Body["proxied"])
		assert.EqualValues(t, 1, w[0].Body["ttl"], "proxied records use automatic TTL")
	})

	t.Run("unchanged CNAME writes nothing", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, nil, []string{cfRecordJSON("c1", "CNAME", "tunnel.example.com", true)})

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{cname})
		require.NoError(t, err)
		assert.False(t, res.Changed)
		assert.Equal(t, []string{"tunnel.example.com"}, res.Previous)
		assert.Empty(t, cfWrites(t, mt))
	})

	t.Run("A records to CNAME converts one record in place after deleting the others", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, []string{cfRecordJSON("a1", "A", "1.1.1.1", false), cfRecordJSON("a2", "A", "2.2.2.2", false)}, nil)
		cfOK(mt, http.MethodDelete, dnsPath+"/a2")
		cfOK(mt, http.MethodPut, dnsPath+"/a1")

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{cname})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		assert.Equal(t, []string{"1.1.1.1", "2.2.2.2"}, res.Previous)

		w := cfWrites(t, mt)
		require.Len(t, w, 2)
		// The other record is deleted first (a CNAME can't coexist with it), then the remaining one is converted
		assert.Equal(t, cfWrite{Method: http.MethodDelete, ID: "a2"}, w[0])
		assert.Equal(t, http.MethodPut, w[1].Method)
		assert.Equal(t, "a1", w[1].ID)
		assert.Equal(t, "CNAME", w[1].Body["type"])
		assert.Equal(t, "tunnel.example.com", w[1].Body["content"])
	})

	t.Run("CNAME to A records converts the CNAME in place then creates the rest", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, nil, []string{cfRecordJSON("c1", "CNAME", "tunnel.example.com", true)})
		cfOK(mt, http.MethodPut, dnsPath+"/c1")
		cfOK(mt, http.MethodPost, dnsPath)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, IPTargets("1.1.1.1", "2001:db8::1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)

		w := cfWrites(t, mt)
		require.Len(t, w, 2)
		assert.Equal(t, http.MethodPut, w[0].Method)
		assert.Equal(t, "c1", w[0].ID)
		assert.Equal(t, "A", w[0].Body["type"])
		assert.Equal(t, "1.1.1.1", w[0].Body["content"])
		assert.Equal(t, false, w[0].Body["proxied"])
		assert.EqualValues(t, 60, w[0].Body["ttl"])
		assert.Equal(t, http.MethodPost, w[1].Method)
		assert.Equal(t, "AAAA", w[1].Body["type"])
	})

	t.Run("changing the CNAME target overwrites the record", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, nil, []string{cfRecordJSON("c1", "CNAME", "old.example.com", false)})
		cfOK(mt, http.MethodPut, dnsPath+"/c1")

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "new.example.com"}})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 1)
		assert.Equal(t, http.MethodPut, w[0].Method)
		assert.Equal(t, "new.example.com", w[0].Body["content"])
	})

	t.Run("changing proxied status updates the record in place", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, []string{cfRecordJSON("a1", "A", "1.1.1.1", false)}, nil)
		cfOK(mt, http.MethodPut, dnsPath+"/a1")

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "1.1.1.1", Proxied: true}})
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 1)
		assert.Equal(t, http.MethodPut, w[0].Method)
		assert.Equal(t, true, w[0].Body["proxied"])
	})

	t.Run("falls back to delete and create if the record can't be overwritten", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		cfSetExisting(mt, nil, []string{cfRecordJSON("c1", "CNAME", "tunnel.example.com", true)})
		mt.SetResponse(http.MethodPut, dnsPath+"/c1", &MockResponse{StatusCode: http.StatusBadRequest, Body: `{"success":false}`})
		cfOK(mt, http.MethodDelete, dnsPath+"/c1")
		cfOK(mt, http.MethodPost, dnsPath)

		res, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, IPTargets("1.1.1.1"))
		require.NoError(t, err)
		assert.True(t, res.Changed)
		w := cfWrites(t, mt)
		require.Len(t, w, 3)
		assert.Equal(t, http.MethodPut, w[0].Method)
		assert.Equal(t, http.MethodDelete, w[1].Method)
		assert.Equal(t, http.MethodPost, w[2].Method)
		assert.Equal(t, "A", w[2].Body["type"])
	})

	t.Run("CNAME can't be combined with other targets", func(t *testing.T) {
		provider, mt := newCloudflareTestProviderWithMock()
		_, err := provider.UpdateRecords(t.Context(), "app.example.com", 60, []Target{cname, {Value: "1.1.1.1"}})
		require.Error(t, err)
		assert.Empty(t, mt.GetRequests(), "nothing is sent")
	})
}

func TestProviders_RejectUnsupportedTargets(t *testing.T) {
	ovh, _ := newOVHTestProviderWithMock()
	_, err := ovh.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "tunnel.example.com"}})
	require.ErrorIs(t, err, ErrUnsupportedTarget)
	_, err = ovh.UpdateRecords(t.Context(), "app.example.com", 60, []Target{{Value: "1.1.1.1", Proxied: true}})
	require.ErrorIs(t, err, ErrUnsupportedTarget)
}
