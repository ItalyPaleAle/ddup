package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	configkit "github.com/italypaleale/go-kit/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newNotificationsConfig() *Config {
	return &Config{
		Providers: map[string]ConfigProvider{
			"test": {
				Cloudflare: &CloudflareConfig{},
			},
		},
		Domains: []ConfigDomain{
			{
				RecordName: "test.example.com",
				Provider:   "test",
				Endpoints: []*ConfigEndpoint{
					{URL: "https://test.example.com/health", IP: "192.0.2.1"},
				},
			},
		},
	}
}

func TestValidateNotifications(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(n *ConfigNotifications)
		expectErr string
	}{
		{
			name: "webhook without name",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{URL: "https://hooks.example.com/x"}}
			},
			expectErr: "notifications.webhooks[0] is invalid: name is empty",
		},
		{
			name: "webhook unknown format",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{Name: "a", Format: "teams", URL: "https://hooks.example.com/x"}}
			},
			expectErr: `notifications.webhooks[0] "a" is invalid: unknown format "teams" (valid formats: plain, slack, discord, ntfy)`,
		},
		{
			name: "webhook unknown event",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{Name: "a", Format: "ntfy", URL: "https://ntfy.example.com/ddup", Events: []NotificationEvent{"foo"}}}
			},
			expectErr: `notifications.webhooks[0] "a" is invalid: unknown event "foo" (valid events: dns_updated, dns_update_failed, all_unhealthy)`,
		},
		{
			name: "webhook duplicate name",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{
					{Name: "a", URL: "https://hooks.example.com/x"},
					{Name: "b", URL: "https://hooks.example.com/x"},
					{Name: "b", Format: "ntfy", URL: "https://ntfy.example.com/ddup"},
				}
			},
			expectErr: `notifications.webhooks[2] "b" is invalid: name is already used by entry 1`,
		},
		{
			name: "email without name",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{{ConnectionString: "console://", To: []string{"a@example.com"}}}
			},
			expectErr: "notifications.email[0] is invalid: name is empty",
		},
		{
			name: "email without recipients",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{{Name: "a", ConnectionString: "console://"}}
			},
			expectErr: `notifications.email[0] "a" is invalid: to is empty; specify at least one recipient`,
		},
		{
			name: "email invalid recipient",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{{Name: "a", ConnectionString: "console://", To: []string{"a@example.com", "not-an-address"}}}
			},
			expectErr: `notifications.email[0] "a" is invalid: to[1] is not a valid email address`,
		},
		{
			name: "email unknown event",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{{Name: "a", ConnectionString: "console://", To: []string{"a@example.com"}, Events: []NotificationEvent{"dns_updated", "nope"}}}
			},
			expectErr: `notifications.email[0] "a" is invalid: unknown event "nope" (valid events: dns_updated, dns_update_failed, all_unhealthy)`,
		},
		{
			name: "email duplicate name",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{
					{Name: "a", ConnectionString: "console://", To: []string{"a@example.com"}},
					{Name: "a", ConnectionString: "console://", To: []string{"a@example.com"}},
				}
			},
			expectErr: `notifications.email[1] "a" is invalid: name is already used by entry 0`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newNotificationsConfig()
			tc.mutate(&cfg.Notifications)
			err := cfg.Validate(slog.Default())
			require.ErrorContains(t, err, tc.expectErr)
		})
	}

	t.Run("names can repeat across kinds", func(t *testing.T) {
		cfg := newNotificationsConfig()
		cfg.Notifications = ConfigNotifications{
			Webhooks: []ConfigNotificationWebhook{{Name: "a", URL: "https://hooks.example.com/x"}},
			Email:    []ConfigNotificationEmail{{Name: "a", ConnectionString: "console://", To: []string{"a@example.com"}}},
		}
		err := cfg.Validate(slog.Default())
		require.NoError(t, err)
	})

	t.Run("errors never include secrets", func(t *testing.T) {
		cfg := newNotificationsConfig()
		cfg.Notifications.Webhooks = []ConfigNotificationWebhook{{Name: "a", Format: "teams", URL: "https://hooks.example.com/secret-token-value"}}
		err := cfg.Validate(slog.Default())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret-token-value")
	})

	t.Run("defaults", func(t *testing.T) {
		cfg := newNotificationsConfig()
		cfg.Notifications = ConfigNotifications{
			Webhooks: []ConfigNotificationWebhook{
				{Name: "a", Format: "ntfy", URL: "https://ntfy.example.com/ddup"},
				{Name: "b", URL: "https://hooks.example.com/x"},
				{Name: "c", Format: "Slack", URL: "https://hooks.example.com/x", Events: []NotificationEvent{EventAllUnhealthy}},
			},
			Email: []ConfigNotificationEmail{{Name: "d", ConnectionString: "console://", To: []string{"a@example.com"}}},
		}
		err := cfg.Validate(slog.Default())
		require.NoError(t, err)

		all := []NotificationEvent{EventDNSUpdated, EventDNSUpdateFailed, EventAllUnhealthy}
		assert.Equal(t, all, cfg.Notifications.Webhooks[0].Events)
		assert.Equal(t, all, cfg.Notifications.Webhooks[1].Events)
		assert.Equal(t, all, cfg.Notifications.Email[0].Events)
		assert.Equal(t, "ntfy", cfg.Notifications.Webhooks[0].Format)
		assert.Equal(t, "plain", cfg.Notifications.Webhooks[1].Format)
		assert.Equal(t, "slack", cfg.Notifications.Webhooks[2].Format)
		assert.Equal(t, []NotificationEvent{EventAllUnhealthy}, cfg.Notifications.Webhooks[2].Events)
	})
}

// Secrets are read from files by the config loader
func TestNotificationsSecretsFromFiles(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "ntfy-token"), []byte("tk_example\n"), 0o600)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(dir, "conn"), []byte("resend://re_example?fromAddress=ddup@example.com\n"), 0o600)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(dir, "recipient"), []byte("postmaster@example.com\n"), 0o600)
	require.NoError(t, err)

	const configYAML = `
interval: 30s
domains:
  - recordName: "test.example.com"
    provider: "p"
    endpoints:
      - url: "https://test.example.com/health"
        ip: "192.0.2.1"
providers:
  p:
    cloudflare:
      apiToken: "token"
      zoneId: "zone"
notifications:
  webhooks:
    - name: "alerts"
      format: ntfy
      url: "http://ntfy.default.svc.cluster.local/ddup"
      token: !file ntfy-token
      allowPrivateDestinations: true
      timeout: 5s
      maxAttempts: 2
      retryInterval: 1s
  email:
    - name: "ops"
      connectionString: !file conn
      to:
        - !file recipient
      events: ["all_unhealthy"]
`
	cfgPath := filepath.Join(dir, "config.yaml")
	err = os.WriteFile(cfgPath, []byte(configYAML), 0o600)
	require.NoError(t, err)
	t.Setenv("DDUP_CONFIG", cfgPath)

	cfg := GetDefaultConfig()
	err = configkit.LoadConfig(cfg, configkit.LoadConfigOpts{
		EnvVar:  "DDUP_CONFIG",
		DirName: "ddup",
	})
	require.NoError(t, err)
	err = cfg.Validate(slog.Default())
	require.NoError(t, err)

	require.Len(t, cfg.Notifications.Webhooks, 1)
	ntfy := cfg.Notifications.Webhooks[0]
	assert.Equal(t, "ntfy", ntfy.Format)
	assert.Equal(t, "tk_example", ntfy.Token)
	assert.True(t, ntfy.AllowPrivateDestinations)
	assert.Equal(t, 5*time.Second, ntfy.Timeout)
	assert.Equal(t, 2, ntfy.MaxAttempts)
	assert.Equal(t, time.Second, ntfy.RetryInterval)

	require.Len(t, cfg.Notifications.Email, 1)
	assert.Equal(t, "resend://re_example?fromAddress=ddup@example.com", cfg.Notifications.Email[0].ConnectionString)
	assert.Equal(t, []string{"postmaster@example.com"}, cfg.Notifications.Email[0].To)
	assert.Equal(t, []NotificationEvent{EventAllUnhealthy}, cfg.Notifications.Email[0].Events)

	// The secrets are not in the debug representation of the config
	assert.NotContains(t, cfg.String(), "tk_example")
	assert.NotContains(t, cfg.String(), "re_example")
}

// Parses a Go source file and returns the string values of the constants selected by match
func stringConsts(t *testing.T, path string, match func(vs *ast.ValueSpec) bool) []string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	require.NoError(t, err)

	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) == 0 || !match(vs) {
				continue
			}
			lit, ok := vs.Values[0].(*ast.BasicLit)
			require.True(t, ok, "constant %s must be a string literal", vs.Names[0].Name)
			val, err := strconv.Unquote(lit.Value)
			require.NoError(t, err)
			values = append(values, val)
		}
	}
	return values
}

// Returns a matcher for the constants declared with the given type
func hasType(typeName string) func(vs *ast.ValueSpec) bool {
	return func(vs *ast.ValueSpec) bool {
		ident, ok := vs.Type.(*ast.Ident)
		return ok && ident.Name == typeName
	}
}

// NotificationEvents must list every event constant, so a new event is not forgotten
func TestNotificationEventsAreComplete(t *testing.T) {
	declared := stringConsts(t, "notifications.go", hasType("NotificationEvent"))
	require.NotEmpty(t, declared)

	listed := make([]string, 0, len(declared))
	for _, ev := range NotificationEvents() {
		listed = append(listed, string(ev))
	}

	assert.ElementsMatch(t, declared, listed, "NotificationEvents() must list all the NotificationEvent constants")
}

// The webhook formats must be the ones supported by go-kit, so a new format is not forgotten
func TestWebhookFormatsMatchGoKit(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", "github.com/italypaleale/go-kit").Output()
	require.NoError(t, err)
	dir := strings.TrimSpace(string(out))
	require.NotEmpty(t, dir)

	supported := stringConsts(t, filepath.Join(dir, "webhook", "webhook.go"), hasType("WebhookFormat"))
	require.NotEmpty(t, supported)

	// The formats accepted by the configuration are the constants of this package named WebhookFormat*
	ours := stringConsts(t, "notifications.go", func(vs *ast.ValueSpec) bool {
		return strings.HasPrefix(vs.Names[0].Name, "WebhookFormat")
	})

	assert.ElementsMatch(t, supported, ours, "the WebhookFormat constants must match the formats supported by go-kit")
}
