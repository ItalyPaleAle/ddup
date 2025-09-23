package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUse(t *testing.T) {
	// testHandler is a simple handler for testing
	testHandler := func(message string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(message)) //nolint:errcheck
		})
	}

	// testMiddleware is a middleware that adds a header for testing
	testMiddleware := func(headerName, headerValue string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(headerName, headerValue)
				next.ServeHTTP(w, r)
			})
		}
	}

	t.Run("No middleware", func(t *testing.T) {
		// Test with no middleware - should return the original handler
		originalHandler := testHandler("test response")
		result := Use(originalHandler)

		// Create a test request
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		// Execute the handler
		result.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "test response", rec.Body.String())
	})

	t.Run("Single middleware", func(t *testing.T) {
		// Test with a single middleware
		originalHandler := testHandler("test response")
		middleware := testMiddleware("X-Test", "middleware-applied")
		result := Use(originalHandler, middleware)

		// Create a test request
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		// Execute the handler
		result.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "test response", rec.Body.String())
		assert.Equal(t, "middleware-applied", rec.Header().Get("X-Test"))
	})

	t.Run("Multiple middlewares", func(t *testing.T) {
		// Test with multiple middlewares - they should be applied in reverse order
		originalHandler := testHandler("test response")
		middleware1 := testMiddleware("X-First", "first")
		middleware2 := testMiddleware("X-Second", "second")
		middleware3 := testMiddleware("X-Third", "third")

		result := Use(originalHandler, middleware1, middleware2, middleware3)

		// Create a test request
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		// Execute the handler
		result.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "test response", rec.Body.String())
		assert.Equal(t, "first", rec.Header().Get("X-First"))
		assert.Equal(t, "second", rec.Header().Get("X-Second"))
		assert.Equal(t, "third", rec.Header().Get("X-Third"))
	})

	t.Run("Middleware order", func(t *testing.T) {
		// Test that middlewares are applied in the correct order
		var order []string

		middleware1 := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "middleware1-before")
				next.ServeHTTP(w, r)
				order = append(order, "middleware1-after")
			})
		}

		middleware2 := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, "middleware2-before")
				next.ServeHTTP(w, r)
				order = append(order, "middleware2-after")
			})
		}

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
			w.WriteHeader(http.StatusOK)
		})

		result := Use(handler, middleware1, middleware2)

		// Create a test request
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()

		// Execute the handler
		result.ServeHTTP(rec, req)

		// Verify the order of execution
		expectedOrder := []string{
			"middleware2-before",
			"middleware1-before",
			"handler",
			"middleware1-after",
			"middleware2-after",
		}
		assert.Equal(t, expectedOrder, order)
	})
}

func TestMiddlewareMaxBodySize(t *testing.T) {
	t.Run("Allowed size", func(t *testing.T) {
		// Test with request body within the allowed size
		middleware := MiddlewareMaxBodySize(100)

		// Create a handler that reads the body
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body) //nolint:errcheck
		})

		wrappedHandler := middleware(handler)

		// Create a request with a small body
		smallBody := "small body content"
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(smallBody))
		rec := httptest.NewRecorder()

		// Execute the handler
		wrappedHandler.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, smallBody, rec.Body.String())
	})

	t.Run("Exceeds size", func(t *testing.T) {
		// Test with request body exceeding the allowed size
		middleware := MiddlewareMaxBodySize(10)

		// Create a handler that tries to read the body
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		wrappedHandler := middleware(handler)

		// Create a request with a large body
		largeBody := strings.Repeat("a", 20) // 20 bytes, exceeds limit of 10
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(largeBody))
		rec := httptest.NewRecorder()

		// Execute the handler
		wrappedHandler.ServeHTTP(rec, req)

		// Verify the response indicates an error
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.Contains(t, rec.Body.String(), "http: request body too large")
	})

	t.Run("Exact size", func(t *testing.T) {
		// Test with request body exactly at the allowed size
		middleware := MiddlewareMaxBodySize(15)

		// Create a handler that reads the body
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body) //nolint:errcheck
		})

		wrappedHandler := middleware(handler)

		// Create a request with body exactly at the limit
		exactBody := strings.Repeat("a", 15) // Exactly 15 bytes
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(exactBody))
		rec := httptest.NewRecorder()

		// Execute the handler
		wrappedHandler.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, exactBody, rec.Body.String())
	})

	t.Run("Empty body", func(t *testing.T) {
		// Test with empty request body
		middleware := MiddlewareMaxBodySize(100)

		// Create a handler that reads the body
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("body length: " + string(rune(len(body))))) //nolint:errcheck
		})

		wrappedHandler := middleware(handler)

		// Create a request with empty body
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()

		// Execute the handler
		wrappedHandler.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "body length: ")
	})

	t.Run("ZeroSize", func(t *testing.T) {
		// Test with zero allowed size
		middleware := MiddlewareMaxBodySize(0)

		// Create a handler that tries to read the body
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
		})

		wrappedHandler := middleware(handler)

		// Create a request with any body content
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("a"))
		rec := httptest.NewRecorder()

		// Execute the handler
		wrappedHandler.ServeHTTP(rec, req)

		// Verify the response indicates an error
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})

	t.Run("Multiple reads", func(t *testing.T) {
		// Test that the middleware works correctly when the handler reads the body multiple times
		middleware := MiddlewareMaxBodySize(50)

		// Create a handler that reads the body in chunks
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buffer := &bytes.Buffer{}

			// Read in small chunks
			chunk := make([]byte, 5)
			for {
				n, err := r.Body.Read(chunk)
				if n > 0 {
					buffer.Write(chunk[:n])
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
					return
				}
			}

			w.WriteHeader(http.StatusOK)
			_, _ = io.Copy(w, buffer) //nolint:errcheck
		})

		wrappedHandler := middleware(handler)

		// Create a request with body within the limit
		body := "This is a test body that should be allowed"
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		rec := httptest.NewRecorder()

		// Execute the handler
		wrappedHandler.ServeHTTP(rec, req)

		// Verify the response
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, body, rec.Body.String())
	})

	t.Run("With Use", func(t *testing.T) {
		// testMiddleware is a middleware that adds a header for testing
		testMiddleware := func(headerName, headerValue string) Middleware {
			return func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set(headerName, headerValue)
					next.ServeHTTP(w, r)
				})
			}
		}

		// Test the max body size middleware integrated with the Use function
		bodyMiddleware := MiddlewareMaxBodySize(20)
		headerMiddleware := testMiddleware("X-Integration", "test")

		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("processed: " + string(body))) //nolint:errcheck
		})

		wrappedHandler := Use(handler, bodyMiddleware, headerMiddleware)

		// Test with allowed body size
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("small body"))
		rec := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "test", rec.Header().Get("X-Integration"))
		assert.Equal(t, "processed: small body", rec.Body.String())

		// Test with body size exceeding limit
		req2 := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("this body is definitely too long"))
		rec2 := httptest.NewRecorder()

		wrappedHandler.ServeHTTP(rec2, req2)

		assert.Equal(t, http.StatusRequestEntityTooLarge, rec2.Code)
		assert.Equal(t, "test", rec2.Header().Get("X-Integration")) // Header middleware should still run
	})
}
