package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/healthcheck"
)

type fakeProvider struct {
	forceErr   error
	forceCalls int
}

func (f *fakeProvider) GetAllDomainsStatus() map[string]healthcheck.DomainStatus {
	return map[string]healthcheck.DomainStatus{"a.example.com": {Provider: "p"}}
}

func (f *fakeProvider) GetDomainStatus(domain string) *healthcheck.DomainStatus { return nil }

func (f *fakeProvider) ForceCheck(ctx context.Context) error {
	f.forceCalls++
	return f.forceErr
}

func TestForceCheckEndpoint(t *testing.T) {
	post := func(t *testing.T, fp *fakeProvider, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		s, err := NewServer(NewServerOpts{HealthChecker: fp})
		require.NoError(t, err)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/check", nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		return rec
	}

	t.Run("requires the header", func(t *testing.T) {
		fp := &fakeProvider{}
		rec := post(t, fp, nil)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Zero(t, fp.forceCalls, "no check is run without the header")

		rec = post(t, fp, map[string]string{headerRequestedBy: "other"})
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Zero(t, fp.forceCalls)
	})

	t.Run("runs the check and returns the status", func(t *testing.T) {
		fp := &fakeProvider{}
		rec := post(t, fp, map[string]string{headerRequestedBy: requestedByValue})
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, 1, fp.forceCalls)
		assert.Contains(t, rec.Body.String(), "a.example.com")
	})

	t.Run("maps errors", func(t *testing.T) {
		rec := post(t, &fakeProvider{forceErr: context.DeadlineExceeded}, map[string]string{headerRequestedBy: requestedByValue})
		assert.Equal(t, http.StatusGatewayTimeout, rec.Code)

		rec = post(t, &fakeProvider{forceErr: errors.New("boom")}, map[string]string{headerRequestedBy: requestedByValue})
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})

	t.Run("GET does not run a check", func(t *testing.T) {
		fp := &fakeProvider{}
		s, err := NewServer(NewServerOpts{HealthChecker: fp})
		require.NoError(t, err)
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/check", nil)
		req.Header.Set(headerRequestedBy, requestedByValue)
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, req)
		assert.NotEqual(t, http.StatusOK, rec.Code)
		assert.Zero(t, fp.forceCalls)
	})
}

func TestStaticCacheControl(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte("<html><head></head></html>")},
		"favicon.svg":      {Data: []byte("<svg/>")},
		"assets/app-1a.js": {Data: []byte("console.log(1)")},
	}
	srv, err := NewCachingFileServer(http.FS(fsys), 31536000, buildInfo{})
	require.NoError(t, err)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		return rec
	}

	// The page and the icon can change on upgrades: browsers must revalidate them
	assert.Equal(t, "no-cache", get("/").Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", get("/index.html").Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", get("/favicon.svg").Header().Get("Cache-Control"))
	// Assets have a hash in their name
	rec := get("/assets/app-1a.js")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
	assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age=31536000")
}

// Sets the build info for the duration of the test
func setBuildInfo(t *testing.T, version, id, commit, date string) {
	t.Helper()
	oldVersion, oldID, oldCommit, oldDate := buildinfo.AppVersion, buildinfo.BuildId, buildinfo.CommitHash, buildinfo.BuildDate
	t.Cleanup(func() {
		buildinfo.AppVersion, buildinfo.BuildId, buildinfo.CommitHash, buildinfo.BuildDate = oldVersion, oldID, oldCommit, oldDate
	})
	buildinfo.AppVersion, buildinfo.BuildId, buildinfo.CommitHash, buildinfo.BuildDate = version, id, commit, date
}

// extractBuildScript returns the content of the build info script element in the page
func extractBuildScript(t *testing.T, body string) string {
	t.Helper()
	const open = `<script type="application/json" id="ddup-build">`
	start := strings.Index(body, open)
	require.GreaterOrEqual(t, start, 0, "the page has the build info element")
	start += len(open)
	end := strings.Index(body[start:], "</script>")
	require.GreaterOrEqual(t, end, 0)
	return body[start : start+end]
}

func TestBuildInfoInPage(t *testing.T) {
	const page = "<!doctype html><html><head><title>ddup</title></head><body><script src=\"/assets/app-1a.js\"></script></body></html>"
	fsys := fstest.MapFS{
		"index.html":       {Data: []byte(page)},
		"assets/app-1a.js": {Data: []byte("console.log(1)")},
	}

	newServer := func(t *testing.T, info buildInfo) *CachingFileServer {
		t.Helper()
		srv, err := NewCachingFileServer(http.FS(fsys), 31536000, info)
		require.NoError(t, err)
		return srv
	}

	get := func(t *testing.T, srv *CachingFileServer, path string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	info := buildInfo{
		Build:     "1.2.3|build-42|0123456789abcdef",
		Version:   "1.2.3",
		BuildID:   "build-42",
		Commit:    "0123456789abcdef",
		BuildDate: "2026-01-02T03:04:05Z",
	}

	t.Run("the page has the build info at both paths", func(t *testing.T) {
		srv := newServer(t, info)
		for _, path := range []string{"/", "/index.html"} {
			rec := get(t, srv, path, nil)
			require.Equal(t, http.StatusOK, rec.Code, path)
			assert.Contains(t, rec.Header().Get("Content-Type"), "text/html", path)

			body := rec.Body.String()
			// The element is right before the end of the head, and the rest of the page is unchanged
			script := extractBuildScript(t, body)
			assert.Contains(t, body, "</script></head><body>")

			var got buildInfo
			require.NoError(t, json.Unmarshal([]byte(script), &got), path)
			assert.Equal(t, info, got, path)
		}
	})

	t.Run("values that are empty are left out", func(t *testing.T) {
		srv := newServer(t, buildInfo{Build: "canary||", Version: "canary"})
		script := extractBuildScript(t, get(t, srv, "/", nil).Body.String())
		assert.JSONEq(t, `{"build":"canary||","version":"canary"}`, script)
	})

	t.Run("hostile values can't end the element", func(t *testing.T) {
		hostile := buildInfo{
			Build:     "x",
			Version:   `</script><script>alert(1)</script>`,
			BuildID:   `"quoted" & <b>`,
			Commit:    "a\u2028b</SCRIPT>",
			BuildDate: `<!-- <script>`,
		}
		srv := newServer(t, hostile)
		body := get(t, srv, "/", nil).Body.String()

		script := extractBuildScript(t, body)
		assert.NotContains(t, script, "<")
		assert.NotContains(t, script, ">")
		// The page has the build info element and its own script, and nothing else
		assert.Equal(t, 2, strings.Count(body, "</script>"))

		var got buildInfo
		require.NoError(t, json.Unmarshal([]byte(script), &got))
		assert.Equal(t, hostile, got)
	})

	t.Run("the page is revalidated and answers 304", func(t *testing.T) {
		srv := newServer(t, info)
		for _, path := range []string{"/", "/index.html"} {
			rec := get(t, srv, path, nil)
			assert.Equal(t, "no-cache", rec.Header().Get("Cache-Control"), path)
			lastModified := rec.Header().Get("Last-Modified")
			require.NotEmpty(t, lastModified, path)

			rec = get(t, srv, path, map[string]string{"If-Modified-Since": lastModified})
			assert.Equal(t, http.StatusNotModified, rec.Code, path)
			assert.Empty(t, rec.Body.String(), path)
		}
	})

	t.Run("assets are unchanged", func(t *testing.T) {
		srv := newServer(t, info)
		rec := get(t, srv, "/assets/app-1a.js", nil)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "console.log(1)", rec.Body.String())
		assert.Contains(t, rec.Header().Get("Cache-Control"), "immutable")
	})

	t.Run("a page without a head is an error", func(t *testing.T) {
		_, err := NewCachingFileServer(http.FS(fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}), 1, info)
		require.ErrorContains(t, err, "no </head>")
	})

	t.Run("no page is not an error", func(t *testing.T) {
		srv, err := NewCachingFileServer(http.FS(fstest.MapFS{}), 1, info)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, get(t, srv, "/missing.html", nil).Code)
	})
}

func TestBuildHeader(t *testing.T) {
	setBuildInfo(t, "1.2.3", "build-42", "0123456789abcdef", "2026-01-02T03:04:05Z")

	s, err := NewServer(NewServerOpts{HealthChecker: &fakeProvider{}})
	require.NoError(t, err)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		return rec
	}

	t.Run("API responses have the build", func(t *testing.T) {
		rec := get("/api/status")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "1.2.3|build-42|0123456789abcdef", rec.Header().Get(headerBuild))
		// The response is the same as before
		assert.JSONEq(t, `{"a.example.com":{"lastUpdated":"0001-01-01T00:00:00Z","provider":"p","endpoints":null}}`, rec.Body.String())
	})

	t.Run("matches the build in the page", func(t *testing.T) {
		info := newBuildInfo()
		page, err := info.insertIntoPage([]byte("<head></head>"))
		require.NoError(t, err)

		var got buildInfo
		require.NoError(t, json.Unmarshal([]byte(extractBuildScript(t, string(page))), &got))
		assert.Equal(t, get("/api/status").Header().Get(headerBuild), got.Build)
	})

	t.Run("errors from the API have it too", func(t *testing.T) {
		rec := get("/api/status/unknown")
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.NotEmpty(t, rec.Header().Get(headerBuild))
	})

	t.Run("GET /api/info is gone", func(t *testing.T) {
		assert.Equal(t, http.StatusNotFound, get("/api/info").Code)
	})

	t.Run("other paths don't have it", func(t *testing.T) {
		assert.Empty(t, get("/healthz").Header().Get(headerBuild))
	})
}

func TestBuildIdentifier(t *testing.T) {
	assert.Equal(t, "1.2.3|build-42|abc", buildIdentifier("1.2.3", "build-42", "abc"))
	assert.Equal(t, "canary||", buildIdentifier("canary", "", ""))
	assert.Equal(t, "a_b_c|_|", buildIdentifier("a\nb\u00e9c", "\x7f", ""))
}
