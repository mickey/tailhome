package main

import (
	"math"
	"strconv"
	"testing"
)

func TestPaletteContrast(t *testing.T) {
	luminances := map[string]map[string]float64{}
	for _, family := range palettes {
		if len(family.Modes) != 2 {
			t.Errorf("%s defines %d modes, want light and dark", family.ID, len(family.Modes))
		}
		for _, mode := range family.Modes {
			key := family.ID + "/" + mode.Name
			if _, exists := luminances[key]; exists {
				t.Fatalf("duplicate palette %s", key)
			}
			roles := map[string]float64{}
			for _, role := range paletteRoles {
				color, ok := mode.Colors[role]
				if !ok {
					t.Errorf("%s missing --%s", key, role)
					continue
				}
				roles[role] = luminance(color)
			}
			if len(mode.Colors) != len(paletteRoles) {
				t.Errorf("%s defines %d roles, want %d", key, len(mode.Colors), len(paletteRoles))
			}
			luminances[key] = roles
		}
	}
	ratio := func(a, b float64) float64 { return (math.Max(a, b) + 0.05) / (math.Min(a, b) + 0.05) }
	for _, family := range palettes {
		for _, mode := range []string{"light", "dark"} {
			key := family.ID + "/" + mode
			p, ok := luminances[key]
			if !ok {
				t.Errorf("%s missing palette", key)
				continue
			}
			check := func(a, b string, minimum float64) {
				t.Helper()
				if got := ratio(p[a], p[b]); got+1e-9 < minimum {
					t.Errorf("%s --%s/--%s ratio %.6f, want >= %.6f", key, a, b, got, minimum)
				}
			}
			for _, bg := range []string{"bg", "card"} {
				for _, fg := range []string{"fg", "muted"} {
					check(fg, bg, 4.5)
				}
				for _, fg := range []string{"up", "down", "unknown", "warn"} {
					check(fg, bg, 3)
				}
				check("line", bg, 1.35)
			}
			check("ok", "card", 4.5)
			check("ok", "bg", 4.5)
			check("fg", "muted", 1.1) // Muted text must stay distinct from body text.
			baseline := luminances["default/"+mode]
			check("card", "bg", ratio(baseline["card"], baseline["bg"]))
		}
	}
}

// luminance is the WCAG 2 relative luminance of a #rrggbb color.
func luminance(hex string) float64 {
	n, _ := strconv.ParseUint(hex[1:], 16, 32) // palette colors are validated at startup
	linear := func(c uint64) float64 {
		x := float64(c) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*linear(n>>16) + 0.7152*linear((n>>8)&255) + 0.0722*linear(n&255)
}
