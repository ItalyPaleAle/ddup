package config

import (
	"log/slog"
	"os"
	"path/filepath"
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
			name: "ntfy without name",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{{URL: "https://ntfy.example.com/ddup"}}
			},
			expectErr: "notifications.ntfy[0] is invalid: name is empty",
		},
		{
			name: "ntfy duplicate name",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{
					{Name: "a", URL: "https://ntfy.example.com/ddup"},
					{Name: "a", URL: "https://ntfy.example.com/ddup"},
				}
			},
			expectErr: `notifications.ntfy[1] "a" is invalid: name is already used by entry 0`,
		},
		{
			name:      "ntfy without url",
			mutate:    func(n *ConfigNotifications) { n.Ntfy = []ConfigNotificationNtfy{{Name: "a"}} },
			expectErr: `notifications.ntfy[0] "a" is invalid: url is empty`,
		},
		{
			name: "ntfy url with other scheme",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "ftp://ntfy.example.com/ddup"}}
			},
			expectErr: `notifications.ntfy[0] "a" is invalid: url must use the http or https scheme`,
		},
		{
			name:      "ntfy url without host",
			mutate:    func(n *ConfigNotifications) { n.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "https:///ddup"}} },
			expectErr: `notifications.ntfy[0] "a" is invalid: url must include a host`,
		},
		{
			name: "ntfy url without topic",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "https://ntfy.example.com/"}}
			},
			expectErr: `notifications.ntfy[0] "a" is invalid: url must include the topic name`,
		},
		{
			name: "ntfy invalid url",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "https://ntfy.example.com/%zz"}}
			},
			expectErr: `notifications.ntfy[0] "a" is invalid: url is not a valid URL`,
		},
		{
			name: "ntfy unknown event",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "https://ntfy.example.com/ddup", Events: []NotificationEvent{"foo"}}}
			},
			expectErr: `notifications.ntfy[0] "a" is invalid: unknown event "foo" (valid events: dns_updated, dns_update_failed, all_unhealthy)`,
		},
		{
			name: "ntfy negative timeout",
			mutate: func(n *ConfigNotifications) {
				n.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "https://ntfy.example.com/ddup", Timeout: -time.Second}}
			},
			expectErr: `notifications.ntfy[0] "a" is invalid: timeout must not be negative`,
		},
		{
			name: "webhook negative maxAttempts",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{Name: "a", URL: "https://hooks.example.com/x", MaxAttempts: -1}}
			},
			expectErr: `notifications.webhooks[0] "a" is invalid: maxAttempts must not be negative`,
		},
		{
			name: "webhook negative retryInterval",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{Name: "a", URL: "https://hooks.example.com/x", RetryInterval: -time.Second}}
			},
			expectErr: `notifications.webhooks[0] "a" is invalid: retryInterval must not be negative`,
		},
		{
			name: "webhook unknown format",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{Name: "a", Format: "teams", URL: "https://hooks.example.com/x"}}
			},
			expectErr: `notifications.webhooks[0] "a" is invalid: unknown format "teams" (valid formats: plain, slack, discord)`,
		},
		{
			name:      "webhook without url",
			mutate:    func(n *ConfigNotifications) { n.Webhooks = []ConfigNotificationWebhook{{Name: "a", Format: "slack"}} },
			expectErr: `notifications.webhooks[0] "a" is invalid: url is empty`,
		},
		{
			name: "webhook duplicate name",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{
					{Name: "a", URL: "https://hooks.example.com/x"},
					{Name: "b", URL: "https://hooks.example.com/x"},
					{Name: "b", URL: "https://hooks.example.com/x"},
				}
			},
			expectErr: `notifications.webhooks[2] "b" is invalid: name is already used by entry 1`,
		},
		{
			name: "webhook url with other scheme",
			mutate: func(n *ConfigNotifications) {
				n.Webhooks = []ConfigNotificationWebhook{{Name: "a", URL: "file:///etc/passwd"}}
			},
			expectErr: `notifications.webhooks[0] "a" is invalid: url must use the http or https scheme`,
		},
		{
			name: "email without name",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{{ConnectionString: "console://", To: []string{"a@example.com"}}}
			},
			expectErr: "notifications.email[0] is invalid: name is empty",
		},
		{
			name: "email without connection string",
			mutate: func(n *ConfigNotifications) {
				n.Email = []ConfigNotificationEmail{{Name: "a", To: []string{"a@example.com"}}}
			},
			expectErr: `notifications.email[0] "a" is invalid: connectionString is empty`,
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
			Ntfy:     []ConfigNotificationNtfy{{Name: "a", URL: "https://ntfy.example.com/ddup"}},
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

		cfg = newNotificationsConfig()
		cfg.Notifications.Ntfy = []ConfigNotificationNtfy{{Name: "a", URL: "ftp://ntfy.example.com/secret-topic-value"}}
		err = cfg.Validate(slog.Default())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "secret-topic-value")
	})

	t.Run("defaults", func(t *testing.T) {
		cfg := newNotificationsConfig()
		cfg.Notifications = ConfigNotifications{
			Ntfy:     []ConfigNotificationNtfy{{Name: "a", URL: "https://ntfy.example.com/ddup"}},
			Webhooks: []ConfigNotificationWebhook{{Name: "b", URL: "https://hooks.example.com/x"}, {Name: "c", Format: "Slack", URL: "https://hooks.example.com/x", Events: []NotificationEvent{EventAllUnhealthy}}},
			Email:    []ConfigNotificationEmail{{Name: "d", ConnectionString: "console://", To: []string{"a@example.com"}}},
		}
		err := cfg.Validate(slog.Default())
		require.NoError(t, err)

		all := []NotificationEvent{EventDNSUpdated, EventDNSUpdateFailed, EventAllUnhealthy}
		assert.Equal(t, all, cfg.Notifications.Ntfy[0].Events)
		assert.Equal(t, all, cfg.Notifications.Webhooks[0].Events)
		assert.Equal(t, all, cfg.Notifications.Email[0].Events)
		assert.Equal(t, "plain", cfg.Notifications.Webhooks[0].Format)
		assert.Equal(t, "slack", cfg.Notifications.Webhooks[1].Format)
		assert.Equal(t, []NotificationEvent{EventAllUnhealthy}, cfg.Notifications.Webhooks[1].Events)
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
  ntfy:
    - name: "alerts"
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

	require.Len(t, cfg.Notifications.Ntfy, 1)
	ntfy := cfg.Notifications.Ntfy[0]
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
