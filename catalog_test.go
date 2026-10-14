package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tsapi "tailscale.com/client/tailscale/v2"
)

type fakeLister struct {
	services []tsapi.Service
	err      error
	calls    int
}

func (f *fakeLister) List(context.Context) ([]tsapi.Service, error) {
	f.calls++
	return f.services, f.err
}

// fakeNet routes "<name>.ts.test:<port>" to local listeners registered in addrs.
type fakeNet struct {
	addrs map[string]string // "host:port" → local address; missing means unreachable
	dials atomic.Int64
}

func (n *fakeNet) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	n.dials.Add(1)
	local, ok := n.addrs[addr]
	if !ok {
		return nil, errors.New("unreachable")
	}
	var d net.Dialer
	return d.DialContext(ctx, network, local)
}

func (n *fakeNet) prober() *Prober {
	return NewProber(n.dial)
}

func serveHTTP(t *testing.T, status int) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func serveHTTPS(t *testing.T) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

func serveTCP(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func writeOverrides(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "overrides.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newCatalog(lister ServiceLister, n *fakeNet, overrides string) *Catalog {
	return &Catalog{
		Services:      lister,
		OverridesPath: overrides,
		DNSSuffix:     "ts.test",
		Self:          "svc:home",
		Prober:        n.prober(),
	}
}

// index flattens a View to name → entry, and name → section ("Other" or group).
func index(v View) (map[string]Entry, map[string]string) {
	entries, sections := map[string]Entry{}, map[string]string{}
	for _, g := range v.Groups {
		for _, e := range g.Entries {
			entries[e.Name], sections[e.Name] = e, g.Name
		}
	}
	for _, e := range v.Other {
		entries[e.Name], sections[e.Name] = e, "Other"
	}
	return entries, sections
}

func TestClassificationAndHealth(t *testing.T) {
	n := &fakeNet{addrs: map[string]string{
		"jellyfin.ts.test:80":   serveHTTP(t, 200),
		"grafana.ts.test:3000":  serveHTTP(t, 302),
		"broken.ts.test:80":     serveHTTP(t, 502),
		"postgres.ts.test:5432": serveTCP(t),
		"queue.ts.test:15672":   serveHTTP(t, 503),
		"proxmox.ts.test:8006":  serveHTTPS(t),
		"ranged.ts.test:8000":   serveHTTP(t, 200),
		"wildcard.ts.test:443":  serveHTTPS(t),
		"console.ts.test:80":    serveHTTP(t, 200),
		// "offline.ts.test:80" is unreachable.
	}}
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:home", Ports: []string{"tcp:443"}},
		{Name: "svc:jellyfin", Ports: []string{"tcp:80"}, Tags: []string{"tag:media"}},
		{Name: "svc:offline", Ports: []string{"tcp:80"}, Tags: []string{"tag:media"}},
		{Name: "svc:broken", Ports: []string{"tcp:80"}},
		{Name: "svc:grafana", Ports: []string{"tcp:3000"}, Tags: []string{"tag:home-lab"}},
		{Name: "svc:postgres", Ports: []string{"tcp:5432"}},
		{Name: "svc:redis", Ports: []string{"tcp:6379"}},
		{Name: "svc:queue", Ports: []string{"tcp:15672"}},
		{Name: "svc:proxmox", Ports: []string{"tcp:8006"}},
		{Name: "svc:ranged", Ports: []string{"udp:53", "tcp:8000-8010"}},
		{Name: "svc:wildcard", Ports: []string{"tcp:*"}},
		{Name: "svc:console", Ports: []string{"do-not-validate"}},
	}}
	c := newCatalog(lister, n, filepath.Join(t.TempDir(), "missing.yaml"))
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	want := []struct {
		name, section, url string
		status             Status
	}{
		{"jellyfin", "Media", "http://jellyfin.ts.test/", StatusUp},
		// A web Service that does not answer stays a tile, marked down.
		{"offline", "Media", "http://offline.ts.test/", StatusDown},
		{"broken", "Services", "http://broken.ts.test/", StatusDown},
		// Unusual port that answers HTTP is detected as web; a redirect counts as up.
		{"grafana", "Home Lab", "http://grafana.ts.test:3000/", StatusUp},
		{"postgres", "Other", "", StatusUp},
		{"redis", "Other", "", StatusDown},
		// Any HTTP answer makes a Service web, even a 503 on first discovery.
		{"queue", "Services", "http://queue.ts.test:15672/", StatusDown},
		// Self-signed certificates count as reachable.
		{"proxmox", "Services", "https://proxmox.ts.test:8006/", StatusUp},
		// A port range is probed on its first port.
		{"ranged", "Services", "http://ranged.ts.test:8000/", StatusUp},
		// tcp:* covers 443.
		{"wildcard", "Services", "https://wildcard.ts.test/", StatusUp},
		// Without a usable port, the default web ports are probed.
		{"console", "Services", "http://console.ts.test/", StatusUp},
	}
	for _, w := range want {
		e, ok := entries[w.name]
		if !ok {
			t.Errorf("%s: missing", w.name)
			continue
		}
		if sections[w.name] != w.section || e.URL != w.url || e.Status != w.status {
			t.Errorf("%s: got section=%q url=%q status=%s, want %q %q %s", w.name, sections[w.name], e.URL, e.Status, w.section, w.url, w.status)
		}
	}
	if _, ok := entries["home"]; ok {
		t.Error("own Service shown")
	}
}

func TestDetectedWebServiceStaysTileWhenDown(t *testing.T) {
	n := &fakeNet{addrs: map[string]string{"grafana.ts.test:3000": serveHTTP(t, 200)}}
	c := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:grafana", Ports: []string{"tcp:3000"}}}}, n, "")
	c.Refresh(t.Context())

	delete(n.addrs, "grafana.ts.test:3000")
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	if sections["grafana"] != "Services" || entries["grafana"].Status != StatusDown {
		t.Fatalf("got section=%q status=%s, want tile marked down", sections["grafana"], entries["grafana"].Status)
	}
}

func TestOverridesTakePrecedence(t *testing.T) {
	path := writeOverrides(t, `
svc:jellyfin:
  label: Movies
  group: Living Room
  icon: https://example.test/j.png
  description: override
grafana:
  url: https://grafana.ts.test/d/main
pgadmin:
  web: false
postgres:
  hidden: true
`)
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:jellyfin", DisplayName: "Jellyfin", Comment: "api", Ports: []string{"tcp:443"}, Tags: []string{"tag:media"}},
		{Name: "svc:grafana", Ports: []string{"tcp:9999"}},
		{Name: "svc:pgadmin", Ports: []string{"tcp:443"}},
		{Name: "svc:postgres", Ports: []string{"tcp:5432"}},
		{Name: "svc:wiki", DisplayName: "Team Wiki", Comment: "docs", Ports: []string{"tcp:443"}},
	}}
	c := newCatalog(lister, &fakeNet{}, path)
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	if e := entries["jellyfin"]; e.Label != "Movies" || sections["jellyfin"] != "Living Room" || e.Icon != "https://example.test/j.png" || e.Description != "override" {
		t.Errorf("jellyfin: %+v in %q", e, sections["jellyfin"])
	}
	if e := entries["grafana"]; e.URL != "https://grafana.ts.test/d/main" {
		t.Errorf("grafana url = %q", e.URL)
	}
	if sections["pgadmin"] != "Other" {
		t.Errorf("pgadmin in %q, want Other", sections["pgadmin"])
	}
	if _, ok := entries["postgres"]; ok {
		t.Error("hidden Service shown")
	}
	if e := entries["wiki"]; e.Label != "Team Wiki" || e.Description != "docs" || !strings.HasSuffix(e.Icon, "/wiki.svg") {
		t.Errorf("wiki: %+v", e)
	}
}

func TestOverridesRejectMistakes(t *testing.T) {
	for name, yaml := range map[string]string{
		"unknown key":   "jellyfin:\n  lable: Movies\n",
		"duplicate key": "jellyfin:\n  label: A\nsvc:jellyfin:\n  label: B\n",
	} {
		if _, err := LoadOverrides(writeOverrides(t, yaml)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestHandler(t *testing.T) {
	path := writeOverrides(t, `
evil:
  url: "javascript:alert(1)"
nas:
  url: vnc://nas.ts.test:5900
db:
  description: Main database
`)
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:evil", DisplayName: "Evil", Ports: []string{"tcp:443"}},
		{Name: "svc:nas", Ports: []string{"tcp:5900"}},
		{Name: "svc:db", Ports: []string{"tcp:5432"}},
		{Name: "svc:bare"},
	}}
	c := newCatalog(lister, &fakeNet{}, path)
	h := NewHandler(c, "start")

	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		return rec
	}
	if body := get("/api/services.json").Body.String(); !strings.Contains(body, `"groups":[]`) || !strings.Contains(body, `"other":[]`) {
		t.Errorf("JSON before first refresh: %s", body)
	}
	c.Refresh(t.Context())
	if body := get("/api/services.json").Body.String(); !strings.Contains(body, `"name":"bare"`) || strings.Contains(body, `"ports":null`) {
		t.Errorf("JSON ports: %s", body)
	}
	rec := get("/")
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, "<h1>start</h1>") || !strings.Contains(body, "Evil") {
		t.Fatalf("page: %d %s", rec.Code, body)
	}
	if strings.Contains(body, `href="javascript:`) {
		t.Error("javascript: URL rendered as a link")
	}
	if !strings.Contains(body, `href="vnc://nas.ts.test:5900"`) {
		t.Error("vnc: URL not rendered as a link")
	}
	if !strings.Contains(body, "Main database") {
		t.Error("description of an Other entry not rendered")
	}
}

func TestManualRefreshThrottledDuringOutage(t *testing.T) {
	lister := &fakeLister{err: errors.New("api down")}
	c := newCatalog(lister, &fakeNet{}, "")

	c.RefreshIfOlder(t.Context(), time.Minute)
	c.RefreshIfOlder(t.Context(), time.Minute)

	if lister.calls != 1 {
		t.Fatalf("API called %d times, want 1", lister.calls)
	}
	if v := c.View(); !v.Updated.IsZero() || v.Error != "api down" {
		t.Fatalf("got updated=%v error=%q, want no success and the error", v.Updated, v.Error)
	}
}

func TestOverrideEdgeCases(t *testing.T) {
	path := writeOverrides(t, `
pinned:
  web: true
vnc:
  url: vnc://vnc.ts.test:5900
shell:
  url: ssh://shell.ts.test
wild:
  web: false
`)
	n := &fakeNet{addrs: map[string]string{
		"vnc.ts.test:5900": serveTCP(t),
		"shell.ts.test:22": serveTCP(t),
		"wild.ts.test:443": serveTCP(t),
	}}
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:pinned", Ports: []string{"tcp:9000"}},
		{Name: "svc:vnc", Ports: []string{"tcp:5900"}},
		{Name: "svc:shell", Ports: []string{"tcp:22"}},
		{Name: "svc:wild", Ports: []string{"tcp:*"}},
	}}
	c := newCatalog(lister, n, path)
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	if e := entries["pinned"]; sections["pinned"] != "Services" || e.Status != StatusDown {
		t.Errorf("web: true: got section=%q status=%s, want tile marked down", sections["pinned"], e.Status)
	}
	if e := entries["vnc"]; e.URL != "vnc://vnc.ts.test:5900" || e.Status != StatusUp {
		t.Errorf("vnc url: got url=%q status=%s (%s), want TCP-checked tile", e.URL, e.Status, e.Reason)
	}
	if e := entries["shell"]; e.Status != StatusUp {
		t.Errorf("port-less ssh url: got status=%s (%s), want the Service's port checked", e.Status, e.Reason)
	}
	if e := entries["wild"]; sections["wild"] != "Other" || e.Status != StatusUp {
		t.Errorf("tcp:* web: false: got section=%q status=%s (%s), want Other checked on 443", sections["wild"], e.Status, e.Reason)
	}
}

// serveSilent accepts connections and reads forever without answering, like
// DNS over TCP waiting for the rest of what it takes for a length-prefixed query.
func serveSilent(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go io.Copy(io.Discard, c)
		}
	}()
	return ln.Addr().String()
}

func TestSilentDaemonIsUpUnderOther(t *testing.T) {
	n := &fakeNet{addrs: map[string]string{"dns.ts.test:53": serveSilent(t)}}
	c := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:dns", Ports: []string{"tcp:53"}}}}, n, "")
	c.Prober.timeout = 300 * time.Millisecond
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	if e := entries["dns"]; sections["dns"] != "Other" || e.Status != StatusUp {
		t.Fatalf("got section=%q status=%s (%s), want Other and up", sections["dns"], e.Status, e.Reason)
	}
}

func TestDetectedURLTriedFirst(t *testing.T) {
	n := &fakeNet{addrs: map[string]string{"app.ts.test:3000": serveHTTP(t, 200)}}
	c := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:app", Ports: []string{"tcp:3000"}}}}, n, "")
	c.Refresh(t.Context()) // tcp, https (fails), http

	n.dials.Store(0)
	c.Refresh(t.Context())
	if got := n.dials.Load(); got != 2 { // tcp, then the detected http URL
		t.Fatalf("second refresh dialed %d times, want 2", got)
	}
}

// hangingDial blocks until ctx ends, like a peer whose packets are dropped.
func hangingDial(ctx context.Context, _, _ string) (net.Conn, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestUnreachableServiceSharesOneTimeout(t *testing.T) {
	p := NewProber(hangingDial)
	p.timeout = 200 * time.Millisecond
	entries := []Entry{newEntry("app", "ts.test", tsapi.Service{Ports: []string{"tcp:3000"}}, Override{})}

	start := time.Now()
	p.Check(t.Context(), entries)
	// Three attempts (https, http, tcp) must share the budget, not get one each.
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Fatalf("check took %v, want about 200ms", elapsed)
	}
	if entries[0].Status != StatusDown {
		t.Fatalf("status = %s, want down", entries[0].Status)
	}
}

func TestCancelledRefreshPublishesNothing(t *testing.T) {
	lister := &fakeLister{services: []tsapi.Service{{Name: "svc:wiki", Ports: []string{"tcp:443"}}}}
	c := newCatalog(lister, &fakeNet{}, "")
	c.Prober = NewProber(hangingDial)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	c.Refresh(ctx)

	if v := c.View(); !v.Updated.IsZero() || len(v.Groups) != 0 || v.Error != "" {
		t.Fatalf("cancelled refresh published %+v", v)
	}
}

func TestRefreshErrorKeepsLastView(t *testing.T) {
	lister := &fakeLister{services: []tsapi.Service{{Name: "svc:wiki", Ports: []string{"tcp:443"}}}}
	c := newCatalog(lister, &fakeNet{}, "")
	c.Refresh(t.Context())

	lister.err = errors.New("api down")
	c.Refresh(t.Context())

	v := c.View()
	if v.Error != "api down" || len(v.Groups) != 1 {
		t.Fatalf("got error=%q groups=%d, want last view plus error", v.Error, len(v.Groups))
	}
}
