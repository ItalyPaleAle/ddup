package notify

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedactor(t *testing.T) {
	r := newRedactor("hook-token", "hook-token-longer", "", "hook-token")

	assert.Equal(t, "no secrets here", r.String("no secrets here"))
	assert.Equal(t, "a [redacted] b", r.String("a hook-token-longer b"))
	assert.Equal(t, "[redacted] and [redacted]", r.String("hook-token and hook-token"))
	assert.Equal(t, "Authorization: Bearer [redacted]", r.String("Authorization: Bearer abc.DEF-123=="))
	assert.Equal(t, "GET https://api.example.com/x?key=[redacted]&b=1", r.String("GET https://api.example.com/x?key=abcdef&b=1"))

	inner := errors.New("failed with hook-token")
	err := r.Error(inner)
	require.Error(t, err)
	assert.Equal(t, "failed with [redacted]", err.Error())
	require.ErrorIs(t, err, inner)
	require.NoError(t, r.Error(nil))
}

func TestURLSecrets(t *testing.T) {
	r := newRedactor(urlSecrets("https://user:pass-value@hooks.example.com/services/T0/B0/token-value?auth=query-value")...)
	out := r.String(`Post "https://user:***@hooks.example.com/services/T0/B0/token-value?auth=query-value": dial tcp: refused`)
	for _, secret := range []string{"token-value", "query-value", "pass-value"} {
		assert.NotContains(t, out, secret)
	}
}

func TestConnStringSecrets(t *testing.T) {
	tests := []struct {
		connString string
		secrets    []string
		keeps      string
	}{
		{
			connString: "resend://re_key_value?fromAddress=ddup@example.com",
			secrets:    []string{"re_key_value"},
		},
		{
			connString: "smtp://user:pass-value@smtp.example.com:587?fromAddress=ddup@example.com",
			secrets:    []string{"pass-value"},
			keeps:      "smtp.example.com",
		},
		{
			connString: "awsses://AKIAEXAMPLE:secret-value@us-east-1?fromAddress=ddup@example.com",
			secrets:    []string{"AKIAEXAMPLE", "secret-value"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.connString, func(t *testing.T) {
			r := newRedactor(connStringSecrets(tc.connString)...)
			out := r.String("failed: " + tc.connString + " host smtp.example.com")
			for _, secret := range tc.secrets {
				assert.NotContains(t, out, secret)
			}
			if tc.keeps != "" {
				assert.Contains(t, out, tc.keeps)
			}
		})
	}
}

func TestRedactingLogger(t *testing.T) {
	logs := captureLogs(t)
	logger := newRedactingLogger(slog.Default(), newRedactor("the-secret")).With("with", "the-secret")

	logger.Warn("message with the-secret",
		"err", errors.New("error with the-secret"),
		slog.Group("g", "inner", "the-secret"),
		"num", 42,
	)

	out := logs.String()
	assert.NotContains(t, out, "the-secret")
	assert.Contains(t, out, "num=42")
	assert.Contains(t, out, "[redacted]")
}
