package server

import (
	"fmt"
	"io/fs"
	"net/http"
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

	cacheMaxAge := time.Hour * 24
	fileServer := NewCachingFileServer(http.FS(distFS), int(cacheMaxAge.Seconds()))

	mux.Handle("GET /", fileServer)

	return nil
}

// CachingFileServer wraps http.FileServer to add caching headers
type CachingFileServer struct {
	root                    http.FileSystem
	lastModified            time.Time
	cacheMaxAge             int
	lastModifiedHeaderValue string
	cacheControlHeaderValue string
}

func NewCachingFileServer(root http.FileSystem, maxAge int) *CachingFileServer {
	return &CachingFileServer{
		root:                    root,
		lastModified:            time.Now(),
		cacheMaxAge:             maxAge,
		lastModifiedHeaderValue: time.Now().UTC().Format(http.TimeFormat),
		cacheControlHeaderValue: fmt.Sprintf("public, max-age=%d", maxAge),
	}
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
	w.Header().Set("Cache-Control", f.cacheControlHeaderValue)

	http.FileServer(f.root).ServeHTTP(w, r)
}
