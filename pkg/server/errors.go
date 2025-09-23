package server

import (
	"context"
	"fmt"
	"net/http"
)

var (
	errStatusRecordNameEmpty = newApiError("api_status_recordname_empty", http.StatusBadRequest, "Parameter record name is empty")
	errStatusDomainNotFound  = newApiError("api_status_domain_notfound", http.StatusNotFound, "Domain not found in the configuration")
)

type apiError struct {
	Code       string            `json:"code"`
	Message    string            `json:"message"`
	InnerError error             `json:"innerError,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`

	httpStatus int
}

func newApiError(code string, httpStatus int, message string) *apiError {
	return &apiError{
		Code:    code,
		Message: message,

		httpStatus: httpStatus,
	}
}

func (e apiError) WriteResponse(ctx context.Context, w http.ResponseWriter) {
	w.Header().Add(headerContentType, jsonContentType)
	w.WriteHeader(e.httpStatus)

	respondWithJSON(ctx, w, e)
}

// Clone returns a cloned error with the data appended
func (e apiError) Clone(with ...func(*apiError)) *apiError {
	cloned := &apiError{
		Code:    e.Code,
		Message: e.Message,

		httpStatus: e.httpStatus,
	}

	for _, w := range with {
		w(cloned)
	}

	return cloned
}

//nolint:unused
func withInnerError(innerError error) func(*apiError) {
	return func(e *apiError) {
		e.InnerError = innerError
	}
}

//nolint:unused
func withMetadata(metadata map[string]string) func(*apiError) {
	return func(e *apiError) {
		e.Metadata = metadata
	}
}

// Error implements the error interface
func (e apiError) Error() string {
	return fmt.Sprintf("API error (%s): %s", e.Code, e.Message)
}

// Is allows comparing API errors by looking at their status codes
func (e apiError) Is(target error) bool {
	targetApiError, ok := target.(apiError)
	if !ok {
		return false
	}

	return targetApiError.Code == e.Code
}
