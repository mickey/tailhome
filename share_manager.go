package main

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

func (active *shareInstance) halt() {
	active.cancel()
	if active.timer != nil {
		active.timer.Stop()
	}
	if active.gateway != nil {
		active.gateway.Close()
	}
	if active.listener != nil {
		go active.listener.Close()
		active.listener = nil
	}
}

func shareDuration(value string) (time.Duration, bool) {
	switch value {
	case "1h":
		return time.Hour, true
	case "8h":
		return 8 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "until-stopped":
		return 0, true
	}
	return 0, false
}
func (m *shareManager) create(target, revision, duration, operation, by string) error {
	elapsed, ok := shareDuration(duration)
	if !ok || operation == "" || len(operation) > 128 {
		return shareErr("invalid_request")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	refreshed := false
	for {
		if m.closing {
			return shareErr("unavailable")
		}
		if m.unavailable != "" {
			return shareErr("oauth_missing")
		}
		var previous *ShareRecord
		count := 0
		for _, record := range m.records {
			if record.OperationID == operation {
				return nil
			}
			if record.live() || record.CleanupPending {
				count++
			}
			if record.TargetID == target {
				if record.live() || record.CleanupPending {
					return shareErr("revision")
				}
				previous = record
			}
		}
		approved, ok := m.targets[target]
		if !ok || approved.Tile.Revision != revision {
			return shareErr("revision")
		}
		if approved.Tile.Preview && (elapsed == 0 || elapsed > 24*time.Hour) {
			return shareErr("invalid_request")
		}
		if approved.Tile.Reason != "" {
			return &shareError{code: "ineligible", detail: approved.Tile.Reason}
		}
		if count >= 8 {
			return shareErr("capacity")
		}
		if approved.Tile.Preview && m.refreshCards != nil && !refreshed {
			refresh := m.refreshCards
			m.mu.Unlock()
			refresh()
			m.mu.Lock()
			refreshed = true
			continue
		}
		now := m.now().UTC()
		id := randomShareID()
		for m.records[id] != nil {
			id = randomShareID()
		}
		record := &ShareRecord{ID: id, Revision: randomShareID(), OperationID: operation, TargetID: target, Label: approved.Tile.Label, Hostname: "public-" + id, Binding: approved.Binding, State: "starting", CreatedAt: now, CreatedBy: by, LastReviewedAt: now, LastReviewedBy: by, NextReviewAt: now.Add(30 * 24 * time.Hour)}
		if approved.Tile.Preview {
			record.PreviewFingerprint = approved.Tile.Revision
		}
		if elapsed != 0 {
			deadline := now.Add(elapsed)
			record.ExpiresAt = &deadline
		}
		if previous != nil {
			delete(m.records, previous.ID)
		}
		m.records[id] = record
		if err := m.saveLocked(); err != nil {
			delete(m.records, id)
			if previous != nil {
				m.records[previous.ID] = previous
			}
			shareLog(id, "failure", err)
			return err
		}
		shareLog(id, "start", nil)
		m.launchLocked(record)
		return nil
	}
}
func (m *shareManager) launchLocked(record *ShareRecord) {
	ctx, cancel := context.WithCancel(context.Background())
	active := &shareInstance{cancel: cancel, done: make(chan struct{})}
	m.active[record.ID] = active
	if record.ExpiresAt != nil {
		schedule := m.afterFunc
		if schedule == nil {
			schedule = time.AfterFunc
		}
		id := record.ID
		active.timer = schedule(record.ExpiresAt.Sub(m.now()), func() { _ = m.stop(id, "", "", "expired") })
	}
	m.workers.Add(1)
	go func(copy ShareRecord) {
		defer m.workers.Done()
		defer close(active.done)
		m.provision(ctx, copy, active)
	}(*record)
}
func (m *shareManager) provision(ctx context.Context, record ShareRecord, active *shareInstance) {
	m.mu.Lock()
	current := m.records[record.ID]
	if m.closing || current == nil || !current.live() {
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	runtime, err := m.factory(record)
	if err != nil {
		m.setupFailed(record.ID, active, err)
		return
	}
	m.mu.Lock()
	active.runtime = runtime
	stopped := m.closing || !m.records[record.ID].live()
	m.mu.Unlock()
	if stopped {
		return
	}
	host, listener, resolve, err := runtime.Prepare(ctx, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if r := m.records[record.ID]; r != nil && r.live() && !m.closing {
			setSharePhase(r, "certificate", m.now())
		}
	})
	if err != nil {
		m.setupFailed(record.ID, active, err)
		return
	}
	m.mu.Lock()
	active.listener = listener
	if m.closing || !m.records[record.ID].live() {
		active.halt()
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	if record.PreviewFingerprint != "" && !m.startPreviewWatch(ctx, record, runtime, resolve) {
		return
	}
	gateway, err := newShareGateway(record.Binding, host, runtime.Dial, resolve, record.ExpiresAt)
	if err != nil {
		m.setupFailed(record.ID, active, err)
		return
	}
	m.mu.Lock()
	active.gateway = gateway
	current = m.records[record.ID]
	if m.closing || !current.live() {
		active.halt()
		m.mu.Unlock()
		return
	}
	current.State = "public"
	current.URL = "https://" + host + "/"
	setSharePhase(current, "public", m.now())
	if err = m.saveLocked(); err != nil {
		m.mu.Unlock()
		m.setupFailed(record.ID, active, &shareError{code: "state_write_failed", cause: err})
		return
	}
	gateway.Open()
	shareLog(record.ID, "public", nil)
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		if err := gateway.Serve(listener); ctx.Err() == nil {
			m.setupFailed(record.ID, active, err)
		}
	}()
	m.mu.Unlock()
}
func (m *shareManager) setupFailed(id string, active *shareInstance, cause error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.records[id]
	if m.closing || r == nil || !r.live() {
		return
	}
	r.State = "failed"
	r.Phase = ""
	r.Reason = shareCode(cause)
	r.CleanupPending = true
	r.Revision = randomShareID()
	active.halt()
	shareLog(id, "failure", cause)
	if err := m.saveLocked(); err != nil {
		shareLog(id, "failure", err)
	}
	m.startCleanupLocked(r)
}
func (m *shareManager) endLocked(r *ShareRecord, by, reason string) error {
	if active := m.active[r.ID]; active != nil {
		active.halt()
	}
	r.State = "stopped"
	if reason == "expired" {
		r.State = "expired"
	}
	r.Phase = ""
	now := m.now().UTC()
	r.StoppedAt = &now
	r.StoppedBy = by
	r.Reason = reason
	r.Revision = randomShareID()
	r.CleanupPending = true
	shareLog(r.ID, reason, nil)
	err := m.saveLocked()
	if err != nil {
		m.err = shareStopStorageMessage
		shareLog(r.ID, "failure", err)
	}
	m.startCleanupLocked(r)
	if err != nil {
		return &shareError{code: "stop_storage", cause: err}
	}
	return nil
}
func (m *shareManager) stop(id, revision, by, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return shareErr("unavailable")
	}
	r := m.records[id]
	if r == nil || (revision != "" && revision != r.Revision) {
		return shareErr("revision")
	}
	if r.live() {
		return m.endLocked(r, by, reason)
	}
	if r.CleanupPending {
		m.startCleanupLocked(r)
	} else if normalShareEnd(r) {
		return m.finishCleanupLocked(r, nil)
	}
	return nil
}

// All terminal paths join this worker after provisioning has released its state.
func (m *shareManager) startCleanupLocked(r *ShareRecord) {
	active := m.active[r.ID]
	if active == nil {
		done := make(chan struct{})
		close(done)
		active = &shareInstance{done: done, cancel: func() {}}
		m.active[r.ID] = active
	}
	if active.cleaning {
		return
	}
	if active.timer != nil {
		active.timer.Stop()
	}
	active.cleaning = true
	m.workers.Add(1)
	go func() { defer m.workers.Done(); m.cleanup(r.ID, active) }()
}
func (m *shareManager) cleanup(id string, active *shareInstance) {
	select {
	case <-active.done:
	default:
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-active.done:
			timer.Stop()
		case <-timer.C:
			m.mu.Lock()
			defer m.mu.Unlock()
			if r := m.records[id]; r != nil {
				_ = m.finishCleanupLocked(r, shareErr("cleanup_pending"))
				m.retryCleanupLocked(r, active)
			}
			return
		}
	}
	m.mu.Lock()
	r := m.records[id]
	if r == nil {
		m.mu.Unlock()
		return
	}
	runtime := active.runtime
	copy := *r
	m.mu.Unlock()
	var err error
	if runtime == nil {
		runtime, err = m.factory(copy)
	}
	if err == nil {
		err = runtime.Logout(context.Background())
		if closeErr := runtime.Close(); err == nil {
			err = closeErr
		}
	}
	if err == nil {
		err = m.removeNodeState(id)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r = m.records[id]
	if r == nil {
		return
	}
	active.runtime = nil
	if err != nil {
		_ = m.finishCleanupLocked(r, &shareError{code: "cleanup_pending", cause: err})
		m.retryCleanupLocked(r, active)
	} else {
		delete(m.active, id)
		_ = m.finishCleanupLocked(r, nil)
	}
}
func (m *shareManager) finishCleanupLocked(r *ShareRecord, cause error) error {
	r.CleanupPending = cause != nil
	r.Phase = ""
	if cause != nil {
		r.State = "cleanup"
	} else {
		r.State = terminalShareState(r.Reason)
	}
	remove := normalShareEnd(r)
	if remove {
		delete(m.records, r.ID)
	}
	if err := m.saveLocked(); err != nil {
		if remove {
			m.records[r.ID] = r
		}
		shareLog(r.ID, "failure", err)
		return err
	}
	shareLog(r.ID, "cleanup", cause)
	return nil
}
func (m *shareManager) retryCleanupLocked(r *ShareRecord, active *shareInstance) {
	active.cleaning = false
	if m.closing {
		return
	}
	active.retries++
	delay := 5 * time.Minute
	if active.retries == 1 {
		delay = 5 * time.Second
	} else if active.retries == 2 {
		delay = 30 * time.Second
	}
	id := r.ID
	active.timer = time.AfterFunc(delay, func() { m.retryCleanup(id) })
}
func (m *shareManager) retryCleanup(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.records[id]; r != nil && r.CleanupPending && !m.closing {
		m.startCleanupLocked(r)
	}
}
func (m *shareManager) removeNodeState(id string) error {
	path := filepath.Join(m.store.dir, "nodes", id)
	if m.removeNodeDir != nil {
		return m.removeNodeDir(path)
	}
	return os.RemoveAll(path)
}
func normalShareEnd(r *ShareRecord) bool {
	return !r.live() && !r.CleanupPending && (r.Reason == "admin_stop" || r.Reason == "expired" || r.Reason == "sharing_disabled")
}
func terminalShareState(reason string) string {
	switch reason {
	case "expired":
		return "expired"
	case "", "admin_stop", "sharing_disabled", "target_changed", "preview_changed", "preview_gone", "preview_unreachable":
		return "stopped"
	default:
		return "failed"
	}
}
func setSharePhase(r *ShareRecord, phase string, now time.Time) {
	if r.Phase != phase {
		r.Phase = phase
		r.StartedAt = now.UTC()
	}
}

func (m *shareManager) review(id, revision, by string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return shareErr("unavailable")
	}
	r := m.records[id]
	if r == nil || r.Revision != revision || !r.live() {
		return shareErr("revision")
	}
	before := *r
	r.LastReviewedAt = m.now().UTC()
	r.LastReviewedBy = by
	r.NextReviewAt = r.LastReviewedAt.Add(30 * 24 * time.Hour)
	r.Revision = randomShareID()
	if err := m.saveLocked(); err != nil {
		*r = before
		return err
	}
	return nil
}
func (m *shareManager) dismiss(id, revision string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return shareErr("unavailable")
	}
	r := m.records[id]
	if r == nil || r.Revision != revision || r.live() || r.CleanupPending {
		return shareErr("revision")
	}
	delete(m.records, id)
	if err := m.saveLocked(); err != nil {
		m.records[id] = r
		return err
	}
	return nil
}

// Only successful catalog publications are evidence of removal or changed binding.
func (m *shareManager) catalogUpdated(view View, suffix, self string) {
	if view.Error != "" || view.Updated.IsZero() {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return
	}
	m.setTargetsLocked(view, suffix, self)
	m.resumeLocked()
}
func (m *shareManager) resumeLocked() {
	for _, r := range m.records {
		if !r.live() {
			if r.CleanupPending {
				if m.active[r.ID] == nil {
					m.startCleanupLocked(r)
				}
			} else if normalShareEnd(r) {
				_ = m.finishCleanupLocked(r, nil)
			}
			continue
		}
		if r.ExpiresAt != nil && !m.now().Before(*r.ExpiresAt) {
			_ = m.endLocked(r, "", "expired")
			continue
		}
		if r.Binding.Kind == "service" && m.undetected[r.Binding.Service] {
			continue
		}
		if r.PreviewFingerprint == "" {
			target, ok := m.targets[r.TargetID]
			if !ok || target.Binding != r.Binding || !target.eligible {
				_ = m.endLocked(r, "", "target_changed")
				continue
			}
		}
		if m.active[r.ID] == nil {
			shareLog(r.ID, "start", nil)
			m.launchLocked(r)
		}
	}
}

// Shutdown keeps unexpired node state for reuse; nothing logs the nodes out.
func (m *shareManager) Close() {
	m.mu.Lock()
	if m.closing {
		m.mu.Unlock()
		return
	}
	m.closing = true
	for id, active := range m.active {
		active.halt()
		r := m.records[id]
		if r != nil && r.live() {
			if r.ExpiresAt == nil || m.now().Before(*r.ExpiresAt) {
				continue
			}
			_ = m.endLocked(r, "", "expired")
		} else if r != nil && r.CleanupPending {
			m.startCleanupLocked(r)
		}
	}
	m.mu.Unlock()
	m.workers.Wait()

}
