package config

import (
	"fmt"
	"strconv"
	"strings"
)

// StatusMatcher matches HTTP status codes against a set of inclusive ranges
type StatusMatcher struct {
	ranges [][2]int
}

// DefaultStatusMatcher matches 2xx status codes
func DefaultStatusMatcher() StatusMatcher {
	return StatusMatcher{ranges: [][2]int{{200, 299}}}
}

// ParseStatusMatcher parses a list of status specs.
// Each item is a code ("200"), an inclusive range ("200-299") or a class ("2xx").
// An empty list returns the default matcher (2xx).
func ParseStatusMatcher(specs []string) (StatusMatcher, error) {
	if len(specs) == 0 {
		return DefaultStatusMatcher(), nil
	}

	m := StatusMatcher{ranges: make([][2]int, 0, len(specs))}
	for _, spec := range specs {
		r, err := parseStatusSpec(strings.TrimSpace(spec))
		if err != nil {
			return StatusMatcher{}, err
		}
		m.ranges = append(m.ranges, r)
	}
	return m, nil
}

func parseStatusSpec(spec string) ([2]int, error) {
	// Class, like "2xx"
	if len(spec) == 3 && (spec[1:] == "xx" || spec[1:] == "XX") && spec[0] >= '1' && spec[0] <= '5' {
		base := int(spec[0]-'0') * 100
		return [2]int{base, base + 99}, nil
	}

	// Range, like "200-299"
	lo, hi, isRange := strings.Cut(spec, "-")
	if !isRange {
		hi = lo
	}

	l, err1 := strconv.Atoi(lo)
	h, err2 := strconv.Atoi(hi)
	if err1 != nil || err2 != nil || l < 100 || h > 599 || l > h {
		return [2]int{}, fmt.Errorf("invalid status code spec %q: must be a code (200), a range (200-299) or a class (2xx)", spec)
	}
	return [2]int{l, h}, nil
}

// Match returns true if the status code is matched
func (m StatusMatcher) Match(code int) bool {
	if len(m.ranges) == 0 {
		m = DefaultStatusMatcher()
	}
	for _, r := range m.ranges {
		if code >= r[0] && code <= r[1] {
			return true
		}
	}
	return false
}
