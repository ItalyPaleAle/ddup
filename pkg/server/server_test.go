package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
