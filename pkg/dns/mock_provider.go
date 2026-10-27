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
	// Values of the targets passed to the last call of UpdateRecords
	LastIPs []string
	// Targets passed to the last call of UpdateRecords
	LastTargets []Target
	// Capabilities reported by the provider
	ProviderCapabilities Capabilities
}

// NewMockProvider creates a new MockProvider.
func NewMockProvider(shouldError bool) *MockProvider {
	return &MockProvider{ShouldError: shouldError}
}

// Name implements the Provider interface.
func (m *MockProvider) Name() string {
	return "mock"
}

// Capabilities implements the Provider interface.
func (m *MockProvider) Capabilities() Capabilities {
	return m.ProviderCapabilities
}

// UpdateRecords implements the Provider interface.
func (m *MockProvider) UpdateRecords(ctx context.Context, domain string, ttl int, targets []Target) (UpdateResult, error) {
	m.CallCount++
	m.LastTargets = targets
	m.LastIPs = make([]string, len(targets))
	for i, t := range targets {
		m.LastIPs[i] = t.Value
	}
	if m.ShouldError {
		return UpdateResult{}, errors.New("mock error")
	}
	return UpdateResult{Changed: !m.NoChange, Previous: m.Previous}, nil
}
