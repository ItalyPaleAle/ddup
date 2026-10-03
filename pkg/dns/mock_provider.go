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
	// If true, UpdateRecords reports that nothing changed
	NoChange bool
	// IPs reported as previously in DNS
	Previous []string
	// Targets passed to the last UpdateRecords call
	LastTargets []Target
}

// NewMockProvider creates a new MockProvider.
func NewMockProvider(shouldError bool) *MockProvider {
	return &MockProvider{ShouldError: shouldError}
}

// Name implements the Provider interface.
func (m *MockProvider) Name() string {
	return "mock"
}

// UpdateRecords implements the Provider interface.
func (m *MockProvider) UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (UpdateResult, error) {
	m.CallCount++
	m.LastTargets = targets
	if m.ShouldError {
		return UpdateResult{}, errors.New("mock error")
	}
	return UpdateResult{Changed: !m.NoChange, Previous: m.Previous}, nil
}
