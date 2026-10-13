package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/italypaleale/ddup/dashboard"
)

// This file contains code adapted from Pocket ID:
// https://github.com/pocket-id/pocket-id/tree/v1.11.2
// Copyright (c) 2024, Elias Schneider
// License: BSD 2-Clause (https://github.com/pocket-id/pocket-id/tree/v1.11.2/LICENSE)

func registerStatic(mux *http.ServeMux) error {
	distFS, err := fs.Sub(dashboard.DashboardFS, "dist")
	if err != nil {
		return fmt.Errorf("failed to create sub FS: %w", err)
	}

	cacheMaxAge := time.Hour * 24 * 365
	fileServer := NewCachingFileServer(http.FS(distFS), int(cacheMaxAge.Seconds()))

	mux.Handle("GET /", fileServer)

	return nil
}

// CachingFileServer wraps http.FileServer to add caching headers
type CachingFileServer struct {
	root                    http.FileSystem
	lastModified            time.Time
	lastModifiedHeaderValue string
	assetsCacheControl      string
}

const (
	// Files in this folder have a hash in their name, so they never change and can be cached for a long time
	hashedAssetsPrefix = "/assets/"
	// Everything else (the page itself, the icon) can change when ddup is upgraded, and must be checked every time, or browsers show an old version for up to the cache time
	revalidateCacheControl = "no-cache"
)

// NewCachingFileServer returns a file server that lets browsers cache the hashed files in /assets/ for maxAge seconds, and always revalidate everything else
func NewCachingFileServer(root http.FileSystem, maxAge int) *CachingFileServer {
	return &CachingFileServer{
		root:                    root,
		lastModified:            time.Now(),
		lastModifiedHeaderValue: time.Now().UTC().Format(http.TimeFormat),
		assetsCacheControl:      fmt.Sprintf("public, max-age=%d, immutable", maxAge),
	}
}

// cacheControlFor returns the Cache-Control header for a path
func (f *CachingFileServer) cacheControlFor(path string) string {
	if strings.HasPrefix(path, hashedAssetsPrefix) {
		return f.assetsCacheControl
	}
	return revalidateCacheControl
}

func (f *CachingFileServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check if the client has a cached version
	if ifModifiedSince := r.Header.Get("If-Modified-Since"); ifModifiedSince != "" {
		ifModifiedSinceTime, err := time.Parse(http.TimeFormat, ifModifiedSince)
		if err == nil && f.lastModified.Before(ifModifiedSinceTime.Add(1*time.Second)) {
			// Client's cached version is up to date
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}

	w.Header().Set("Last-Modified", f.lastModifiedHeaderValue)
	w.Header().Set("Cache-Control", f.cacheControlFor(r.URL.Path))

	http.FileServer(f.root).ServeHTTP(w, r)
}
