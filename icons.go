package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	iconRoot = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/"
	iconBase = iconRoot + "svg/"
)

// Icons matches Service names to dashboard-icons entries, so immich-server
// gets Immich's icon and a name with no icon shows its first letter instead
// of a request that fails. Until the list of icons loads, names are used as-is.
type Icons struct {
	Fetch  func(context.Context) ([]iconFile, error) // available icons
	Loaded func()                                    // called after each successful load; may be nil

	mu       sync.Mutex
	exact    map[string]iconFile
	squashed map[string]iconFile // keyed by name without hyphens, for homeassistant → home-assistant
	next     time.Time           // when to fetch the list again
	loading  bool
}

// iconFile is an icon in dashboard-icons, as paths under iconRoot. Dark is
// its variant drawn for dark backgrounds, when it has one.
type iconFile struct {
	Name, File, Dark string
}

// load fetches the list in the background once a day, or an hour after a
// failure, so a slow or blocked CDN never delays discovery.
func (ic *Icons) load(ctx context.Context) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.loading || time.Now().Before(ic.next) {
		return
	}
	ic.loading = true
	go func() {
		icons, err := ic.Fetch(ctx)
		ic.mu.Lock()
		ic.loading = false
		if err != nil {
			if ctx.Err() == nil { // shutting down is not worth a log line
				log.Printf("icons: %v", err)
			}
			ic.next = time.Now().Add(time.Hour)
			ic.mu.Unlock()
			return
		}
		// The first of several names squashing alike wins, every time.
		slices.SortFunc(icons, func(a, b iconFile) int {
			return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.File, b.File))
		})
		ic.exact, ic.squashed = map[string]iconFile{}, map[string]iconFile{}
		for _, f := range icons {
			if _, ok := ic.exact[f.Name]; !ok {
				ic.exact[f.Name] = f
			}
			if s := squash(f.Name); ic.squashed[s].File == "" {
				ic.squashed[s] = f
			}
		}
		ic.next = time.Now().Add(24 * time.Hour)
		ic.mu.Unlock()
		if ic.Loaded != nil {
			ic.Loaded()
		}
	}()
}

// url returns the icon for a Service name and its dark-background variant,
// or "" if none matches.
func (ic *Icons) url(name string) (light, dark string) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.exact == nil {
		return iconURL(name), ""
	}
	// Drop trailing words one at a time: immich-server → immich, metabase-prod → metabase.
	parts := strings.Split(name, "-")
	for n := len(parts); n > 0; n-- {
		c := strings.Join(parts[:n], "-")
		if n < len(parts) && len(c) < 3 { // go-links isn't Go, x-wing isn't X
			break
		}
		if f, ok := ic.exact[c]; ok {
			return f.urls()
		}
		if f := ic.squashed[squash(c)]; f.File != "" {
			return f.urls()
		}
	}
	return "", ""
}

// named returns an icon chosen by name, such as an icon override, with its
// dark-background variant when the list knows it. Unknown names are used
// as-is, since the list can lag behind the CDN.
func (ic *Icons) named(name string) (light, dark string) {
	if ic != nil && !strings.Contains(name, "/") {
		ic.mu.Lock()
		f, ok := ic.exact[name]
		ic.mu.Unlock()
		if ok {
			return f.urls()
		}
	}
	return iconURL(name), ""
}

func (f iconFile) urls() (light, dark string) {
	light = iconRoot + f.File
	if f.Dark != "" {
		dark = iconRoot + f.Dark
	}
	return light, dark
}

func squash(s string) string { return strings.ReplaceAll(s, "-", "") }

// fetchIconNames downloads dashboard-icons' index of icons. It reveals no Service names.
// The index is metadata.json: tree.json stopped listing new icons in September 2026.
func fetchIconNames(ctx context.Context) ([]iconFile, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", iconRoot+"metadata.json", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loading icon list: HTTP %d", resp.StatusCode)
	}
	icons, err := iconFiles(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("loading icon list: %w", err)
	}
	return icons, nil
}

var iconName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// iconFiles reads metadata.json. Each icon comes in its base format, SVG when
// there is one, PNG otherwise. An icon with color variants matches by its name
// to the variant drawn for light backgrounds (colors.dark), with the one for
// dark backgrounds (colors.light) as its dark variant; each variant's own
// name matches that exact file.
func iconFiles(r io.Reader) ([]iconFile, error) {
	var meta map[string]struct {
		Base   string
		Colors struct{ Dark, Light string }
	}
	if err := json.NewDecoder(r).Decode(&meta); err != nil {
		return nil, err
	}
	icons := make([]iconFile, 0, len(meta))
	for name, m := range meta {
		if m.Base != "svg" && m.Base != "png" || !iconName.MatchString(name) { // names become URL paths
			continue
		}
		file := func(n string) string { return m.Base + "/" + n + "." + m.Base }
		onLight, onDark := name, name
		// Use the variants only when the icon itself is one of them: Spotify's
		// own icon is green, and its variants are plain black and white.
		if (m.Colors.Dark == name || m.Colors.Light == name) && iconName.MatchString(m.Colors.Dark) && iconName.MatchString(m.Colors.Light) {
			onLight, onDark = m.Colors.Dark, m.Colors.Light
		}
		f := iconFile{Name: name, File: file(onLight)}
		if onDark != onLight {
			f.Dark = file(onDark)
		}
		icons = append(icons, f)
		if onLight != name {
			icons = append(icons, iconFile{Name: onLight, File: file(onLight)})
		}
		if onDark != name && onDark != onLight {
			icons = append(icons, iconFile{Name: onDark, File: file(onDark)})
		}
	}
	return icons, nil
}
