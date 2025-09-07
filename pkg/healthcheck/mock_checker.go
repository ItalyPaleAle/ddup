//go:build unit

package healthcheck

import (
	"context"
)

// MockChecker is a mock implementation that embeds the healthcheck interface but allows override.
type MockChecker struct {
	Domain      string
	MaxAttempts int
	Results     []Result
}

// CheckAll implements the public part of Checker interface.
func (m *MockChecker) CheckAll(ctx context.Context) []Result {
	return m.Results
}

// GetDomain implements the public part of Checker interface.
func (m *MockChecker) GetDomain() string {
	return m.Domain
}

// GetMaxAttempts implements the public part of Checker interface.
func (m *MockChecker) GetMaxAttempts() int {
	return m.MaxAttempts
}
