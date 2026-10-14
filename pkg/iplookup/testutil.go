//go:build unit

package iplookup

import (
	"testing"
)

// SetDefaultSources replaces the built-in list of services for the duration of the test
func SetDefaultSources(tb testing.TB, sources []Source) {
	tb.Helper()

	prev := defaultSources
	defaultSources = sources
	tb.Cleanup(func() {
		defaultSources = prev
	})
}
