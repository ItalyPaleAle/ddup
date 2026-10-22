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
)

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

func TestCloudflareProviderCapabilities(t *testing.T) {
	provider, _ := newCloudflareTestProviderWithMock()
	assert.Equal(t, Capabilities{Type: "cloudflare", CNAME: true, ProviderOptions: []string{"cloudflare"}}, provider.Capabilities())
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
