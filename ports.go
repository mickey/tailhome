package main

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// parsePreviewPorts bounds expansion before allocating a discovery pool.
func parsePreviewPorts(value string) ([]uint16, error) {
	ports := []uint16{}
	if strings.TrimSpace(value) == "" {
		return ports, nil
	}
	bad := func() ([]uint16, error) {
		return nil, fmt.Errorf("TAILHOME_PREVIEW_PORTS must contain decimal ports or ascending ranges, excluding 443, with at most 32 distinct ports: %q", value)
	}
	number := func(s string) (int, bool) { // ParseUint rejects signs, blanks and non-digits.
		n, err := strconv.ParseUint(s, 10, 16)
		return int(n), err == nil && n != 0
	}
	for _, token := range strings.Split(value, ",") {
		parts := strings.Split(strings.TrimSpace(token), "-")
		if len(parts) > 2 {
			return bad()
		}
		lo, ok := number(parts[0])
		if !ok {
			return bad()
		}
		hi := lo
		if len(parts) == 2 {
			hi, ok = number(parts[1])
			if !ok {
				return bad()
			}
		}
		if hi < lo || hi-lo+1 > 32 || lo <= 443 && hi >= 443 {
			return bad()
		}
		for n := lo; n <= hi; n++ {
			p := uint16(n)
			if !slices.Contains(ports, p) {
				ports = append(ports, p)
				if len(ports) > 32 {
					return bad()
				}
			}
		}
	}
	slices.Sort(ports)
	return ports, nil
}
