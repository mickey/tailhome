package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/tailcfg"
)

type blockedShareRuntime struct {
	started, release chan struct{}
	once             sync.Once
	logout           chan struct{}
}

func (f *blockedShareRuntime) Prepare(ctx context.Context, _ func()) (string, net.Listener, func(context.Context) ([]netip.Addr, error), error) {
	f.once.Do(func() { close(f.started) })
	select {
	case <-ctx.Done():
		return "", nil, nil, ctx.Err()
	case <-f.release:
		return "", nil, nil, shareErr("setup_failed")
	}
}
func (f *blockedShareRuntime) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, shareErr("unexpected_dial")
}
func (f *blockedShareRuntime) Logout(context.Context) error {
	select {
	case f.logout <- struct{}{}:
	default:
	}
	return nil
}
func (f *blockedShareRuntime) Close() error { return nil }
func fakeShareManager(t *testing.T) (*shareManager, *blockedShareRuntime, shareTarget) {
	t.Helper()
	runtime := &blockedShareRuntime{started: make(chan struct{}), release: make(chan struct{}), logout: make(chan struct{}, 8)}
	manager, err := newShareManager(t.TempDir(), true, func(ShareRecord) (shareRuntime, error) { return runtime, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	entry := Entry{Name: "app", Source: "service", Host: "app.test.ts.net", URL: "https://app.test.ts.net/", Label: "App", Ports: []string{"tcp:443"}}
	target := targetForEntry(entry, "test.ts.net", "svc:home")
	manager.targets[target.Tile.TargetID] = target
	return manager, runtime, target
}
func assertShareRetired(t *testing.T, m *shareManager, id string) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.records[id] != nil {
		t.Fatalf("completed share still visible: %+v", m.records[id])
	}
}
func TestShareConfig(t *testing.T) {
	for _, test := range []struct {
		admins string
		count  int
		valid  bool
	}{
		{"", 0, true},
		{" \t ", 0, true},
		{"a@example.com", 1, true},
		{"a@example.com, a@example.com,b@example.com", 2, true},
		{"a,", 0, false},
		{",a", 0, false},
		{"*", 0, false},
		{"a?@example.com", 0, false},
	} {
		cfg, err := parseShareConfig(test.admins)
		if (err == nil) != test.valid {
			t.Fatalf("%q: %v", test.admins, err)
		}
		if test.valid && (len(cfg.Admins) != test.count || cfg.Enabled != (test.count != 0)) {
			t.Errorf("%q: enabled=%t admins=%v", test.admins, cfg.Enabled, cfg.Admins)
		}
	}
}
func TestShareStopRacingProvisionAndDiskFull(t *testing.T) {
	m, runtime, target := fakeShareManager(t)
	if err := m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "operation", "admin"); err != nil {
		t.Fatal(err)
	}
	<-runtime.started
	record := m.snapshot(true).Records[0]
	m.mu.Lock()
	m.store.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
	m.mu.Unlock()
	if err := m.stop(record.ID, record.Revision, "admin", "admin_stop"); shareCode(err) != "stop_storage" {
		t.Fatalf("stop=%v", err)
	}
	m.workers.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.records[record.ID]; r == nil || r.live() {
		t.Fatal("disk-full stop lost its off intent or warning")
	}
	if m.err == "" {
		t.Fatal("missing storage warning")
	}
}
func TestSharingOffCleansCachedNodes(t *testing.T) {
	t.Run("default routes and state", func(t *testing.T) {
		root := t.TempDir()
		manager, err := newShareManager(root, false, nil)
		if manager != nil || err != nil {
			t.Fatal(manager, err)
		}
		if _, err = os.Stat(filepath.Join(root, "shares")); !os.IsNotExist(err) {
			t.Fatal("default off created state")
		}
		response := httptest.NewRecorder()
		newHandler(&Catalog{DNSSuffix: "test.ts.net"}, "home", nil).ServeHTTP(response, httptest.NewRequest("GET", "https://home.test.ts.net/share/status", nil))
		if response.Code != 404 {
			t.Fatal("sharing-off route exists")
		}
	})
	for _, state := range []string{"public", "cleanup"} {
		for _, retry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retry=%t", state, retry), func(t *testing.T) {
				root := t.TempDir()
				store, err := openShareStore(root)
				if err != nil {
					t.Fatal(err)
				}
				now := time.Now().UTC()
				expiry := now.Add(time.Hour)
				id := randomShareID()
				record := &ShareRecord{ID: id, Hostname: "public-" + id, State: state, CreatedAt: now, NextReviewAt: now.Add(30 * 24 * time.Hour), ExpiresAt: &expiry, CleanupPending: state == "cleanup"}
				if err := store.Save(map[string]*ShareRecord{id: record}); err != nil {
					t.Fatal(err)
				}
				nodeDir := filepath.Join(store.dir, "nodes", id)
				if err := os.MkdirAll(nodeDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(nodeDir, "tailscaled.state"), []byte("cached identity"), 0600); err != nil {
					t.Fatal(err)
				}
				var runtimes []*controlledShareRuntime
				manager, err := newShareManager(root, false, func(saved ShareRecord) (shareRuntime, error) {
					if saved.live() || saved.Reason != "sharing_disabled" || !saved.ExpiresAt.Equal(expiry) {
						t.Error("cleanup did not use stopped saved consent")
					}
					runtime := &controlledShareRuntime{blockedShareRuntime: &blockedShareRuntime{started: make(chan struct{}), release: make(chan struct{}), logout: make(chan struct{}, 1)}}
					if retry && len(runtimes) == 0 {
						runtime.logoutErr = errors.New("temporarily offline")
					}
					runtimes = append(runtimes, runtime)
					return runtime, nil
				})
				if err != nil || manager == nil {
					t.Fatal("missing cleanup-only manager", err)
				}
				t.Cleanup(manager.Close)
				manager.workers.Wait()
				if retry {
					view := manager.snapshot(true)
					if len(view.Records) != 1 || view.Records[0].State != "cleanup" || !view.Records[0].CleanupPending {
						t.Fatal("failed off-mode cleanup was not retained for retry")
					}
					if _, err := os.Stat(nodeDir); err != nil {
						t.Fatal("removed node state before logout succeeded")
					}
					manager.retryCleanup(id)
					manager.workers.Wait()
				}
				wantRuntimes := 1
				if retry {
					wantRuntimes++
				}
				if len(runtimes) != wantRuntimes {
					t.Fatal("cached node cleanup was skipped")
				}
				for _, runtime := range runtimes {
					select {
					case <-runtime.started:
						t.Fatal("off-mode cleanup provisioned a share")
					default:
					}
					if runtime.logouts.Load() != 1 || runtime.closes.Load() != 1 {
						t.Fatal("cleanup did not log out and close its cached node")
					}
				}
				assertShareRetired(t, manager, id)
				if _, err := os.Stat(nodeDir); !os.IsNotExist(err) {
					t.Fatal("cleaned node state remains")
				}
				loaded, err := openShareStore(root)
				if err != nil || len(loaded.ledger.Records) != 0 {
					t.Fatal("off-mode cleanup left a record to dismiss", err)
				}
				inert, err := newShareManager(root, false, func(ShareRecord) (shareRuntime, error) {
					t.Error("clean off mode created a runtime")
					return nil, errors.New("unexpected runtime")
				})
				if inert != nil || err != nil {
					t.Fatal("clean off mode was not inert", err)
				}
			})
		}
	}
}
func TestShareReviewDoesNotExtendExpiry(t *testing.T) {
	m, _, _ := fakeShareManager(t)
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	m.now = func() time.Time { return now }
	id := randomShareID()
	r := &ShareRecord{ID: id, Hostname: "public-" + id, Revision: "r", State: "public", ExpiresAt: &expiry, CreatedAt: now.Add(-31 * 24 * time.Hour), NextReviewAt: now.Add(-time.Hour)}
	m.records[id] = r
	if !m.snapshot(true).Records[0].ReviewDue {
		t.Fatal("missing review")
	}
	if err := m.review(id, "r", "admin"); err != nil {
		t.Fatal(err)
	}
	if !r.ExpiresAt.Equal(expiry) || !r.NextReviewAt.Equal(now.Add(30*24*time.Hour)) || r.LastReviewedBy != "admin" {
		t.Fatalf("review=%+v", r)
	}
	data, err := os.ReadFile(filepath.Join(m.store.dir, "shares.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger shareLedger
	if json.Unmarshal(data, &ledger) != nil || ledger.Shares[0].LastReviewedBy != "admin" {
		t.Fatal("review was not durable")
	}
}
func TestShareLedgerVersionAndIDs(t *testing.T) {
	root := t.TempDir()
	_, err := openShareStore(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "shares", "shares.json")
	for _, data := range []string{`{"version":999}`, `{"version":1,"shares":[{"id":"../..","hostname":"public-../.."}]}`} {
		if err = os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = openShareStore(root); err == nil {
			t.Fatal("invalid state accepted")
		}
		saved, _ := os.ReadFile(path)
		if string(saved) != data {
			t.Fatal("invalid state overwritten")
		}
	}
}

type fakeShareWho struct {
	response *apitype.WhoIsResponse
	calls    int
	remote   string
}

func (f *fakeShareWho) WhoIs(_ context.Context, remote string) (*apitype.WhoIsResponse, error) {
	f.calls++
	f.remote = remote
	return f.response, nil
}
func TestShareAdminBoundary(t *testing.T) {
	for _, service := range []bool{false, true} {
		for _, mode := range []string{"allowed", "tagged", "not allowlisted", "missing origin", "bad revision"} {
			t.Run(fmt.Sprintf("service=%t/%s", service, mode), func(t *testing.T) {
				m, _, target := fakeShareManager(t)
				login := "admin@example.com"
				tags := []string(nil)
				if mode == "not allowlisted" {
					login = "other@example.com"
				}
				if mode == "tagged" {
					tags = []string{"tag:app"}
				}
				who := &fakeShareWho{response: &apitype.WhoIsResponse{Node: &tailcfg.Node{Tags: tags}, UserProfile: &tailcfg.UserProfile{LoginName: login}}}
				web := &shareWeb{manager: m, who: who, admins: map[string]bool{"admin@example.com": true}, host: "home.test.ts.net"}
				if service {
					web.service = "svc:home"
				}
				mux := http.NewServeMux()
				web.mount(mux)
				revision := target.Tile.Revision
				if mode == "bad revision" {
					revision = "stale"
				}
				body, _ := json.Marshal(map[string]string{"targetID": target.Tile.TargetID, "revision": revision, "duration": "24h", "operationID": "admin-operation"})
				request := httptest.NewRequest("POST", "https://home.test.ts.net/share/create", strings.NewReader(string(body)))
				request.Header.Set("Origin", "https://home.test.ts.net")
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("X-Tailhome-Share-Request", "1")
				request.Header.Set("Tailscale-User-Login", "forged@example.com")
				if mode == "missing origin" {
					request.Header.Del("Origin")
				}
				if service {
					// Mirror the verified tsnet HTTP proxy: client identity is always overwritten.
					request.RemoteAddr = "127.0.0.1:1234"
					request.Header.Del("Tailscale-User-Login")
					if len(tags) == 0 {
						request.Header.Set("Tailscale-User-Login", login)
					}
				}
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				expected := 403
				if mode == "allowed" {
					expected = 200
				} else if mode == "bad revision" {
					expected = 409
				}
				if response.Code != expected {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if mode == "allowed" {
					record := m.snapshot(true).Records[0]
					if record.CreatedBy != "admin@example.com" {
						t.Fatal("wrong recorded owner")
					}
				}
				if service && who.calls != 0 {
					t.Fatal("Service mode repeated WhoIs")
				}
				if !service && mode != "missing origin" && who.remote != request.RemoteAddr {
					t.Fatal("WhoIs source rewritten")
				}
			})
		}
	}
}
func TestShareEligibility(t *testing.T) {
	base := Entry{Name: "app", Source: "service", Host: "app.test.ts.net", URL: "https://app.test.ts.net/", Ports: []string{"tcp:443"}}
	for _, change := range []func(*Entry){func(e *Entry) { e.Temporary = true }, func(e *Entry) { e.URL = "https://external.example/" }, func(e *Entry) { e.URL = "http://127.0.0.1/" }, func(e *Entry) { e.Unverified = true }, func(e *Entry) { e.URL = "https://app.test.ts.net:444/" }} {
		entry := base
		change(&entry)
		if targetForEntry(entry, "test.ts.net", "svc:home").eligible {
			t.Fatalf("ineligible endpoint accepted: %+v", entry)
		}
	}
}
