package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		assert.NotContains(t, script, "&")
		assert.NotContains(t, script, "\u2028")
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

func TestAppendHTMLSafeJSON(t *testing.T) {
	// The input is what an encoder that doesn't escape for HTML would produce
	got := appendHTMLSafeJSON([]byte("x"), []byte("{\"a\":\"</script> & <b>\u2028\u2029\"}"))
	assert.Equal(t, `x{"a":"\u003c/script\u003e \u0026 \u003cb\u003e\u2028\u2029"}`, string(got))
	assert.JSONEq(t, "{\"a\":\"</script> & <b>\u2028\u2029\"}", string(got[1:]))
}
