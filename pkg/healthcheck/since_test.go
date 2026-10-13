package healthcheck

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/italypaleale/ddup/pkg/config"
	"github.com/italypaleale/ddup/pkg/dns"
	"github.com/italypaleale/ddup/pkg/healthcheck/checker"
)

type sinceFixture struct {
	hc   *HealthChecker
	mock *checker.MockChecker
	ep   *config.ConfigEndpoint
	now  time.Time
}

func newSinceFixture(maxAttempts int) *sinceFixture {
	f := &sinceFixture{
		ep:  &config.ConfigEndpoint{Name: "endpoint1", IP: "1.1.1.1"},
		now: time.Date(2026, 10, 12, 14, 0, 0, 0, time.UTC),
		mock: &checker.MockChecker{
			Domain:      "example.com",
			MaxAttempts: maxAttempts,
		},
	}
	f.hc = &HealthChecker{
		domainCheckers: map[string]*domainChecker{
			"example.com": {
				checker:   f.mock,
				ttl:       60,
				failedIPs: map[string]int{},
				provider:  dns.NewMockProvider(false),
				clock:     func() time.Time { return f.now },
			},
		},
	}
	return f
}

// check runs a cycle one minute after the previous one, with the endpoint healthy or not
func (f *sinceFixture) check(t *testing.T, healthy bool) DomainStatusEndpoint {
	t.Helper()

	f.now = f.now.Add(time.Minute)
	res := checker.Result{
		Endpoint: f.ep,
		Healthy:  healthy,
	}
	if !healthy {
		res.Error = errors.New("connection failed")
	}
	f.mock.Results = []checker.Result{res}
	f.hc.checkAndUpdateDNS(t.Context())

	status := f.hc.GetDomainStatus("example.com")
	require.NotNil(t, status)
	require.Len(t, status.Endpoints, 1)
	return status.Endpoints[0]
}

func TestEndpointSince_FirstCheckHealthy(t *testing.T) {
	f := newSinceFixture(1)

	e := f.check(t, true)
	require.NotNil(t, e.Since)
	assert.Equal(t, f.now, *e.Since)
	assert.True(t, e.SinceStart, "the time of the first check is not a state change")
	assert.Nil(t, e.LastFailure)

	// Staying healthy changes nothing
	first := f.now
	e = f.check(t, true)
	require.NotNil(t, e.Since)
	assert.Equal(t, first, *e.Since)
	assert.True(t, e.SinceStart)
	assert.Nil(t, e.LastFailure)
}

func TestEndpointSince_FirstCheckFailing(t *testing.T) {
	f := newSinceFixture(1)

	e := f.check(t, false)
	assert.False(t, e.Healthy)
	require.NotNil(t, e.Since)
	assert.Equal(t, f.now, *e.Since)
	assert.True(t, e.SinceStart)
	require.NotNil(t, e.LastFailure)
	assert.Equal(t, f.now, *e.LastFailure)
}

func TestEndpointSince_FailAndRecover(t *testing.T) {
	f := newSinceFixture(1)

	e := f.check(t, true)
	require.NotNil(t, e.Since)
	startedAt := *e.Since

	// Healthy to failing
	e = f.check(t, false)
	assert.False(t, e.Healthy)
	require.NotNil(t, e.Since)
	assert.Equal(t, f.now, *e.Since)
	assert.True(t, e.Since.After(startedAt))
	assert.False(t, e.SinceStart)
	failedAt := f.now
	require.NotNil(t, e.LastFailure)
	assert.Equal(t, failedAt, *e.LastFailure)

	// Still failing: since stays, last failure moves
	e = f.check(t, false)
	require.NotNil(t, e.Since)
	assert.Equal(t, failedAt, *e.Since)
	require.NotNil(t, e.LastFailure)
	assert.Equal(t, f.now, *e.LastFailure)
	lastFailure := f.now

	// Failing to healthy: since moves, last failure is kept
	e = f.check(t, true)
	assert.True(t, e.Healthy)
	require.NotNil(t, e.Since)
	assert.Equal(t, f.now, *e.Since)
	assert.False(t, e.SinceStart)
	require.NotNil(t, e.LastFailure)
	assert.Equal(t, lastFailure, *e.LastFailure)

	// Staying healthy keeps both
	recovered := f.now
	e = f.check(t, true)
	require.NotNil(t, e.Since)
	assert.Equal(t, recovered, *e.Since)
	require.NotNil(t, e.LastFailure)
	assert.Equal(t, lastFailure, *e.LastFailure)
}

func TestEndpointSince_RetriesAreNotStateChanges(t *testing.T) {
	f := newSinceFixture(3)

	e := f.check(t, true)
	require.NotNil(t, e.Since)
	startedAt := *e.Since

	// A failed check that is still within the allowed attempts moves the last failure, not since
	e = f.check(t, false)
	assert.True(t, e.Healthy)
	require.NotNil(t, e.Since)
	assert.Equal(t, startedAt, *e.Since)
	require.NotNil(t, e.LastFailure)
	assert.Equal(t, f.now, *e.LastFailure)
}

func TestEndpointSince_RecoverAfter(t *testing.T) {
	f := newSinceFixture(1)
	f.mock.RecoverAfter = 2

	f.check(t, true)
	e := f.check(t, false)
	require.NotNil(t, e.Since)
	failedAt := *e.Since

	// A passing check that is not enough to recover does not change the state
	e = f.check(t, true)
	assert.False(t, e.Healthy)
	require.NotNil(t, e.Since)
	assert.Equal(t, failedAt, *e.Since)

	// The second one does
	e = f.check(t, true)
	assert.True(t, e.Healthy)
	require.NotNil(t, e.Since)
	assert.Equal(t, f.now, *e.Since)
	assert.False(t, e.SinceStart)
}

func TestEndpointSince_Flapping(t *testing.T) {
	f := newSinceFixture(1)

	f.check(t, true)
	for range 3 {
		e := f.check(t, false)
		require.NotNil(t, e.Since)
		assert.Equal(t, f.now, *e.Since)

		e = f.check(t, true)
		require.NotNil(t, e.Since)
		assert.Equal(t, f.now, *e.Since)
		assert.False(t, e.SinceStart)
		require.NotNil(t, e.LastFailure)
		assert.Equal(t, f.now.Add(-time.Minute), *e.LastFailure)
	}
}

func TestEndpointSince_RemovedEndpointIsForgotten(t *testing.T) {
	f := newSinceFixture(1)
	dc := f.hc.domainCheckers["example.com"]

	f.check(t, true)
	require.NotNil(t, dc.getTimes("1.1.1.1"))

	// The endpoint is not checked any more
	f.mock.Results = nil
	f.hc.checkAndUpdateDNS(t.Context())
	assert.Nil(t, dc.getTimes("1.1.1.1"))
	assert.Empty(t, dc.times)
}

func TestEndpointSince_APIFields(t *testing.T) {
	marshal := func(e DomainStatusEndpoint) map[string]any {
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		m := map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &m))
		return m
	}

	f := newSinceFixture(1)
	f.check(t, true)
	m := marshal(f.check(t, false))
	assert.Equal(t, f.now.Format(time.RFC3339Nano), m["since"])
	assert.Equal(t, f.now.Format(time.RFC3339Nano), m["lastFailure"])
	assert.NotContains(t, m, "sinceStart")

	// A new endpoint reports that its start is not known, and has no last failure
	g := newSinceFixture(1)
	m = marshal(g.check(t, true))
	assert.Contains(t, m, "since")
	assert.Equal(t, true, m["sinceStart"])
	assert.NotContains(t, m, "lastFailure")

	// Without times, all three fields are omitted
	m = marshal(DomainStatusEndpoint{IP: "1.1.1.1"})
	assert.NotContains(t, m, "since")
	assert.NotContains(t, m, "sinceStart")
	assert.NotContains(t, m, "lastFailure")
}
