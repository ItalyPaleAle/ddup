//go:build dashboarddev

package config

// This file must come before "instance.go" lexically
func init() {
	// In dashboard dev mode, enable CORS
	defaultDevConfig = ConfigDev{
		EnableCORS: true,
	}
}
