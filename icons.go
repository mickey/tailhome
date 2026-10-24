package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

const iconBase = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/"

// Icons matches Service names to dashboard-icons entries, so immich-server
// gets Immich's icon and a name with no icon shows its first letter instead
// of a request that fails. Until the list of icons loads, names are used as-is.
type Icons struct {
	Fetch  func(context.Context) ([]string, error) // names of the available icons
	Loaded func()                                  // called after each successful load; may be nil

	mu       sync.Mutex
	exact    map[string]bool
	squashed map[string]string // name without hyphens → name, for homeassistant → home-assistant
	next     time.Time         // when to fetch the list again
	loading  bool
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
		names, err := ic.Fetch(ctx)
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
		slices.Sort(names) // the first of several names squashing alike wins, every time
		ic.exact, ic.squashed = map[string]bool{}, map[string]string{}
		for _, n := range names {
			ic.exact[n] = true
			if s := squash(n); ic.squashed[s] == "" {
				ic.squashed[s] = n
			}
		}
		ic.next = time.Now().Add(24 * time.Hour)
		ic.mu.Unlock()
		if ic.Loaded != nil {
			ic.Loaded()
		}
	}()
}

// url returns the icon for a Service name, or "" if none matches.
func (ic *Icons) url(name string) string {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.exact == nil {
		return iconURL(name)
	}
	// Drop trailing words one at a time: immich-server → immich, metabase-prod → metabase.
	parts := strings.Split(name, "-")
	for n := len(parts); n > 0; n-- {
		c := strings.Join(parts[:n], "-")
		if n < len(parts) && len(c) < 3 { // go-links isn't Go, x-wing isn't X
			break
		}
		if ic.exact[c] {
			return iconBase + c + ".svg"
		}
		if m := ic.squashed[squash(c)]; m != "" {
			return iconBase + m + ".svg"
		}
	}
	return ""
}

func squash(s string) string { return strings.ReplaceAll(s, "-", "") }

// fetchIconNames downloads dashboard-icons' list of files. It reveals no Service names.
func fetchIconNames(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/tree.json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loading icon list: HTTP %d", resp.StatusCode)
	}
	var tree struct{ SVG []string }
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, fmt.Errorf("loading icon list: %w", err)
	}
	names := make([]string, 0, len(tree.SVG))
	for _, f := range tree.SVG {
		if n, ok := strings.CutSuffix(f, ".svg"); ok {
			names = append(names, n)
		}
	}
	return names, nil
}
