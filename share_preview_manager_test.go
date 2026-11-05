package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type managedPreviewResult struct {
	fingerprint string
	err         error
}

type previewAdmissionListener struct {
	net.Listener
	accepting chan struct{}
	once      sync.Once
}

func (l *previewAdmissionListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })
	return l.Listener.Accept()
}

func previewTestEntry(raw string) Entry {
	return Entry{Source: "device", Name: "dev:8443", Label: "Preview", Host: "dev.test.ts.net", URL: "https://dev.test.ts.net:8443/", Ports: []string{"tcp:8443"}, nodeID: "stable-dev", Temporary: true, cardRaw: []byte(raw)}
}
func previewTick(t *testing.T, ticks chan time.Time) {
	t.Helper()
	select {
	case ticks <- time.Now():
	case <-time.After(time.Second):
		t.Fatal("preview watcher did not advance")
	}
}
func previewResult(t *testing.T, results chan<- managedPreviewResult, fingerprint string, err error) {
	t.Helper()
	select {
	case results <- managedPreviewResult{fingerprint, err}:
	case <-time.After(time.Second):
		t.Fatal("preview watcher did not fetch")
	}
}
func configurePreviewLifecycle(t *testing.T, manager *shareManager) (chan managedPreviewResult, chan time.Time, *previewAdmissionListener) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	admission := &previewAdmissionListener{Listener: listener, accepting: make(chan struct{})}
	runtime := &readyShareRuntime{blockedShareRuntime: &blockedShareRuntime{started: make(chan struct{}), release: make(chan struct{}), logout: make(chan struct{}, 1)}, listener: admission}
	manager.factory = func(ShareRecord) (shareRuntime, error) { return runtime, nil }
	results := make(chan managedPreviewResult)
	manager.checkPreview = func(ctx context.Context) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case result := <-results:
			return result.fingerprint, result.err
		}
	}
	ticks := make(chan time.Time)
	manager.previewTick = ticks
	return results, ticks, admission
}
func awaitPreviewAdmission(t *testing.T, listener *previewAdmissionListener) {
	t.Helper()
	select {
	case <-listener.accepting:
	case <-time.After(time.Second):
		t.Fatal("verified preview did not become public")
	}
}
func assertPreviewNotAdmitted(t *testing.T, listener *previewAdmissionListener) {
	t.Helper()
	select {
	case <-listener.accepting:
		t.Fatal("unverified preview became public")
	default:
	}
}

func TestPreviewShareDurationsAndFingerprintPersistence(t *testing.T) {
	before := targetForEntry(previewTestEntry(`{"preview":true,"unknown":1}`), "test.ts.net", "svc:home")
	after := targetForEntry(previewTestEntry(`{ "preview":true,"unknown":1}`), "test.ts.net", "svc:home")
	if before.Tile.TargetID != after.Tile.TargetID || before.Tile.Revision == after.Tile.Revision {
		t.Fatal("exact card changes did not revise a stable target")
	}
	withoutCard := previewTestEntry("")
	withoutCard.cardRaw = nil
	noCard := targetForEntry(withoutCard, "test.ts.net", "svc:home")
	if !noCard.eligible || !noCard.Tile.Preview || noCard.Tile.Revision == before.Tile.Revision {
		t.Fatal("no-card preview contract incorrect")
	}
	for _, duration := range []string{"1h", "8h", "24h", "7d", "until-stopped"} {
		t.Run(duration, func(t *testing.T) {
			manager, _, _ := fakeShareManager(t)
			target := targetForEntry(previewTestEntry(`{"preview":true,"version":"A"}`), "test.ts.net", "svc:home")
			manager.targets[target.Tile.TargetID] = target
			err := manager.create(target.Tile.TargetID, target.Tile.Revision, duration, "preview-create", "admin")
			if duration == "7d" || duration == "until-stopped" {
				if shareCode(err) != "invalid_request" {
					t.Fatal("long preview duration accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			record := manager.snapshot(true).Records[0]
			elapsed, _ := shareDuration(duration)
			if record.PreviewFingerprint == "" || record.ExpiresAt == nil || record.ExpiresAt.Sub(record.CreatedAt) != elapsed {
				t.Fatal("preview duration or classification lost")
			}
			store, err := openShareStore(filepath.Dir(manager.store.dir))
			if err != nil {
				t.Fatal(err)
			}
			saved := store.ledger.Records[record.ID]
			if saved.PreviewFingerprint != target.Tile.Revision {
				t.Fatal("approved preview fingerprint was not persisted")
			}
			encoded, err := json.Marshal(record)
			if err != nil || strings.Contains(string(encoded), "previewFingerprint") {
				t.Fatal("private fingerprint leaked into status")
			}
		})
	}
}

func TestPreviewLifecycleChecks(t *testing.T) {
	for _, mode := range []string{"changed", "appeared", "removed", "gone", "no-card gone", "reset failures", "initial failures", "initial gone"} {
		t.Run(mode, func(t *testing.T) {
			manager, _, _ := fakeShareManager(t)
			results, ticks, listener := configurePreviewLifecycle(t, manager)
			entry := previewTestEntry(`{"preview":true}`)
			if mode == "appeared" || mode == "no-card gone" {
				entry.cardRaw = nil
			}
			target := targetForEntry(entry, "test.ts.net", "svc:home")
			manager.targets[target.Tile.TargetID] = target
			if err := manager.create(target.Tile.TargetID, target.Tile.Revision, "1h", "watched-preview", "admin"); err != nil {
				t.Fatal(err)
			}
			record := manager.snapshot(true).Records[0]
			failure := errors.New("unreachable")
			if mode == "initial failures" || mode == "initial gone" {
				for range 2 {
					previewResult(t, results, "", failure)
					previewTick(t, ticks)
					assertPreviewNotAdmitted(t, listener)
					if manager.snapshot(true).Records[0].State != "starting" {
						t.Fatal("one or two failures stopped preview")
					}
				}
				if mode == "initial gone" {
					previewResult(t, results, "", failure)
					manager.workers.Wait()
					assertPreviewNotAdmitted(t, listener)
					if manager.snapshot(true).Records[0].Reason != "preview_unreachable" {
						t.Fatal("third initial failure did not stop preview")
					}
					return
				}
			}
			previewResult(t, results, target.Tile.Revision, nil)
			awaitPreviewAdmission(t, listener)
			if mode == "initial failures" {
				if err := manager.stop(record.ID, record.Revision, "admin", "admin_stop"); err != nil {
					t.Fatal(err)
				}
				manager.workers.Wait()
				return
			}
			if mode == "changed" || mode == "appeared" || mode == "removed" {
				previewTick(t, ticks)
				raw := []byte(`{"preview":true,"version":"B"}`)
				if mode == "removed" {
					raw = nil
				}
				previewResult(t, results, previewFingerprint(target.Binding, raw), nil)
			} else {
				previewTick(t, ticks)
				if mode == "reset failures" {
					for range 2 {
						previewResult(t, results, "", failure)
						previewTick(t, ticks)
					}
					previewResult(t, results, target.Tile.Revision, nil)
					previewTick(t, ticks)
				}
				for range 2 {
					previewResult(t, results, "", failure)
					previewTick(t, ticks)
					// A catalog omission must not bypass the three-check threshold.
					manager.catalogUpdated(View{Updated: time.Now()}, "test.ts.net", "svc:home")
					if !manager.snapshot(true).Records[0].Public {
						t.Fatal("one or two failures revoked preview")
					}
				}
				previewResult(t, results, "", failure)
			}
			manager.workers.Wait()
			stopped := manager.snapshot(true).Records[0]
			want := "preview_unreachable"
			if mode == "changed" || mode == "appeared" {
				want = "preview_changed"
			} else if mode == "removed" {
				want = "preview_gone"
			}
			if stopped.State != "stopped" || stopped.Reason != want || !stopped.CanDismiss || stopped.Public {
				t.Fatalf("preview terminal state: %+v", stopped)
			}
			if err := manager.dismiss(stopped.ID, stopped.Revision); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPreviewRestartChangedCardNeverAdmits(t *testing.T) {
	root := t.TempDir()
	store, err := openShareStore(root)
	if err != nil {
		t.Fatal(err)
	}
	target := targetForEntry(previewTestEntry(`{"preview":true,"version":"A"}`), "test.ts.net", "svc:home")
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	id := randomShareID()
	record := &ShareRecord{ID: id, Hostname: "public-" + id, TargetID: target.Tile.TargetID, Binding: target.Binding, PreviewFingerprint: target.Tile.Revision, State: "public", CreatedAt: now, NextReviewAt: now.Add(30 * 24 * time.Hour), ExpiresAt: &expiry}
	if err := store.Save(map[string]*ShareRecord{id: record}); err != nil {
		t.Fatal(err)
	}
	manager, err := newShareManager(root, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Close)
	results, _, listener := configurePreviewLifecycle(t, manager)
	manager.catalogUpdated(View{Updated: now}, "test.ts.net", "svc:home")
	previewResult(t, results, previewFingerprint(target.Binding, []byte(`{"preview":true,"version":"B"}`)), nil)
	manager.workers.Wait()
	assertPreviewNotAdmitted(t, listener)
	stopped := manager.snapshot(true).Records[0]
	if stopped.Public || stopped.State != "stopped" || stopped.Reason != "preview_changed" || !stopped.ExpiresAt.Equal(expiry) || !stopped.CanDismiss {
		t.Fatalf("changed preview resumed: %+v", stopped)
	}
}

func TestPreviewCreateRefreshesCardBeforeApproval(t *testing.T) {
	manager, runtime, _ := fakeShareManager(t)
	device := catalogDevice("dev", "dev", "100.64.0.1")
	catalog, _, network, ca := deviceCatalog(t, device)
	catalog.PreviewPorts = []uint16{8443}
	server := serveCard(t, network, ca, device, 8443, `{"preview":true,"version":"A"}`, 200, 200)
	catalog.OnRefresh = func(view View) {
		manager.catalogUpdated(view, catalog.DNSSuffix, catalog.Self)
	}
	manager.refreshCards = func() {
		catalog.RefreshManualIfOlder(t.Context(), 0)
	}
	catalog.Refresh(t.Context())
	before := manager.snapshot(true).Targets
	if len(before) != 1 || !before[0].Preview {
		t.Fatal("preview was not discovered")
	}
	approved := before[0]
	server.set(`{"preview":true,"version":"B"}`)
	err := manager.create(approved.TargetID, approved.Revision, "1h", "old-card", "admin")
	status, code, _ := shareHTTPError(err)
	if status != http.StatusConflict || code != "revision" {
		t.Fatal("changed card did not reject stale approval", err)
	}
	select {
	case <-runtime.started:
		t.Fatal("stale approval provisioned a node")
	default:
	}
	page := manager.snapshot(true)
	if len(page.Records) != 0 || len(page.Targets) != 1 || page.Targets[0].Revision == approved.Revision {
		t.Fatal("next confirmation did not receive the refreshed card")
	}
	current := page.Targets[0]
	if err := manager.create(current.TargetID, current.Revision, "1h", "new-card", "admin"); err != nil {
		t.Fatal("fresh approval could not recover without manual refresh", err)
	}
	record := manager.snapshot(true).Records[0]
	if record.PreviewFingerprint != current.Revision {
		t.Fatal("fresh approval persisted the wrong fingerprint")
	}
}

func TestPreviewCreateRejectsBeforeCardRefresh(t *testing.T) {
	for _, test := range []struct{ name, code string }{
		{"already shared", "revision"},
		{"capacity", "capacity"},
		{"unavailable", "oauth_missing"},
		{"closing", "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager, _, _ := fakeShareManager(t)
			target := targetForEntry(previewTestEntry(`{"preview":true}`), "test.ts.net", "svc:home")
			manager.targets[target.Tile.TargetID] = target
			switch test.name {
			case "already shared":
				id := randomShareID()
				manager.records[id] = &ShareRecord{ID: id, TargetID: target.Tile.TargetID, State: "public"}
			case "capacity":
				for range 8 {
					id := randomShareID()
					manager.records[id] = &ShareRecord{ID: id, State: "public"}
				}
			case "unavailable":
				manager.unavailable = shareOAuthMessage
			case "closing":
				manager.Close()
			}
			manager.refreshCards = func() { t.Error("rejected create refreshed the catalog") }
			if err := manager.create(target.Tile.TargetID, target.Tile.Revision, "1h", "rejected-create", "admin"); shareCode(err) != test.code {
				t.Fatal("wrong create rejection", err)
			}
		})
	}
}
