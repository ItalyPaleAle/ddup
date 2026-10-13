package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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

func registerStatic(mux *http.ServeMux, info buildInfo) error {
	distFS, err := fs.Sub(dashboard.DashboardFS, "dist")
	if err != nil {
		return fmt.Errorf("failed to create sub FS: %w", err)
	}

	cacheMaxAge := time.Hour * 24 * 365
	fileServer, err := NewCachingFileServer(http.FS(distFS), int(cacheMaxAge.Seconds()), info)
	if err != nil {
		return fmt.Errorf("failed to create file server: %w", err)
	}

	mux.Handle("GET /", fileServer)

	return nil
}

// CachingFileServer wraps http.FileServer to add caching headers
type CachingFileServer struct {
	root                    http.FileSystem
	lastModified            time.Time
	lastModifiedHeaderValue string
	assetsCacheControl      string
	// The page with the build info inserted, computed once at startup
	// It's nil if there's no page in the file system
	page []byte
}

const (
	// Files in this folder have a hash in their name, so they never change and can be cached for a long time
	hashedAssetsPrefix = "/assets/"
	// The page is served at both paths
	pagePath = "/index.html"
	// Everything else (the page itself, the icon) can change when ddup is upgraded, and must be checked every time, or browsers show an old version for up to the cache time
	revalidateCacheControl = "no-cache"
)

// NewCachingFileServer returns a file server that lets browsers cache the hashed files in /assets/ for maxAge seconds, and always revalidate everything else
// The page (index.html) is served with the build info inserted
func NewCachingFileServer(root http.FileSystem, maxAge int, info buildInfo) (*CachingFileServer, error) {
	page, err := loadPage(root, info)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	return &CachingFileServer{
		root:                    root,
		lastModified:            now,
		lastModifiedHeaderValue: now.UTC().Format(http.TimeFormat),
		assetsCacheControl:      fmt.Sprintf("public, max-age=%d, immutable", maxAge),
		page:                    page,
	}, nil
}

// loadPage reads index.html and inserts the build info
// It returns nil if there's no index.html
func loadPage(root http.FileSystem, info buildInfo) ([]byte, error) {
	f, err := root.Open(pagePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", pagePath, err)
	}
	defer f.Close() //nolint:errcheck

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", pagePath, err)
	}

	page, err := info.insertIntoPage(data)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare %s: %w", pagePath, err)
	}
	return page, nil
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

	if f.page != nil && (r.URL.Path == "/" || r.URL.Path == pagePath) {
		http.ServeContent(w, r, pagePath, f.lastModified, bytes.NewReader(f.page))
		return
	}

	http.FileServer(f.root).ServeHTTP(w, r)
}
