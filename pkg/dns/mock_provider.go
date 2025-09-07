//go:build unit

package dns

import (
	"context"
	"errors"
)

// MockProvider is a mock implementation of the Provider interface for testing.
type MockProvider struct {
	// If true, UpdateRecords will return an error
	ShouldError bool
	CallCount   int
}

// NewMockProvider creates a new MockProvider.
func NewMockProvider(shouldError bool) *MockProvider {
	return &MockProvider{ShouldError: shouldError}
}

// UpdateRecords implements the Provider interface.
func (m *MockProvider) UpdateRecords(ctx context.Context, domain string, ttl int, ips []string) error {
	m.CallCount++
	if m.ShouldError {
		return errors.New("mock error")
	}
	return nil
}
