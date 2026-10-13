package server

import (
	"net/http"
	"strings"
)

// Middleware type is a function that takes an http.Handler and returns another http.Handler
type Middleware func(next http.Handler) http.Handler

// Use applies middlewares to the handler
func Use(h http.Handler, middlewares ...Middleware) http.Handler {
	for _, middleware := range middlewares {
		h = middleware(h)
	}
	return h
}

// MiddlewareMaxBodySize is a middleware that limits the size of the request body
func MiddlewareMaxBodySize(maxSize int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			next.ServeHTTP(w, r)
		})
	}
}

// MiddlewareBuildHeader is a middleware that adds the identifier of the build to the responses for API requests
func MiddlewareBuildHeader(build string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set(headerBuild, build)
			}
			next.ServeHTTP(w, r)
		})
	}
}
