package dns

import (
	"fmt"
	"net/netip"
)

const (
	recordTypeA    = "A"
	recordTypeAAAA = "AAAA"
)

// recordTypeForIP returns the DNS record type appropriate for an IP address.
func recordTypeForIP(ip string) (string, error) {
	parsed, err := parseIP(ip)
	if err != nil {
		return "", err
	}
	if parsed.Is4() {
		return recordTypeA, nil
	}
	return recordTypeAAAA, nil
}

func canonicalizeIP(ip string) (string, error) {
	parsed, err := parseIP(ip)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func canonicalizeIPs(ips []string) ([]string, error) {
	canonical := make([]string, len(ips))
	for i, ip := range ips {
		var err error
		canonical[i], err = canonicalizeIP(ip)
		if err != nil {
			return nil, err
		}
	}
	return canonical, nil
}

func parseIP(ip string) (netip.Addr, error) {
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP address %q", ip)
	}
	return parsed, nil
}

// splitIPs separates a list of IP addresses into IPv4 and IPv6 addresses.
func splitIPs(ips []string) (ipv4 []string, ipv6 []string, err error) {
	ipv4 = make([]string, 0, len(ips))
	ipv6 = make([]string, 0, len(ips))

	for _, ip := range ips {
		parsed, parseErr := parseIP(ip)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		if parsed.Is4() {
			ipv4 = append(ipv4, parsed.String())
		} else {
			ipv6 = append(ipv6, parsed.String())
		}
	}

	return ipv4, ipv6, nil
}
