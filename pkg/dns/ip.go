package dns

import (
	"fmt"
	"net"
)

const (
	recordTypeA    = "A"
	recordTypeAAAA = "AAAA"
)

// recordTypeForIP returns the DNS record type appropriate for an IP address.
func recordTypeForIP(ip string) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return "", fmt.Errorf("invalid IP address %q", ip)
	}
	if parsed.To4() != nil {
		return recordTypeA, nil
	}
	return recordTypeAAAA, nil
}

// splitIPs separates a list of IP addresses into IPv4 and IPv6 addresses.
func splitIPs(ips []string) (ipv4 []string, ipv6 []string, err error) {
	ipv4 = make([]string, 0, len(ips))
	ipv6 = make([]string, 0, len(ips))

	for _, ip := range ips {
		recordType, typeErr := recordTypeForIP(ip)
		if typeErr != nil {
			return nil, nil, typeErr
		}
		if recordType == recordTypeA {
			ipv4 = append(ipv4, ip)
		} else {
			ipv6 = append(ipv6, ip)
		}
	}

	return ipv4, ipv6, nil
}
