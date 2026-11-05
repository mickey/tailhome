package main

import (
	"errors"
	"testing"
	"time"

	tsapi "tailscale.com/client/tailscale/v2"
)

func TestShareResumeDefersOnListingErrorAndPreservesExpiry(t *testing.T) {
	m, runtime, target := fakeShareManager(t)
	deadline := time.Now().Add(time.Hour).UTC()
	id := randomShareID()
	r := &ShareRecord{ID: id, Hostname: "public-" + id, TargetID: target.Tile.TargetID, Binding: target.Binding, Revision: "revision", State: "starting", CreatedAt: time.Now().UTC(), NextReviewAt: deadline.Add(29 * 24 * time.Hour), ExpiresAt: &deadline}
	m.records[id] = r
	listing := &fakeLister{err: errors.New("API temporarily unavailable")}
	catalog := newCatalog(listing, &fakeNet{}, "")
	catalog.DNSSuffix = "test.ts.net"
	catalog.OnRefresh = func(view View) { m.catalogUpdated(view, catalog.DNSSuffix, catalog.Self) }
	catalog.Refresh(t.Context())
	select {
	case <-runtime.started:
		t.Fatal("started without successful discovery")
	default:
	}
	if !r.live() {
		t.Fatal("listing failure revoked consent")
	}
	listing.err = nil
	listing.services = []tsapi.Service{{Name: "svc:app", Ports: []string{"tcp:443"}}}
	catalog.Refresh(t.Context())
	select {
	case <-runtime.started:
	case <-time.After(time.Second):
		t.Fatal("successful catalog did not resume")
	}
	m.mu.Lock()
	sameExpiry := r.ExpiresAt.Equal(deadline)
	m.mu.Unlock()
	if !sameExpiry {
		t.Fatal("resume extended expiry")
	}
	listing.err = errors.New("API hiccup")
	catalog.Refresh(t.Context())
	m.mu.Lock()
	live := r.live()
	m.mu.Unlock()
	if !live {
		t.Fatal("later listing failure stopped running share")
	}
	listing.err = nil
	listing.services = nil
	catalog.Refresh(t.Context())
	m.workers.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.live() || r.Reason != "target_changed" {
		t.Fatal("successful removal did not stop prior binding")
	}
}
func TestShareExpiredRestartNeverProvisions(t *testing.T) {
	m, runtime, target := fakeShareManager(t)
	deadline := time.Now().Add(-time.Hour)
	id := randomShareID()
	r := &ShareRecord{ID: id, Hostname: "public-" + id, TargetID: target.Tile.TargetID, State: "starting", Binding: target.Binding, CreatedAt: deadline.Add(-time.Hour), NextReviewAt: deadline.Add(30 * 24 * time.Hour), ExpiresAt: &deadline}
	m.records[id] = r
	m.catalogUpdated(View{Updated: time.Now()}, "test.ts.net", "svc:home")
	m.workers.Wait()
	select {
	case <-runtime.started:
		t.Fatal("expired share opened a listener")
	default:
	}
	assertShareRetired(t, m, id)
}
func TestHiddenTargetsUseCatalogPipeline(t *testing.T) {
	t.Run("service survives resume", func(t *testing.T) {
		m, runtime, target := fakeShareManager(t)
		now := time.Now()
		id := randomShareID()
		m.records[id] = &ShareRecord{ID: id, Hostname: "public-" + id, TargetID: target.Tile.TargetID, Binding: target.Binding, State: "starting", CreatedAt: now, NextReviewAt: now.Add(30 * 24 * time.Hour)}
		catalog := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:app", Ports: []string{"tcp:443"}}}}, &fakeNet{}, writeOverrides(t, "app: {hidden: true}\n"))
		catalog.DNSSuffix = "test.ts.net"
		catalog.IncludeHidden = true
		catalog.OnRefresh = func(view View) { m.catalogUpdated(view, catalog.DNSSuffix, catalog.Self) }
		catalog.Refresh(t.Context())
		select {
		case <-runtime.started:
		case <-time.After(time.Second):
			t.Fatal("hidden target was lost on resume")
		}
		view := catalog.View()
		if len(view.all()) != 0 || len(view.hidden) != 1 || len(m.snapshot(true).Targets) != 0 {
			t.Fatal("hidden resume leaked a tile")
		}
	})
	t.Run("device metadata remains authoritative", func(t *testing.T) {
		device := catalogDevice("device", "app", "100.64.0.1")
		catalog, _, network, ca := deviceCatalog(t, device)
		catalog.IncludeHidden = true
		catalog.OverridesPath = writeOverrides(t, "app: {hidden: true}\n")
		network.addrs["100.64.0.1:443"] = ca.serve(t, device.DNSName, 200)
		catalog.Refresh(t.Context())
		view := catalog.View()
		if len(view.all()) != 0 || len(view.hidden) != 1 {
			t.Fatal("hidden device was discarded or rendered")
		}
		target := targetForEntry(view.hidden[0], catalog.DNSSuffix, catalog.Self)
		if !target.eligible || target.Binding.NodeID != string(device.ID) || target.Tile.Reason == "" {
			t.Fatal("hidden device lost its approved binding")
		}
	})
}

func TestShareProbeDetectedServiceWaitsForAppOnRestart(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		name := "visible"
		if hidden {
			name = "hidden"
		}
		t.Run(name, func(t *testing.T) {
			m, runtime, _ := fakeShareManager(t)
			entry := Entry{Source: "service", Name: "app", Label: "App", Host: "app.test.ts.net", URL: "http://app.test.ts.net:3000/", Ports: []string{"tcp:3000"}}
			target := targetForEntry(entry, "test.ts.net", "svc:home")
			now := time.Now().UTC()
			id := randomShareID()
			r := &ShareRecord{ID: id, Hostname: "public-" + id, TargetID: target.Tile.TargetID, Binding: target.Binding, State: "starting", CreatedAt: now, NextReviewAt: now.Add(30 * 24 * time.Hour)}
			m.records[id] = r
			network := &fakeNet{addrs: map[string]string{}}
			path := ""
			if hidden {
				path = writeOverrides(t, "app: {hidden: true}\n")
			}
			catalog := newCatalog(&fakeLister{services: []tsapi.Service{{Name: "svc:app", Ports: []string{"tcp:3000"}}}}, network, path)
			catalog.DNSSuffix = "test.ts.net"
			catalog.IncludeHidden = true
			catalog.OnRefresh = func(view View) { m.catalogUpdated(view, catalog.DNSSuffix, catalog.Self) }
			catalog.Refresh(t.Context())
			m.mu.Lock()
			waiting := r.live() && m.active[id] == nil
			m.mu.Unlock()
			if !waiting {
				t.Fatal("undetected app was stopped or launched")
			}
			select {
			case <-runtime.started:
				t.Fatal("undetected app provisioned")
			default:
			}
			network.addrs["app.test.ts.net:3000"] = serveHTTP(t, 200)
			catalog.Refresh(t.Context())
			select {
			case <-runtime.started:
			case <-time.After(time.Second):
				t.Fatal("detected app did not resume")
			}
			delete(network.addrs, "app.test.ts.net:3000")
			catalog.Refresh(t.Context())
			catalog.Refresh(t.Context())
			m.mu.Lock()
			live := r.live()
			m.mu.Unlock()
			if !live || catalog.detected["app"] != entry.URL {
				t.Fatal("temporary outage lost the detected binding")
			}
		})
	}
}
