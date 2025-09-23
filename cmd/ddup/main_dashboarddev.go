//go:build dashboarddev

package main

import (
	"time"

	"github.com/italypaleale/ddup/pkg/healthcheck"
)

func init() {
	// Mock the status provider so it returns static data
	statusProvider = &mockStatusProvider{}
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

	return map[string]healthcheck.DomainStatus{
		"example.com": {
			LastUpdated: now.Add(-1 * time.Second),
			Provider:    provider1,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "1.1.1.1", Healthy: true, FailureCount: 0},
				{IP: "1.1.2.2", Healthy: false, FailureCount: 101},
				{IP: "1.1.3.3", Healthy: true, FailureCount: 0},
			},
		},
		"mysite.xyz": {
			LastUpdated: now.Add(-2 * time.Second),
			Provider:    provider1,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "2.2.1.1", Healthy: true, FailureCount: 0},
				{IP: "2.2.2.2", Healthy: true, FailureCount: 1},
				{IP: "2.2.3.3", Healthy: true, FailureCount: 1},
			},
		},
		"alpha.dev": {
			LastUpdated: now.Add(-2500 * time.Millisecond),
			Provider:    provider2,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "10.10.10.1", Healthy: true, FailureCount: 0},
				{IP: "10.10.10.2", Healthy: true, FailureCount: 1},
			},
		},
		"beta.io": {
			LastUpdated: now.Add(-5 * time.Second),
			Provider:    provider2,
			Error:       "",
			Endpoints: []healthcheck.DomainStatusEndpoint{
				{IP: "172.16.0.1", Healthy: false, FailureCount: 12},
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
				{IP: "2001:db8::1", Healthy: true, FailureCount: 0},
				{IP: "2001:db8::2", Healthy: false, FailureCount: 4},
			},
		},
	}
}

func (m mockStatusProvider) GetDomainStatus(domain string) *healthcheck.DomainStatus {
	return nil
}
