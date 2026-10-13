package utils

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsTruthy(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "True", "t", "T", "on", "ON", "yes", "YES", "y", "Y"} {
		assert.True(t, IsTruthy(v), "expected %q to be truthy", v)
	}
	for _, v := range []string{"", "0", "false", "f", "off", "no", "n", "2", "truee", "yes please", " true"} {
		assert.False(t, IsTruthy(v), "expected %q not to be truthy", v)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	err := os.WriteFile(file, []byte("hello"), 0o600)
	require.NoError(t, err)

	exists, err := FileExists(file)
	require.NoError(t, err)
	assert.True(t, exists)

	// Directories are not files
	exists, err = FileExists(dir)
	require.NoError(t, err)
	assert.False(t, exists)

	// A missing file is not an error
	exists, err = FileExists(filepath.Join(dir, "missing.txt"))
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestElementsMatch(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected bool
	}{
		{name: "both nil", a: nil, b: nil, expected: true},
		{name: "nil and empty", a: nil, b: []string{}, expected: true},
		{name: "same order", a: []string{"1.1.1.1", "2.2.2.2"}, b: []string{"1.1.1.1", "2.2.2.2"}, expected: true},
		{name: "different order", a: []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"}, b: []string{"3.3.3.3", "1.1.1.1", "2.2.2.2"}, expected: true},
		{name: "different lengths", a: []string{"1.1.1.1"}, b: []string{"1.1.1.1", "2.2.2.2"}, expected: false},
		{name: "one empty", a: []string{"1.1.1.1"}, b: nil, expected: false},
		{name: "different elements", a: []string{"1.1.1.1", "2.2.2.2"}, b: []string{"1.1.1.1", "3.3.3.3"}, expected: false},
		{name: "same duplicates", a: []string{"1.1.1.1", "1.1.1.1", "2.2.2.2"}, b: []string{"2.2.2.2", "1.1.1.1", "1.1.1.1"}, expected: true},
		{name: "different duplicates", a: []string{"1.1.1.1", "1.1.1.1", "2.2.2.2"}, b: []string{"1.1.1.1", "2.2.2.2", "2.2.2.2"}, expected: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, ElementsMatch(tc.a, tc.b))
			// The result does not depend on the order of the arguments
			assert.Equal(t, tc.expected, ElementsMatch(tc.b, tc.a))
		})
	}
}
