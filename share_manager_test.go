package main

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type controlledShareRuntime struct {
	*blockedShareRuntime
	prepareRelease, cleanupRelease <-chan struct{}
	logoutErr, closeErr            error
	logouts, closes                atomic.Int32
}

func (r *controlledShareRuntime) Prepare(ctx context.Context, report func()) (string, net.Listener, func(context.Context) ([]netip.Addr, error), error) {
	if r.prepareRelease == nil {
		return r.blockedShareRuntime.Prepare(ctx, report)
	}
	r.once.Do(func() { close(r.started) })
	<-ctx.Done()
	<-r.prepareRelease
	return "", nil, nil, ctx.Err()
}
func (r *controlledShareRuntime) Logout(ctx context.Context) error {
	r.logouts.Add(1)
	if r.cleanupRelease != nil {
		select {
		case <-r.cleanupRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if r.logoutErr != nil {
		return r.logoutErr
	}
	return r.blockedShareRuntime.Logout(ctx)
}
func (r *controlledShareRuntime) Close() error { r.closes.Add(1); return r.closeErr }
func TestShareShutdownAndCleanup(t *testing.T) {
	for _, test := range []struct{ name, trigger, failure string }{{"keep live key", "shutdown", ""}, {"admin stop", "stop", ""}, {"expiry at shutdown", "expired", ""}, {"logout failure", "stop", "logout"}, {"close failure", "stop", "close"}, {"directory failure", "stop", "remove"}, {"shutdown directory failure", "expired", "remove"}} {
		t.Run(test.name, func(t *testing.T) {
			m, first, target := fakeShareManager(t)
			runtime := &controlledShareRuntime{blockedShareRuntime: first}
			switch test.failure {
			case "logout":
				runtime.logoutErr = shareErr("cleanup_pending")
			case "close":
				runtime.closeErr = shareErr("cleanup_pending")
			case "remove":
				m.removeNodeDir = func(string) error { return errors.New("cannot remove") }
			}
			m.factory = func(ShareRecord) (shareRuntime, error) { return runtime, nil }
			if err := m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "operation", "admin"); err != nil {
				t.Fatal(err)
			}
			<-first.started
			record := m.snapshot(true).Records[0]
			dir := filepath.Join(m.store.dir, "nodes", record.ID)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			key := []byte(`{"_current-profile":"retained-key"}`)
			path := filepath.Join(dir, "tailscaled.state")
			if err := os.WriteFile(path, key, 0600); err != nil {
				t.Fatal(err)
			}
			if test.trigger == "stop" {
				if err := m.stop(record.ID, record.Revision, "admin", "admin_stop"); err != nil {
					t.Fatal(err)
				}
				m.workers.Wait()
			} else {
				if test.trigger == "expired" {
					m.now = func() time.Time { return record.ExpiresAt.Add(time.Second) }
				}
				m.Close()
			}
			if test.trigger == "shutdown" {
				if runtime.logouts.Load() != 0 || runtime.closes.Load() != 0 {
					t.Fatal("live shutdown discarded identity")
				}
				saved, err := os.ReadFile(path)
				if err != nil || string(saved) != string(key) {
					t.Fatal("shutdown changed node key")
				}
				if !m.snapshot(true).Records[0].live() {
					t.Fatal("cancellation revoked consent")
				}
				return
			}
			if runtime.logouts.Load() != 1 || runtime.closes.Load() != 1 {
				t.Fatal("terminal cleanup not performed exactly once")
			}
			if test.failure == "" {
				assertShareRetired(t, m, record.ID)
			} else {
				records := m.snapshot(true).Records
				if len(records) != 1 || records[0].State != "cleanup" || !records[0].CleanupPending || records[0].CanDismiss {
					t.Fatalf("cleanup failure hidden: %+v", records)
				}
			}
		})
	}
}
func TestShareStopWhileProvisionBlocked(t *testing.T) {
	m, first, target := fakeShareManager(t)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	runtime := &controlledShareRuntime{blockedShareRuntime: first, prepareRelease: release}
	m.factory = func(ShareRecord) (shareRuntime, error) { return runtime, nil }
	if err := m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "operation", "admin"); err != nil {
		t.Fatal(err)
	}
	<-first.started
	record := m.snapshot(true).Records[0]
	if err := m.stop(record.ID, record.Revision, "admin", "admin_stop"); err != nil {
		t.Fatal(err)
	}
	pending := m.snapshot(true).Records[0]
	if pending.live() || !pending.CleanupPending || pending.State == "cleanup" {
		t.Fatal("blocked Stop is not visible as normal shutdown")
	}
	if runtime.logouts.Load() != 0 {
		t.Fatal("cleanup raced active provisioning")
	}
	close(release)
	m.workers.Wait()
	assertShareRetired(t, m, record.ID)
}
func TestShareCleanupReopensClosedRuntime(t *testing.T) {
	m, first, target := fakeShareManager(t)
	var calls atomic.Int32
	m.factory = func(ShareRecord) (shareRuntime, error) {
		runtime := &controlledShareRuntime{blockedShareRuntime: first}
		if calls.Add(1) == 1 {
			runtime.logoutErr = shareErr("cleanup_pending")
		}
		return runtime, nil
	}
	if err := m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "operation", "admin"); err != nil {
		t.Fatal(err)
	}
	<-first.started
	record := m.snapshot(true).Records[0]
	if err := m.stop(record.ID, record.Revision, "admin", "admin_stop"); err != nil {
		t.Fatal(err)
	}
	m.workers.Wait()
	if !m.snapshot(true).Records[0].CleanupPending {
		t.Fatal("lost unknown deletion evidence")
	}
	m.mu.Lock()
	m.active[record.ID].timer.Stop()
	m.resumeLocked()
	m.mu.Unlock()
	m.workers.Wait()
	if calls.Load() != 1 {
		t.Fatal("catalog refresh bypassed cleanup backoff")
	}
	m.retryCleanup(record.ID)
	m.workers.Wait()
	if calls.Load() != 2 {
		t.Fatal("cleanup reused a closed backend")
	}
	assertShareRetired(t, m, record.ID)
}
func TestShareTerminalReasonsAndStorageRecovery(t *testing.T) {
	for _, reason := range []string{"admin_stop", "expired", "certificate_rate_limited", "sharing_disabled"} {
		t.Run(reason, func(t *testing.T) {
			m, _, target := fakeShareManager(t)
			now := time.Now().UTC()
			id := randomShareID()
			r := &ShareRecord{ID: id, Hostname: "public-" + id, TargetID: target.Tile.TargetID, Revision: "revision", State: "cleanup", Reason: reason, CleanupPending: true, CreatedAt: now, NextReviewAt: now.Add(time.Hour), StoppedAt: &now, StoppedBy: "original"}
			m.records[id] = r
			if err := m.stop(id, "revision", "retrying", "admin_stop"); err != nil {
				t.Fatal(err)
			}
			m.workers.Wait()
			if r.Reason != reason || r.StoppedBy != "original" || r.Revision != "revision" {
				t.Fatal("cleanup rewrote original consent")
			}
			if reason == "admin_stop" || reason == "expired" || reason == "sharing_disabled" {
				assertShareRetired(t, m, id)
			} else {
				if r.State != terminalShareState(reason) || !m.snapshot(true).Records[0].CanDismiss {
					t.Fatal("failure was discarded or misclassified")
				}
				if err := m.dismiss(id, "revision"); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	t.Run("storage repair allows retirement", func(t *testing.T) {
		m, _, _ := fakeShareManager(t)
		id := randomShareID()
		r := &ShareRecord{ID: id, Hostname: "public-" + id, Revision: "revision", State: "stopped", Reason: "admin_stop", CreatedAt: time.Now(), NextReviewAt: time.Now().Add(time.Hour)}
		m.records[id] = r
		write := m.store.write
		m.store.write = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
		if err := m.stop(id, "revision", "admin", "admin_stop"); shareCode(err) != "storage" {
			t.Fatal("failed save lost its storage code")
		}
		if len(m.snapshot(true).Records) != 1 || m.snapshot(true).Error == "" {
			t.Fatal("failed retirement disappeared")
		}
		m.store.write = write
		if err := m.stop(id, "revision", "admin", "admin_stop"); err != nil {
			t.Fatal(err)
		}
		if m.snapshot(true).Error != "" {
			t.Fatal("storage warning remained after successful full-ledger save")
		}
		assertShareRetired(t, m, id)
	})
}
func TestShareOperationReplayAndClosedMutations(t *testing.T) {
	m, first, target := fakeShareManager(t)
	for range 2 {
		if err := m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "operation", "admin"); err != nil {
			t.Fatal(err)
		}
	}
	<-first.started
	if len(m.snapshot(true).Records) != 1 {
		t.Fatal("operation replay duplicated a live share")
	}
	record := m.snapshot(true).Records[0]
	m.Close()
	if shareCode(m.stop(record.ID, record.Revision, "admin", "admin_stop")) != "unavailable" || shareCode(m.review(record.ID, record.Revision, "admin")) != "unavailable" || shareCode(m.dismiss(record.ID, record.Revision)) != "unavailable" {
		t.Fatal("mutation after lock release was allowed")
	}
}

type readyShareRuntime struct {
	*blockedShareRuntime
	listener net.Listener
}

func (r *readyShareRuntime) Prepare(context.Context, func()) (string, net.Listener, func(context.Context) ([]netip.Addr, error), error) {
	return "public-example.test.ts.net", r.listener, func(context.Context) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("100.64.0.1")}, nil
	}, nil
}
func (r *readyShareRuntime) Close() error { _ = r.listener.Close(); return nil }
func TestSharePublicSaveFailureIsFailedNotStopped(t *testing.T) {
	m, first, target := fakeShareManager(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	runtime := &readyShareRuntime{blockedShareRuntime: first, listener: listener}
	m.factory = func(ShareRecord) (shareRuntime, error) { return runtime, nil }
	write := m.store.write
	calls := 0
	m.store.write = func(path string, data []byte, mode os.FileMode) error {
		calls++
		if calls > 1 {
			return &os.PathError{Op: "write", Path: path, Err: errors.New("disk full")}
		}
		return write(path, data, mode)
	}
	if err = m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "operation", "admin"); err != nil {
		t.Fatal(err)
	}
	m.workers.Wait()
	records := m.snapshot(true).Records
	if len(records) != 1 || records[0].State != "failed" || records[0].Reason != "state_write_failed" || records[0].Public {
		t.Fatalf("failed publication misclassified: %+v", records)
	}
}
