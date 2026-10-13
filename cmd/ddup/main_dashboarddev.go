//go:build dashboarddev

package main

import (
	"context"
	"time"

	"github.com/italypaleale/ddup/pkg/buildinfo"
	"github.com/italypaleale/ddup/pkg/healthcheck"
)

func init() {
	// Mock the status provider so it returns static data
	statusProvider = &mockStatusProvider{}

	// Sample build info, shown in the footer of the dashboard
	// Values set at build time (with -ldflags -X) are kept, so different builds can be tried
	if buildinfo.AppVersion == "canary" {
		buildinfo.AppVersion = "1.2.3"
	}
	if buildinfo.BuildId == "" {
		buildinfo.BuildId = "sample"
	}
	if buildinfo.CommitHash == "" {
		buildinfo.CommitHash = "0123456789abcdef0123456789abcdef01234567"
	}
	if buildinfo.BuildDate == "" {
		buildinfo.BuildDate = "2026-01-02T03:04:05Z"
	}
}

type mockStatusProvider struct {
}

func (m mockStatusProvider) GetAllDomainsStatus() map[string]healthcheck.DomainStatus {
	const (
		provider1 = "test-provider-1"
		provider2 = "test-provider-2"
		provider3 = "test-provider-3"
	)
	now := time.Now()

	// Time of an event the given duration ago
	ago := func(d time.Duration) *time.Time {
		t := now.Add(-d)
		return &t
	}

	return map[string]healthcheck.DomainStatus{
		"example.com": {
			LastUpdated: now.Add(-1 * time.Second),
			Provider:    provider1,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "1.1.1.1", Healthy: true, FailureCount: 0, Since: ago(51 * time.Hour), LastFailure: ago(52 * time.Hour)},
				{IP: "1.1.2.2", Healthy: false, FailureCount: 101, Since: ago(26 * time.Minute), LastFailure: ago(30 * time.Second)},
				{IP: "1.1.3.3", Healthy: true, FailureCount: 0, Since: ago(40 * time.Minute), SinceStart: true},
			},
		},
		"mysite.xyz": {
			LastUpdated: now.Add(-2 * time.Second),
			Provider:    provider1,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "2.2.1.1", Healthy: true, FailureCount: 0, Since: ago(40 * time.Minute), SinceStart: true},
				{IP: "2.2.2.2", Healthy: true, FailureCount: 1, Since: ago(40 * time.Minute), SinceStart: true, LastFailure: ago(70 * time.Second)},
				{IP: "2.2.3.3", Healthy: true, FailureCount: 1, Since: ago(9 * 24 * time.Hour)},
			},
		},
		"alpha.dev": {
			LastUpdated: now.Add(-2500 * time.Millisecond),
			Provider:    provider2,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "10.10.10.1", Healthy: true, FailureCount: 0, Since: ago(5 * time.Hour)},
				{IP: "10.10.10.2", Healthy: true, FailureCount: 1, Since: ago(5 * time.Hour), LastFailure: ago(2 * time.Minute)},
			},
		},
		"beta.io": {
			LastUpdated: now.Add(-5 * time.Second),
			Provider:    provider2,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "172.16.0.1", Healthy: false, FailureCount: 12, Since: ago(40 * time.Minute), SinceStart: true, LastFailure: ago(10 * time.Second)},
			},
		},
		"down.example": {
			LastUpdated: now.Add(-1 * time.Minute),
			Provider:    provider3,
			Error:       "failed to update status with provider",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "3.3.1.1", Healthy: true, FailureCount: 0},
			},
		},
		"slow.example": {
			LastUpdated: now.Add(-1500 * time.Millisecond),
			Provider:    provider3,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "3.3.1.1", Healthy: true, FailureCount: 0},
				{IP: "3.3.2.2", Healthy: false, FailureCount: 3},
				{IP: "3.3.3.3", Healthy: false, FailureCount: 7},
			},
		},
		"emptyendpoints.test": {
			LastUpdated: now.Add(-24 * time.Hour),
			Provider:    provider1,
			Error:       "",
			Endpoints:   []healthcheck.DomainStatusEndpoint{},
		},
		"ipv6.example": {
			LastUpdated: now.Add(-15 * time.Second),
			Provider:    provider2,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "2001:db8::1", Healthy: true, FailureCount: 0, Since: ago(3 * time.Hour)},
				{IP: "2001:db8::2", Healthy: false, FailureCount: 4, Since: ago(2 * time.Minute), LastFailure: ago(15 * time.Second)},
			},
		},
	}
}

func (m mockStatusProvider) GetDomainStatus(domain string) *healthcheck.DomainStatus {
	return nil
}

func (m mockStatusProvider) ForceCheck(ctx context.Context) error {
	// Simulate a check taking a moment
	select {
	case <-time.After(1500 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck
	}
}
