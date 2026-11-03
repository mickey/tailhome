package main

import (
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

type paletteMode struct {
	Name   string
	Colors map[string]string
}

type paletteFamily struct {
	ID    string
	Name  string
	Modes []paletteMode
}

var paletteRoles = []string{"bg", "card", "fg", "muted", "line", "up", "ok", "down", "unknown", "warn"}

var palettes = []paletteFamily{
	{ID: "default", Name: "Default", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#f6f6f4", "card": "#ffffff", "fg": "#1c1c1a", "muted": "#6b6b66", "line": "#d5d5d1", "up": "#2f9e5b", "ok": "#1f7a43", "down": "#d1453b", "unknown": "#8a8a83", "warn": "#be8311"}},
		{Name: "dark", Colors: map[string]string{"bg": "#151514", "card": "#1f1f1d", "fg": "#ececea", "muted": "#a3a39c", "line": "#393936", "up": "#45b46f", "ok": "#5cc985", "down": "#d1453b", "unknown": "#a3a39c", "warn": "#be8311"}},
	}},
	{ID: "catppuccin", Name: "Catppuccin", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#eff1f5", "card": "#dce0e8", "fg": "#4c4f69", "muted": "#5c5f77", "line": "#bcc0cc", "up": "#388b25", "ok": "#2b6c1d", "down": "#d20f39", "unknown": "#6c6f85", "warn": "#b27117"}},
		{Name: "dark", Colors: map[string]string{"bg": "#1e1e2e", "card": "#11111b", "fg": "#cdd6f4", "muted": "#a6adc8", "line": "#45475a", "up": "#a6e3a1", "ok": "#a6e3a1", "down": "#f38ba8", "unknown": "#7f849c", "warn": "#f9e2af"}},
	}},
	{ID: "gruvbox", Name: "Gruvbox", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#f2e5bc", "card": "#f9f5d7", "fg": "#3c3836", "muted": "#665c54", "line": "#d5c4a1", "up": "#79740e", "ok": "#3d7151", "down": "#9d0006", "unknown": "#908172", "warn": "#b57614"}},
		{Name: "dark", Colors: map[string]string{"bg": "#282828", "card": "#3c3836", "fg": "#ebdbb2", "muted": "#bdae93", "line": "#524b47", "up": "#b8bb26", "ok": "#b8bb26", "down": "#fb4b36", "unknown": "#a89984", "warn": "#fabd2f"}},
	}},
	{ID: "tokyo-night", Name: "Tokyo Night", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#d0d5e3", "card": "#e1e2e7", "fg": "#233d7b", "muted": "#414c76", "line": "#b4b5b9", "up": "#587539", "ok": "#4c6431", "down": "#df265c", "unknown": "#74778d", "warn": "#8c6c3e"}},
		{Name: "dark", Colors: map[string]string{"bg": "#1f2231", "card": "#15161e", "fg": "#c0caf5", "muted": "#a9b1d6", "line": "#414868", "up": "#9ece6a", "ok": "#9ece6a", "down": "#f7768e", "unknown": "#a9b1d6", "warn": "#e0af68"}},
	}},
	{ID: "rose-pine", Name: "Rosé Pine", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#f8f0e7", "card": "#fffaf3", "fg": "#464261", "muted": "#6e6a86", "line": "#cecacd", "up": "#6d8f89", "ok": "#286983", "down": "#b4637a", "unknown": "#907aa9", "warn": "#bc7e2a"}},
		{Name: "dark", Colors: map[string]string{"bg": "#16141f", "card": "#21202e", "fg": "#e0def4", "muted": "#95b1ac", "line": "#403d52", "up": "#95b1ac", "ok": "#95b1ac", "down": "#eb6f92", "unknown": "#908caa", "warn": "#f6c177"}},
	}},
	{ID: "solarized", Name: "Solarized", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#fdf6e3", "card": "#eee8d5", "fg": "#002b36", "muted": "#073642", "line": "#93a1a1", "up": "#758600", "ok": "#5b6900", "down": "#dc322f", "unknown": "#657b83", "warn": "#a87f00"}},
		{Name: "dark", Colors: map[string]string{"bg": "#002a35", "card": "#073642", "fg": "#eee8d5", "muted": "#93a1a1", "line": "#576c73", "up": "#859900", "ok": "#9bab2e", "down": "#e1524f", "unknown": "#839496", "warn": "#b58900"}},
	}},
	{ID: "kanagawa", Name: "Kanagawa", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#f2ecbc", "card": "#e5ddb0", "fg": "#49443c", "muted": "#545464", "line": "#c6bf97", "up": "#677f49", "ok": "#46615d", "down": "#c84053", "unknown": "#727169", "warn": "#77713f"}},
		{Name: "dark", Colors: map[string]string{"bg": "#1f1f28", "card": "#2a2a37", "fg": "#dcd7ba", "muted": "#87a987", "line": "#49443c", "up": "#98bb6c", "ok": "#98bb6c", "down": "#e46876", "unknown": "#7a8382", "warn": "#e6c384"}},
	}},
	{ID: "one", Name: "One", Modes: []paletteMode{
		{Name: "light", Colors: map[string]string{"bg": "#f0f0f0", "card": "#fafafa", "fg": "#383a42", "muted": "#696c77", "line": "#c9c9c9", "up": "#4d9b4c", "ok": "#3d7a3c", "down": "#e45649", "unknown": "#818387", "warn": "#986801"}},
		{Name: "dark", Colors: map[string]string{"bg": "#21252b", "card": "#31353f", "fg": "#cbd0d8", "muted": "#abb2bf", "line": "#434953", "up": "#98c379", "ok": "#98c379", "down": "#e86671", "unknown": "#848b98", "warn": "#e5c07b"}},
	}},
}

var paletteIDs, paletteCSS = renderPalettes()

// Only validated IDs and hex colors enter CSS.
func renderPalettes() ([]string, template.CSS) {
	var ids []string
	var css strings.Builder
	idPattern := regexp.MustCompile(`^[a-z]+(?:-[a-z]+)*$`)
	hexPattern := regexp.MustCompile(`^#[0-9a-f]{6}$`)
	for _, family := range palettes {
		if !idPattern.MatchString(family.ID) {
			panic("invalid palette ID: " + family.ID)
		}
		ids = append(ids, family.ID)
		for _, mode := range family.Modes {
			if mode.Name != "light" && mode.Name != "dark" {
				panic("invalid palette mode: " + mode.Name)
			}
			// Default light also supplies the no-script fallback.
			if family.ID == "default" && mode.Name == "light" {
				css.WriteString(`  :root, [data-theme="light"] [data-palette="default"]`)
			} else {
				fmt.Fprintf(&css, `  :root[data-theme="%s"][data-palette="%s"], [data-theme="%s"] [data-palette="%s"]`, mode.Name, family.ID, mode.Name, family.ID)
			}
			fmt.Fprintf(&css, " { color-scheme: %s;", mode.Name)
			for _, role := range paletteRoles {
				color := mode.Colors[role]
				if !hexPattern.MatchString(color) {
					panic("invalid palette color: " + family.ID + "/" + mode.Name + "/" + role)
				}
				fmt.Fprintf(&css, " --%s: %s;", role, color)
			}
			css.WriteString(" }\n")
		}
	}
	return ids, template.CSS(css.String())
}
