package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn/ipnstate"
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

// listen accepts connections on a local port and hands each to accept.
func listen(t *testing.T, accept func(net.Conn)) string {
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
			accept(c)
		}
	}()
	return ln.Addr().String()
}

func serveTCP(t *testing.T) string {
	t.Helper()
	return listen(t, func(c net.Conn) { c.Close() })
}

// serveSilent reads forever without answering, like DNS over TCP waiting for
// the rest of a length-prefixed query.
func serveSilent(t *testing.T) string {
	t.Helper()
	return listen(t, func(c net.Conn) { go io.Copy(io.Discard, c) })
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
		Prober:        NewProber(n.dial),
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
		"shell.ts.test:22":      serveTCP(t),
		"screen.ts.test:5900":   serveTCP(t),
		"gitea.ts.test:22":      serveTCP(t),
		"gitea.ts.test:3000":    serveHTTP(t, 200),
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
		{Name: "svc:shell", Ports: []string{"tcp:22"}},
		{Name: "svc:screen", Ports: []string{"tcp:5900"}},
		// SSH next to a web port: still probed for HTTP on its first port.
		{Name: "svc:gitea", Ports: []string{"tcp:22", "tcp:3000"}},
	}}
	c := newCatalog(lister, n, "")
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	want := []struct {
		name, section, url string
		status             Status
	}{
		{"jellyfin", "Media", "http://jellyfin.ts.test/", StatusUp},
		// A web Service that does not answer stays a tile, marked down.
		{"offline", "Media", "http://offline.ts.test/", StatusDown},
		{"broken", "Apps", "http://broken.ts.test/", StatusDown},
		// Unusual port that answers HTTP is detected as web; a redirect counts as up.
		{"grafana", "Home Lab", "http://grafana.ts.test:3000/", StatusUp},
		{"postgres", "Other", "", StatusUp},
		{"redis", "Other", "", StatusDown},
		// Any HTTP answer makes a Service web, even a 503 on first discovery.
		{"queue", "Apps", "http://queue.ts.test:15672/", StatusDown},
		// Self-signed certificates count as reachable.
		{"proxmox", "Apps", "https://proxmox.ts.test:8006/", StatusUp},
		// A port range is probed on its first port.
		{"ranged", "Apps", "http://ranged.ts.test:8000/", StatusUp},
		// tcp:* covers 443.
		{"wildcard", "Apps", "https://wildcard.ts.test/", StatusUp},
		// Without a usable port, the default web ports are probed.
		{"console", "Apps", "http://console.ts.test/", StatusUp},
		// SSH and VNC are not web: they land under Other, checked over TCP.
		{"shell", "Other", "", StatusUp},
		{"screen", "Other", "", StatusUp},
		{"gitea", "Other", "", StatusUp},
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
	// What a click copies under Other: a connection string for well-known
	// ports, else the hostname.
	for name, want := range map[string]string{
		"shell":    "ssh shell.ts.test",
		"screen":   "vnc://screen.ts.test",
		"postgres": "postgres://postgres.ts.test:5432/",
		"redis":    "redis://redis.ts.test:6379",
		"gitea":    "gitea.ts.test", // two ports: no single connection string
	} {
		if got := entries[name].Copy; got != want {
			t.Errorf("%s copies %q, want %q", name, got, want)
		}
	}
}

func TestDetectedWebServiceStaysTileWhenDown(t *testing.T) {
	n := &fakeNet{addrs: map[string]string{"grafana.ts.test:3000": serveHTTP(t, 200)}}
	c := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:grafana", Ports: []string{"tcp:3000"}}}}, n, "")
	c.Refresh(t.Context())

	delete(n.addrs, "grafana.ts.test:3000")
	c.Refresh(t.Context())

	entries, sections := index(c.View())
	if sections["grafana"] != "Apps" || entries["grafana"].Status != StatusDown {
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
	if body := strings.TrimSpace(get("/api/list.json").Body.String()); body != "[]" {
		t.Errorf("list before first refresh: %s", body)
	}
	c.Refresh(t.Context())
	if body := get("/api/services.json").Body.String(); !strings.Contains(body, `"name":"bare"`) || strings.Contains(body, `"ports":null`) {
		t.Errorf("JSON ports: %s", body)
	}
	var list []Entry
	if err := json.Unmarshal(get("/api/list.json").Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range list {
		names = append(names, e.Name)
	}
	// Tiles first, then Other, each in page order.
	if got := strings.Join(names, ","); got != "evil,nas,bare,db" {
		t.Errorf("list order: %s", got)
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
	if !strings.Contains(body, `data-copy="postgres://db.ts.test:5432/"`) {
		t.Error("Other row does not copy its connection string")
	}
}

// Phones only offer the page as an app when the manifest and its icons load.
func TestManifestIcons(t *testing.T) {
	h := NewHandler(newCatalog(&fakeLister{}, &fakeNet{}, ""), "start")
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		return rec
	}
	var m struct {
		Name  string
		Icons []struct{ Src string }
	}
	rec := get("/manifest.webmanifest")
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Name != "start" || len(m.Icons) == 0 {
		t.Errorf("manifest: %s", rec.Body)
	}
	srcs := []string{"/icons/apple-touch-icon.png"} // linked from the page for iOS
	for _, i := range m.Icons {
		srcs = append(srcs, i.Src)
	}
	for _, src := range srcs {
		if rec := get(src); rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
			t.Errorf("%s: %d %s", src, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// "Open on another device" gives the page's own full address, built from the
// node name and tailnet; a wrong one would send other devices nowhere.
func TestOtherDeviceAddress(t *testing.T) {
	c := newCatalog(&fakeLister{}, &fakeNet{}, "")
	rec := httptest.NewRecorder()
	NewHandler(c, "home-1").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), `data-copy="https://home-1.ts.test/"`) {
		t.Error("page does not offer its own address for other devices")
	}
}

func TestEmptyPageGuidance(t *testing.T) {
	render := func(c *Catalog) string {
		c.Refresh(t.Context())
		rec := httptest.NewRecorder()
		NewHandler(c, "home").ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		return rec.Body.String()
	}
	const policyHint = "your policy lets this node reach your Services"

	local := newCatalog(&fakeLister{}, &fakeNet{}, "")
	local.Local = true
	notices := []Notice{{Text: "expires <soon>", LinkText: "Open the Machines page", URL: "https://console.tailscale.com/admin/machines"}}
	local.Notices = func(context.Context) []Notice { return notices }
	body := render(local)
	if !strings.Contains(body, "No apps yet") || !strings.Contains(body, policyHint) {
		t.Errorf("local empty page lacks its guidance: %s", body)
	}
	if !strings.Contains(body, "expires &lt;soon&gt;") || !strings.Contains(body, `<a href="https://console.tailscale.com/admin/machines">Open the Machines page</a>`) {
		t.Errorf("notice not rendered escaped with its link: %s", body)
	}

	if body := render(newCatalog(&fakeLister{}, &fakeNet{}, "")); !strings.Contains(body, "No apps yet") || strings.Contains(body, policyHint) {
		t.Errorf("OAuth empty page: want guidance without the local-mode hints: %s", body)
	}
	if body := render(newCatalog(&fakeLister{err: errors.New("api down")}, &fakeNet{}, "")); strings.Contains(body, "No apps yet") {
		t.Error("empty-state guidance shown over a refresh error")
	}
	listed := &fakeLister{services: []tsapi.Service{{Name: "svc:db", Ports: []string{"tcp:5432"}}}}
	if body := render(newCatalog(listed, &fakeNet{}, "")); strings.Contains(body, "No apps yet") {
		t.Error("empty-state guidance shown with Services listed")
	}
}

func TestNoticesClearAfterFix(t *testing.T) {
	expiry := time.Date(2027, 4, 18, 0, 0, 0, 0, time.UTC)
	self := &ipnstate.PeerStatus{DNSName: "home-1.tn.ts.net.", KeyExpiry: &expiry}
	if got := nodeNotices(self, "home"); len(got) != 2 || !strings.Contains(got[0].Text, "http://home-1/") || !strings.Contains(got[1].Text, "2027-04-18") {
		t.Fatalf("renamed, expiring node: got %+v", got)
	}
	if got := nodeNotices(&ipnstate.PeerStatus{DNSName: "home.tn.ts.net."}, "home"); len(got) != 0 {
		t.Errorf("node with its name and no expiry: got %+v", got)
	}

	c := newCatalog(&fakeLister{}, &fakeNet{}, "")
	c.Notices = func(context.Context) []Notice { return nodeNotices(self, "home") }
	c.Refresh(t.Context())
	if n := len(c.View().Notices); n != 2 {
		t.Fatalf("before the fix: %d notices, want 2", n)
	}
	self = &ipnstate.PeerStatus{DNSName: "home-1.tn.ts.net."} // expiry disabled in the admin console
	c.Refresh(t.Context())
	if got := c.View().Notices; len(got) != 1 || strings.Contains(got[0].Text, "expires") {
		t.Errorf("after disabling expiry: got %+v, want only the name notice", got)
	}
}

func TestEnvTags(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  []string
		bad   bool
	}{
		{"", nil, false},
		{" \t ", nil, false},
		{" tag:app ,\t tag:nas, ", []string{"tag:app", "tag:nas"}, false},
		{"app", nil, true},
		{"tag:", nil, true},
		{"tag:app,nas", nil, true},
	} {
		t.Setenv("TAILHOME_DEVICE_TAGS", tt.value)
		got, err := envTags("TAILHOME_DEVICE_TAGS")
		if (err != nil) != tt.bad || !slices.Equal(got, tt.want) {
			t.Errorf("%q: got %v, %v; want %v, error=%v", tt.value, got, err, tt.want, tt.bad)
		}
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
	if e := entries["pinned"]; sections["pinned"] != "Apps" || e.Status != StatusDown {
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

func TestCopyOverride(t *testing.T) {
	path := writeOverrides(t, `
gitea:
  copy: git@gitea.ts.test
db:
  copy: psql -h db.ts.test
`)
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:gitea", Ports: []string{"tcp:443", "tcp:22"}},
		{Name: "svc:db", Ports: []string{"tcp:5432"}},
	}}
	c := newCatalog(lister, &fakeNet{}, path)
	c.Refresh(t.Context())
	v := c.View()

	// A web Service keeps its tile and gains a row under Other.
	if len(v.Groups) != 1 || len(v.Groups[0].Entries) != 1 || v.Groups[0].Entries[0].URL != "https://gitea.ts.test/" {
		t.Fatalf("gitea tile: got %+v", v.Groups)
	}
	copies := map[string]string{}
	for _, e := range v.Other {
		if e.URL != "" {
			t.Errorf("%s row under Other has a URL %q", e.Name, e.URL)
		}
		copies[e.Name] = e.Copy
	}
	// The override replaces the value inferred from the port.
	if copies["gitea"] != "git@gitea.ts.test" || copies["db"] != "psql -h db.ts.test" || len(v.Other) != 2 {
		t.Errorf("rows under Other: got %v", copies)
	}
	// The flat list names each Service once: the copy row repeats the tile.
	if n := len(v.all()); n != 2 {
		t.Errorf("flat list has %d entries, want 2", n)
	}
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
	// The tcp, https and http attempts must share the budget, not get one each.
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

func TestIconMatching(t *testing.T) {
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:immich-server"}, {Name: "svc:homeassistant"}, {Name: "svc:metabase-prod"},
		{Name: "svc:jf-prod"}, {Name: "svc:grafana"}, {Name: "svc:nas"}, {Name: "svc:go-links"},
	}}
	path := writeOverrides(t, "nas:\n  icon: synology\n")
	icons := map[string]string{}
	// The list loads in the background; Loaded refreshes once it is in, as main does.
	catalogIcons := func(fetch func(context.Context) ([]string, error), loads bool) {
		c := newCatalog(lister, &fakeNet{}, path)
		loaded := make(chan struct{})
		c.Icons = &Icons{Fetch: fetch, Loaded: func() { close(loaded) }}
		c.Refresh(t.Context())
		if loads { // failed fetches never call Loaded
			<-loaded
			c.Refresh(t.Context())
		}
		entries, _ := index(c.View())
		for name, e := range entries {
			icons[name] = strings.TrimSuffix(strings.TrimPrefix(e.Icon, iconBase), ".svg")
		}
	}

	catalogIcons(func(context.Context) ([]string, error) {
		return []string{"immich", "home-assistant", "metabase", "grafana", "grafana-alt", "go"}, nil
	}, true)
	for name, want := range map[string]string{
		"immich-server": "immich",         // trailing word dropped
		"homeassistant": "home-assistant", // hyphens ignored
		"metabase-prod": "metabase",
		"grafana":       "grafana",
		"jf-prod":       "",         // no icon: the tile shows its first letter, and nothing is requested
		"nas":           "synology", // an icon override wins
		"go-links":      "",         // too short a first word to mean the Go icon
	} {
		if icons[name] != want {
			t.Errorf("%s: icon %q, want %q", name, icons[name], want)
		}
	}

	// Without the list of icons, names are used as they are.
	catalogIcons(func(context.Context) ([]string, error) { return nil, errors.New("offline") }, false)
	if icons["immich-server"] != "immich-server" || icons["jf-prod"] != "jf-prod" {
		t.Errorf("icons without the list: %v", icons)
	}
}

// A blocked CDN must not hold up discovery or manual refreshes.
func TestStalledIconListDoesNotDelayRefresh(t *testing.T) {
	c := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:immich-server"}}}, &fakeNet{}, "")
	c.Icons = &Icons{Fetch: func(ctx context.Context) ([]string, error) { <-ctx.Done(); return nil, ctx.Err() }}
	start := time.Now()
	c.Refresh(t.Context())
	if elapsed := time.Since(start); elapsed > time.Second || len(c.View().all()) != 1 {
		t.Errorf("refresh took %v and listed %d Services, want it quick and listing 1", elapsed, len(c.View().all()))
	}
}

func TestShortcuts(t *testing.T) {
	path := writeOverrides(t, `
grafana:
  url: https://grafana.ts.test/d/main
evil:
  url: "javascript:alert(1)"
`)
	lister := &fakeLister{services: []tsapi.Service{
		{Name: "svc:jellyfin", Ports: []string{"tcp:443"}},
		{Name: "svc:grafana", Ports: []string{"tcp:443"}},
		{Name: "svc:evil", Ports: []string{"tcp:443"}},
		{Name: "svc:db", Ports: []string{"tcp:5432"}},
	}}
	c := newCatalog(lister, &fakeNet{}, path)
	c.Refresh(t.Context())
	h := NewHandler(c, "home")
	for target, want := range map[string]string{
		"/go/jellyfin":  "https://jellyfin.ts.test/",
		"/go/JellyFin":  "https://jellyfin.ts.test/",      // case-insensitive
		"/go/grafana":   "https://grafana.ts.test/d/main", // the tile's own URL, override included
		"/go/jellyfin/": "https://jellyfin.ts.test/",      // pasted with a trailing slash
		"/go/jel":       "",                               // no prefixes: a bookmark must not break when a similar name appears
		"/go/db":        "",                               // not a tile: nothing to open
		"/go/evil":      "",                               // never redirect where a tile wouldn't link
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		got := rec.Header().Get("Location")
		if want == "" && rec.Code != http.StatusNotFound || want != "" && (rec.Code != http.StatusFound || got != want) {
			t.Errorf("%s: %d %q, want %q", target, rec.Code, got, want)
		}
	}
}
