package checker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/italypaleale/ddup/pkg/config"
)

const (
	// Responses larger than this are rejected; a response with an IP address is tiny
	maxLookupResponseSize = 4 << 10

	// Lookups with the same settings made within this time are shared, so several domains that use the same service don't each call it
	lookupCacheTTLDefault = 10 * time.Second
)

// How long lookups are cached; this is a variable so tests can turn the cache off
var lookupCacheTTL = lookupCacheTTLDefault

type lookupCacheEntry struct {
	ip      string
	expires time.Time
}

var (
	lookupCacheLock sync.Mutex
	lookupCache     = map[string]lookupCacheEntry{}
)

// lookupIP finds the public IP address by calling the services in the lookup configuration, in order, until one works
func lookupIP(ctx context.Context, spec *config.ConfigIPLookup, timeout time.Duration) (string, error) {
	cacheKey := fmt.Sprintf("%v|%d|%s", spec.URLs, spec.Family, spec.Pattern)
	lookupCacheLock.Lock()
	entry, ok := lookupCache[cacheKey]
	lookupCacheLock.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return entry.ip, nil
	}

	var pattern *regexp.Regexp
	if spec.Pattern != "" {
		// Validated when the config is loaded
		pattern = regexp.MustCompile(spec.Pattern)
	}

	var errs []error
	for _, u := range spec.URLs {
		ip, err := lookupIPFromURL(ctx, u, spec.Family, pattern, timeout)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", u, err))
			continue
		}

		lookupCacheLock.Lock()
		lookupCache[cacheKey] = lookupCacheEntry{ip: ip, expires: time.Now().Add(lookupCacheTTL)}
		lookupCacheLock.Unlock()
		return ip, nil
	}

	return "", fmt.Errorf("IP lookup failed: %w", errors.Join(errs...))
}

func lookupIPFromURL(ctx context.Context, url string, family int, pattern *regexp.Regexp, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "ddup/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("status code %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLookupResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}
	if len(body) > maxLookupResponseSize {
		return "", errors.New("response is too large")
	}

	return parseLookupResponse(string(body), family, pattern)
}

// parseLookupResponse extracts and validates the IP address from the response of a lookup service
func parseLookupResponse(body string, family int, pattern *regexp.Regexp) (string, error) {
	value := strings.TrimSpace(body)
	if pattern != nil {
		m := pattern.FindStringSubmatch(body)
		if m == nil {
			return "", errors.New("response doesn't match the pattern")
		}
		value = strings.TrimSpace(m[1])
	}

	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", fmt.Errorf("response is not an IP address: %q", truncate(value, 64))
	}
	// An IPv4 address that some services return mapped to IPv6
	addr = addr.Unmap()

	switch {
	case family == 4 && !addr.Is4():
		return "", fmt.Errorf("got %s, but an IPv4 address is expected", addr)
	case family == 6 && !addr.Is6():
		return "", fmt.Errorf("got %s, but an IPv6 address is expected", addr)
	case !addr.IsGlobalUnicast() || addr.IsPrivate():
		return "", fmt.Errorf("%s is not a public IP address", addr)
	}

	return addr.String(), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
