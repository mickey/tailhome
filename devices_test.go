package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"tailscale.com/client/local"
	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

type fakeDeviceLister struct {
	devices []Device
	err     error
	calls   int
}

func (f *fakeDeviceLister) Devices(context.Context) ([]Device, error) {
	f.calls++
	return f.devices, f.err
}

type deviceCA struct {
	roots *x509.CertPool
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
}

func newDeviceCA(t *testing.T) *deviceCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "tailhome test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(cert)
	return &deviceCA{roots: roots, cert: cert, key: key}
}

func (ca *deviceCA) leaf(t *testing.T, name string, expired bool) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Now().Add(time.Hour)
	if expired {
		notAfter = time.Now().Add(-time.Hour)
	}
	cert := &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{name},
		NotBefore: time.Now().Add(-2 * time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, cert, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func serveDeviceTLS(t *testing.T, cert tls.Certificate, handler http.Handler) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().String()
}

// serve answers https://<name>/ with status and asserts the probe's request
// contract: GET /, Host and SNI = name, tailhome named in User-Agent, redirects not followed.
func (ca *deviceCA) serve(t *testing.T, name string, status int) string {
	t.Helper()
	return serveDeviceTLS(t, ca.leaf(t, name, false), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cardPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/" || r.Host != name || r.TLS.ServerName != name {
			t.Errorf("got %s %s%s SNI %s, want GET %s/", r.Method, r.Host, r.URL.Path, r.TLS.ServerName, name)
		}
		if !strings.HasPrefix(r.UserAgent(), "tailhome/") {
			t.Errorf("User-Agent %q does not name tailhome", r.UserAgent())
		}
		w.Header().Set("Location", "https://"+name+"/elsewhere")
		w.WriteHeader(status)
	}))
}

// probe exercises the root-health contract without fetching a card.
func (p *DeviceProber) probe(ctx context.Context, d Device, insecure bool) (bool, Status, string, bool) {
	check := deviceCheck{Device: d, port: 443, insecure: insecure, state: &endpointState{tlsUp: true}}
	p.check(ctx, &check, false)
	return check.answered, check.status, check.reason, check.unverified
}

func TestDeviceProberTLS(t *testing.T) {
	ca := newDeviceCA(t)
	other := newDeviceCA(t)
	for _, tt := range []struct {
		name       string
		cert       tls.Certificate
		insecure   bool
		answered   bool
		unverified bool
	}{
		{"valid", ca.leaf(t, "app.ts.test", false), false, true, false},
		{"untrusted", other.leaf(t, "app.ts.test", false), false, false, false},
		{"wrong hostname", ca.leaf(t, "other.ts.test", false), false, false, false},
		{"expired", ca.leaf(t, "app.ts.test", true), false, false, false},
		{"valid exception", ca.leaf(t, "app.ts.test", false), true, true, false},
		{"untrusted exception", other.leaf(t, "app.ts.test", false), true, true, true},
		{"wrong hostname exception", ca.leaf(t, "other.ts.test", false), true, true, true},
		{"expired exception", ca.leaf(t, "app.ts.test", true), true, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			addr := serveDeviceTLS(t, tt.cert, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }))
			n := &fakeNet{addrs: map[string]string{"100.64.0.1:443": addr}}
			p := &DeviceProber{dial: n.dial, roots: ca.roots}
			answered, status, reason, unverified := p.probe(context.Background(), catalogDevice("node", "app", "100.64.0.1"), tt.insecure)
			if answered != tt.answered {
				t.Fatalf("answered = %v, reason %q", answered, reason)
			}
			if unverified != tt.unverified {
				t.Errorf("unverified = %v, want %v", unverified, tt.unverified)
			}
			if answered && status != StatusUp {
				t.Errorf("status = %q, reason %q", status, reason)
			}
			if !answered && !strings.Contains(reason, "x509:") {
				t.Errorf("expected certificate reason, got %q", reason)
			}
			if n.dials.Load() != 1 {
				t.Errorf("dials = %d", n.dials.Load())
			}
		})
	}
}

func TestDeviceProberResponses(t *testing.T) {
	ca := newDeviceCA(t)
	for _, code := range []int{200, 301, 401, 403, 404, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			n := &fakeNet{addrs: map[string]string{"100.64.0.1:443": ca.serve(t, "app.ts.test", code)}}
			p := &DeviceProber{dial: n.dial, roots: ca.roots}
			answered, status, reason, _ := p.probe(context.Background(), catalogDevice("node", "app", "100.64.0.1"), false)
			if !answered {
				t.Fatalf("not answered: %s", reason)
			}
			if (status == StatusDown) != (code >= 500) {
				t.Errorf("status %q for %d", status, code)
			}
			if n.dials.Load() != 1 {
				t.Errorf("dials = %d", n.dials.Load())
			}
		})
	}
}

func TestDeviceProberNoIP(t *testing.T) {
	n := &fakeNet{}
	p := &DeviceProber{dial: n.dial}
	answered, status, reason, _ := p.probe(context.Background(), Device{DNSName: "app.ts.test"}, false)
	if answered || status != StatusDown || reason != "device has no Tailscale IP" || n.dials.Load() != 0 {
		t.Fatalf("got %v %s %q, dials %d", answered, status, reason, n.dials.Load())
	}
}

func TestDeviceProberTimeout(t *testing.T) {
	n := &fakeNet{addrs: map[string]string{"100.64.0.1:443": serveSilent(t)}}
	p := &DeviceProber{dial: n.dial, timeout: 20 * time.Millisecond}
	answered, status, reason, _ := p.probe(context.Background(), catalogDevice("node", "app", "100.64.0.1"), false)
	// The handshake timer and the probe deadline are equal and start microseconds apart; either may fire first.
	if answered || status != StatusDown || !(strings.Contains(reason, "deadline exceeded") || strings.Contains(reason, "handshake timeout")) {
		t.Fatalf("got %v %s %q", answered, status, reason)
	}
}

func TestLocalDevices(t *testing.T) {
	tags := views.SliceOf([]string{"tag:app"})
	status := &ipnstate.Status{Peer: map[key.NodePublic]*ipnstate.PeerStatus{
		key.NewNode().Public(): {ID: "listed", DNSName: "app.ts.test.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.0.1")}, Tags: &tags, Online: true, InNetworkMap: true},
		key.NewNode().Public(): {ID: "stale", DNSName: "stale.ts.test.", Online: true},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/localapi/v0/status" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(status); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(srv.Close)
	l := localDevices{lc: &local.Client{Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, srv.Listener.Addr().String())
	}}}
	got, err := l.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("devices = %+v", got)
	}
	d := got[0]
	if d.ID != "listed" || d.DNSName != "app.ts.test" || d.IP.String() != "100.64.0.1" || len(d.Tags) != 1 || d.Tags[0] != "tag:app" || !d.Online {
		t.Fatalf("device = %+v", d)
	}
}

func catalogDevice(id, name, ip string) Device {
	return Device{ID: tailcfg.StableNodeID(id), DNSName: name + ".ts.test", IP: netip.MustParseAddr(ip), Online: true}
}

func deviceCatalog(t *testing.T, devices ...Device) (*Catalog, *fakeDeviceLister, *fakeNet, *deviceCA) {
	t.Helper()
	ca := newDeviceCA(t)
	n := &fakeNet{addrs: map[string]string{}}
	l := &fakeDeviceLister{devices: devices}
	c := newCatalog(&fakeLister{}, n, "")
	c.Self = ""
	c.Devices = l
	c.DeviceProber = &DeviceProber{dial: n.dial, roots: ca.roots, timeout: 200 * time.Millisecond}
	return c, l, n, ca
}

func deviceEntry(t *testing.T, c *Catalog, name string) Entry {
	t.Helper()
	entries, _ := index(c.View())
	e, ok := entries[name]
	if !ok {
		t.Fatalf("missing %s: %+v", name, c.View())
	}
	return e
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	old, flags := log.Writer(), log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(old)
		log.SetFlags(flags)
	})
	return &logs
}

func TestCatalogDeviceAdmissionHealthAndRemoval(t *testing.T) {
	d := catalogDevice("node", "paperless-ngx", "100.64.0.1")
	c, l, n, ca := deviceCatalog(t, d)
	n.addrs["100.64.0.1:443"] = ca.serve(t, d.DNSName, 403)
	c.Refresh(t.Context())
	e := deviceEntry(t, c, "paperless-ngx")
	if e.Source != "device" || e.Group != "Apps" || e.Label != "Paperless Ngx" || e.Host != d.DNSName || e.URL != "https://"+d.DNSName+"/" || e.Status != StatusUp || e.Copy != "" || e.probe != nil || len(e.Ports) != 1 || e.Ports[0] != "tcp:443" {
		t.Fatalf("device entry: %+v", e)
	}
	l.devices[0].Online = false
	before := n.dials.Load()
	c.Refresh(t.Context())
	if e = deviceEntry(t, c, "paperless-ngx"); e.Status != StatusDown || e.Reason != "Device offline" || n.dials.Load() != before {
		t.Fatalf("offline: %+v, dials %d -> %d", e, before, n.dials.Load())
	}
	l.devices[0].Online = true
	n.addrs["100.64.0.1:443"] = ca.serve(t, d.DNSName, 503)
	c.Refresh(t.Context())
	if e = deviceEntry(t, c, "paperless-ngx"); e.Status != StatusDown || e.Reason != "HTTP 503" {
		t.Fatalf("HTTP failure: %+v", e)
	}
	n.addrs["100.64.0.1:443"] = serveHTTPS(t)
	c.Refresh(t.Context())
	if e = deviceEntry(t, c, "paperless-ngx"); e.Status != StatusDown || !strings.Contains(e.Reason, "x509:") {
		t.Fatalf("certificate failure lost admitted tile or x509 reason: %+v", e)
	}
	l.devices = nil
	c.Refresh(t.Context())
	if len(c.View().all()) != 0 || len(c.admitted) != 0 {
		t.Fatalf("departed peer retained: %+v", c.View())
	}
}

func TestCatalogDeviceNeverProbesIneligible(t *testing.T) {
	for _, kind := range []string{"off", "offline", "hidden", "tag-filter", "foreign", "nested", "service", "hidden-service", "self-service"} {
		t.Run(kind, func(t *testing.T) {
			d := catalogDevice("node", "app", "100.64.0.1")
			c, l, n, _ := deviceCatalog(t, d)
			switch kind {
			case "off":
				c.Devices = nil
			case "offline":
				l.devices[0].Online = false
			case "hidden":
				c.OverridesPath = writeOverrides(t, "app: {hidden: true}\n")
			case "tag-filter":
				c.DeviceTags = []string{"tag:app"}
			case "foreign":
				l.devices[0].DNSName = "app.foreign.ts.net"
			case "nested":
				l.devices[0].DNSName = "app.nested.ts.test"
			default:
				c.Services = &fakeLister{services: []tsapi.Service{{Name: "svc:app", Ports: []string{"tcp:80"}}}}
				if kind == "hidden-service" {
					c.OverridesPath = writeOverrides(t, "app: {hidden: true}\n")
				}
				if kind == "self-service" {
					c.Self = "svc:app"
				}
			}
			c.Refresh(t.Context())
			if len(c.admitted) != 0 {
				t.Fatal("ineligible device admitted")
			}
			// The visible Service may be checked, but no device IP is ever dialled.
			wantDials := int64(0)
			if kind == "service" {
				wantDials = 1
			}
			if n.dials.Load() != wantDials {
				t.Fatalf("got %d dials, want %d Service-only dials", n.dials.Load(), wantDials)
			}
			if kind == "off" && l.calls != 0 {
				t.Fatal("disabled device listing called")
			}
		})
	}
}

func TestCatalogDeviceIdentityAndPolicy(t *testing.T) {
	for _, change := range []string{"new-node", "rename", "filter", "hidden", "collision", "policy", "IP"} {
		t.Run(change, func(t *testing.T) {
			d := catalogDevice("old", "app", "100.64.0.1")
			d.Tags = []string{"tag:app"}
			c, l, n, ca := deviceCatalog(t, d)
			c.DeviceTags = []string{"tag:app"}
			c.InsecureTLSTags = []string{"tag:nas"}
			n.addrs["100.64.0.1:443"] = ca.serve(t, d.DNSName, 200)
			c.Refresh(t.Context())
			deviceEntry(t, c, "app")
			delete(n.addrs, "100.64.0.1:443")
			switch change {
			case "new-node":
				l.devices[0].ID = "new"
			case "rename":
				l.devices[0].DNSName = "renamed.ts.test"
			case "filter":
				l.devices[0].Tags = nil
			case "hidden":
				c.OverridesPath = writeOverrides(t, "app: {hidden: true}\n")
			case "collision":
				c.Services = &fakeLister{services: []tsapi.Service{{Name: "svc:app", Ports: []string{"tcp:80"}}}}
			case "policy":
				l.devices[0].Tags = []string{"tag:app", "tag:nas"}
			case "IP":
				l.devices[0].IP = netip.MustParseAddr("100.64.0.2")
				n.addrs["100.64.0.2:443"] = ca.serve(t, d.DNSName, 200)
			}
			c.Refresh(t.Context())
			if change == "IP" {
				if e := deviceEntry(t, c, "app"); e.Status != StatusUp {
					t.Fatalf("IP update not used: %+v", e)
				}
				if c.admitted[endpointID{d.ID, 443}].IP != l.devices[0].IP {
					t.Fatal("recorded IP not refreshed")
				}
			} else if len(c.admitted) != 0 {
				t.Fatalf("changed identity was not required to re-admit: %+v", c.admitted)
			}
		})
	}
}

func TestCatalogDeviceInsecurePolicyAndTeachingLog(t *testing.T) {
	for _, tagged := range []bool{true, false} {
		t.Run(fmt.Sprint(tagged), func(t *testing.T) {
			logs := captureLogs(t)
			d := catalogDevice("node", "nas", "100.64.0.1")
			if tagged {
				d.Tags = []string{"tag:app"}
			}
			c, l, n, _ := deviceCatalog(t, d)
			c.InsecureTLSTags = []string{"tag:nas"}
			other := newDeviceCA(t)
			n.addrs["100.64.0.1:443"] = other.serve(t, d.DNSName, 200)
			c.Refresh(t.Context())
			c.Refresh(t.Context())
			if len(c.View().all()) != 0 {
				t.Fatal("untrusted device listed")
			}
			if strings.Count(logs.String(), "nas answers HTTPS") != 1 {
				t.Fatalf("teaching log not once: %s", logs.String())
			}
			advice := "tag it and add the tag"
			if tagged {
				advice = "add one of its tags"
			}
			if !strings.Contains(logs.String(), advice) {
				t.Fatalf("missing tag advice: %s", logs.String())
			}
			l.devices[0].Tags = append(l.devices[0].Tags, "tag:nas")
			c.Refresh(t.Context())
			if e := deviceEntry(t, c, "nas"); !e.Unverified || e.Description != "" || e.Status != StatusUp {
				t.Fatalf("exception: %+v", e)
			}
			if strings.Contains(logs.String(), "nas card request:") {
				t.Fatalf("TLS policy produced a card diagnostic: %s", logs.String())
			}
			l.devices[0].Tags = nil
			c.Refresh(t.Context())
			if len(c.admitted) != 0 || len(c.View().all()) != 0 {
				t.Fatal("policy removal retained untrusted tile")
			}
		})
	}
}

func TestCatalogDeviceInsecureDescriptionTracksCertificate(t *testing.T) {
	d := catalogDevice("node", "app", "100.64.0.1")
	d.Tags = []string{"tag:app"}
	c, _, n, ca := deviceCatalog(t, d)
	c.InsecureTLSTags = []string{"tag:app"}
	valid := ca.serve(t, d.DNSName, 200)
	selfSigned := serveHTTPS(t)
	for _, step := range []struct {
		addr        string
		description string
	}{
		{valid, ""},
		{selfSigned, "Unverified HTTPS"},
		{valid, ""},
	} {
		n.addrs["100.64.0.1:443"] = step.addr
		before := n.dials.Load()
		c.Refresh(t.Context())
		if e := deviceEntry(t, c, "app"); e.Unverified != (step.description != "") || e.Description != "" || e.Status != StatusUp {
			t.Fatalf("certificate description: %+v, want %q", e, step.description)
		}
		if got := n.dials.Load() - before; got != 1 {
			t.Fatalf("probe used %d dials, want one shared connection", got)
		}
	}
	c.OverridesPath = writeOverrides(t, "app: {description: Private app}\n")
	c.Refresh(t.Context())
	if e := deviceEntry(t, c, "app"); e.Description != "Private app" {
		t.Fatalf("description override lost: %+v", e)
	}
}

func TestCatalogDeviceOverridesSourcesAndTooltip(t *testing.T) {
	d := catalogDevice("node", "app", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	c.OverridesPath = writeOverrides(t, "app: {label: Custom, description: Private app, icon: custom, group: Work, order: -2, url: 'https://other.example/path', web: false, copy: ignored}\n")
	c.Services = &fakeLister{services: []tsapi.Service{{Name: "svc:web", Ports: []string{"tcp:80"}}}}
	n.addrs["web.ts.test:80"] = serveHTTP(t, 200)
	n.addrs["100.64.0.1:443"] = ca.serve(t, d.DNSName, 200)
	c.Refresh(t.Context())
	e := deviceEntry(t, c, "app")
	if e.Label != "Custom" || e.Description != "Private app" || e.Icon != iconURL("custom") || e.Group != "Work" || e.order != -2 || e.URL != "https://other.example/path" || e.Copy != "" || e.Status != StatusUp || len(c.View().Other) != 0 {
		t.Fatalf("overrides: %+v", e)
	}
	if c.warned["override:app"] {
		t.Fatal("member override warned as unknown")
	}
	h := NewHandler(c, "home")
	for _, target := range []string{"/api/services.json", "/api/list.json"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		if !json.Valid(rec.Body.Bytes()) || !strings.Contains(rec.Body.String(), `"source":"device"`) || !strings.Contains(rec.Body.String(), `"source":"service"`) {
			t.Fatalf("%s: %s", target, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if !strings.Contains(rec.Body.String(), "Open app.ts.test · device") || strings.Contains(rec.Body.String(), "Open web.ts.test · device") {
		t.Fatal("device tooltip provenance missing or applied to Service")
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/go/app", nil))
	if rec.Header().Get("Location") != e.URL {
		t.Fatalf("device shortcut: %s", rec.Header().Get("Location"))
	}
}

func TestCatalogDeviceListingFailureIsolation(t *testing.T) {
	logs := captureLogs(t)
	d := catalogDevice("node", "app", "100.64.0.1")
	d.Tags = []string{"tag:app"}
	c, l, n, ca := deviceCatalog(t, d)
	c.DeviceTags, c.InsecureTLSTags = []string{"tag:app"}, []string{"tag:app"}
	s := &fakeLister{services: []tsapi.Service{{Name: "svc:web", Ports: []string{"tcp:80"}}}}
	c.Services = s
	n.addrs["web.ts.test:80"] = serveHTTP(t, 200)
	n.addrs["100.64.0.1:443"] = ca.serve(t, d.DNSName, 200)
	c.Refresh(t.Context())
	updated := c.View().Updated
	l.err = errors.New("netmap unavailable")
	l.devices = nil
	delete(n.addrs, "100.64.0.1:443")
	c.Refresh(t.Context())
	c.Refresh(t.Context())
	if e := deviceEntry(t, c, "app"); e.Status != StatusUp {
		t.Fatalf("failed listing changed snapshot: %+v", e)
	}
	if e := deviceEntry(t, c, "web"); e.Status != StatusUp || c.View().Error == "" || !c.View().Updated.Equal(updated) {
		t.Fatalf("failed listing did not preserve snapshot and error: %+v, %s", e, c.View().Error)
	}
	listingTransitions := 0
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if strings.HasPrefix(line, "devices: listing failed:") {
			listingTransitions++
		}
	}
	if len(c.admitted) != 1 || listingTransitions != 1 {
		t.Fatalf("listing failure state: %s", logs.String())
	}
	c.OverridesPath = writeOverrides(t, "app: {hidden: true}\n")
	c.Refresh(t.Context())
	if len(c.admitted) != 0 || !strings.Contains(logs.String(), "device app removed: hidden") {
		t.Fatalf("hidden during outage: %s", logs.String())
	}
	l.err = nil
	c.Refresh(t.Context())
	if len(c.admitted) != 0 || strings.Count(logs.String(), "devices: listing works again") != 1 {
		t.Fatalf("recovery: %s", logs.String())
	}
}

func TestListingFailureOnlyProbesAdmittedEndpoints(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, l, n, ca := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	serveCard(t, n, ca, d, 443, "", 404, 200)
	c.Refresh(t.Context())
	if len(c.admitted) != 1 {
		t.Fatalf("initial admissions: %v", c.admitted)
	}
	// A newly reachable sibling must wait for a successful device listing.
	srv := serveCard(t, n, ca, d, 8443, `{"title":"New preview"}`, 200, 0)
	before := n.dials.Load()
	l.err = errors.New("netmap unavailable")
	c.Refresh(t.Context())
	if got := n.dials.Load() - before; got != 1 {
		t.Fatalf("outage used %d dials, want only the admitted endpoint", got)
	}
	if cards, _ := srv.counts(); cards != 0 || len(c.admitted) != 1 {
		t.Fatalf("outage probed or admitted sibling: cards=%d admissions=%v", cards, c.admitted)
	}
	l.err = nil
	c.Refresh(t.Context())
	if len(c.admitted) != 2 || len(c.View().Previews) != 1 {
		t.Fatalf("recovery did not admit sibling: %+v", c.View())
	}
}

func TestCatalogServiceAndOverrideErrorsSkipDevices(t *testing.T) {
	c, l, _, _ := deviceCatalog(t, catalogDevice("node", "app", "100.64.0.1"))
	c.Services = &fakeLister{err: errors.New("services unavailable")}
	c.Refresh(t.Context())
	if l.calls != 0 || c.View().Error == "" {
		t.Fatal("Service error did not stop device phase")
	}
	c.Services = &fakeLister{}
	c.OverridesPath = writeOverrides(t, "app: {unknown: true}\n")
	c.Refresh(t.Context())
	if l.calls != 0 || c.View().Error == "" {
		t.Fatal("override error did not stop device phase")
	}
}

func TestCatalogDevicePoolBudgetAndCursor(t *testing.T) {
	var devices []Device
	for i := range 24 {
		devices = append(devices, catalogDevice(fmt.Sprint(i), fmt.Sprintf("app%02d", i), fmt.Sprintf("100.64.0.%d", i+1)))
	}
	c, _, _, _ := deviceCatalog(t, devices...)
	c.scanBudget = 10 * time.Millisecond
	c.DeviceProber.timeout = 60 * time.Millisecond
	active, peak := 0, 0
	var mu sync.Mutex
	seen := map[string]int{}
	c.DeviceProber.dial = func(_ context.Context, _, addr string) (net.Conn, error) {
		mu.Lock()
		active++
		peak = max(peak, active)
		seen[addr]++
		mu.Unlock()
		time.Sleep(30 * time.Millisecond) // shorter than the probe timeout: the dial ends before the probe returns
		mu.Lock()
		active--
		mu.Unlock()
		return nil, errors.New("unreachable")
	}
	c.Refresh(t.Context())
	first := c.cursor
	if first == "" {
		t.Fatal("cursor did not advance")
	}
	c.Refresh(t.Context())
	if c.cursor == first {
		t.Fatal("cursor failed to rotate")
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 8 || peak == 0 {
		t.Fatalf("pool peak = %d", peak)
	}
	if len(seen) <= 8 {
		t.Fatalf("scan starved later peers: %v", seen)
	}
	for addr, count := range seen {
		if count != 1 {
			t.Errorf("%s requested %d times before scan wrapped", addr, count)
		}
	}
}

func TestCatalogDeviceShortScanKeepsAdmitted(t *testing.T) {
	d := catalogDevice("admitted", "known", "100.64.0.1")
	c, l, n, ca := deviceCatalog(t, d)
	n.addrs["100.64.0.1:443"] = ca.serve(t, d.DNSName, 200)
	c.Refresh(t.Context())
	for i := range 16 {
		l.devices = append(l.devices, catalogDevice(fmt.Sprint(i), fmt.Sprintf("new%02d", i), fmt.Sprintf("100.64.0.%d", i+2)))
	}
	c.scanBudget = time.Nanosecond
	c.Refresh(t.Context())
	if deviceEntry(t, c, "known").Status != StatusUp || len(c.admitted) != 1 {
		t.Fatal("short scan pruned admitted device")
	}
}

func TestCatalogDeviceCandidateBudgetStartsAfterAdmitted(t *testing.T) {
	var devices []Device
	for i := range 16 {
		devices = append(devices, catalogDevice(fmt.Sprint(i), fmt.Sprintf("known%02d", i), fmt.Sprintf("100.64.0.%d", i+1)))
	}
	candidate := catalogDevice("new", "new", "100.64.0.100")
	c, _, n, ca := deviceCatalog(t, append(devices, candidate)...)
	c.admitted = map[endpointID]Device{}
	for _, d := range devices {
		c.admitted[endpointID{d.ID, 443}] = d
	}
	c.scanBudget = 300 * time.Millisecond
	c.DeviceProber.timeout = time.Second
	n.addrs["100.64.0.100:443"] = ca.serve(t, candidate.DNSName, 200)
	c.DeviceProber.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr == "100.64.0.100:443" {
			return n.dial(ctx, network, addr)
		}
		time.Sleep(200 * time.Millisecond) // slower than the candidate, faster than the probe timeout
		return nil, errors.New("device unreachable")
	}
	c.Refresh(t.Context())
	if e := deviceEntry(t, c, "new"); e.Status != StatusUp || len(c.admitted) != 17 {
		t.Fatalf("admitted checks consumed candidate budget: %+v", e)
	}
}

// cardServer counts card and root requests; a zero root code rejects root checks.
type cardServer struct {
	t            *testing.T
	mu           sync.Mutex
	body         string
	code, root   int
	cards, roots int
}

func (s *cardServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Path != cardPath {
		s.roots++
		if s.root == 0 {
			s.t.Errorf("unexpected root request on %s", r.Host)
		}
		w.WriteHeader(cmp.Or(s.root, 500))
		return
	}
	s.cards++
	w.WriteHeader(s.code)
	fmt.Fprint(w, s.body)
}

func (s *cardServer) set(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body = body
}

func (s *cardServer) counts() (cards, roots int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cards, s.roots
}

func serveCard(t *testing.T, n *fakeNet, ca *deviceCA, d Device, port uint16, body string, code, root int) *cardServer {
	t.Helper()
	s := &cardServer{t: t, body: body, code: code, root: root}
	n.addrs[netip.AddrPortFrom(d.IP, port).String()] = serveDeviceTLS(t, ca.leaf(t, d.DNSName, false), s)
	return s
}

func TestPreviewProbeLifecycle(t *testing.T) {
	for _, port := range []uint16{443, 8443} {
		t.Run(fmt.Sprint(port), func(t *testing.T) {
			logs := captureLogs(t)
			d := catalogDevice("node", "devbox", "100.64.0.1")
			c, l, n, ca := deviceCatalog(t, d)
			if port != 443 {
				c.PreviewPorts = []uint16{port}
			}
			srv := serveCard(t, n, ca, d, port, `{"title":"A title","description":"A description","icon":"shopify","preview":true,"set":"Landing","version":"A"}`, 200, 502)
			c.Refresh(t.Context())
			name := endpointName("devbox", port)
			check := func(wantCards int) {
				t.Helper()
				e := deviceEntry(t, c, name)
				if !e.Temporary || e.Check != "tls" || e.Status != StatusUp || e.Label != "A title" || e.Set != "Landing" || e.Version != "A" {
					t.Fatalf("preview: %+v", e)
				}
				cards, roots := srv.counts()
				if cards != wantCards || roots != 0 {
					t.Fatalf("requests cards=%d roots=%d, want %d/0", cards, roots, wantCards)
				}
			}
			check(1)
			c.Refresh(t.Context())
			check(1)
			c.RefreshManualIfOlder(t.Context(), 0)
			check(2)
			// A skipped refresh does not fetch or publish.
			updated := c.View().Updated
			c.RefreshManualIfOlder(t.Context(), time.Hour)
			check(2)
			if !c.View().Updated.Equal(updated) {
				t.Fatal("skipped refresh published")
			}
			l.devices[0].Online = false
			c.Refresh(t.Context())
			if len(c.View().Previews) != 0 || len(c.admitted) != 1 {
				t.Fatalf("down preview: %+v", c.View())
			}
			l.devices[0].Online = true
			c.Refresh(t.Context())
			check(3)
			if strings.Contains(logs.String(), "devbox listed") || strings.Contains(logs.String(), name+" is down") || strings.Contains(logs.String(), "listing 1 apps") {
				t.Fatalf("preview lifecycle noise: %s", logs.String())
			}
			// A valid empty replacement clears metadata and preview classification
			// on 443, but cannot cancel the configured extra-port policy.
			srv.set(`{}`)
			c.RefreshManualIfOlder(t.Context(), 0)
			e := deviceEntry(t, c, name)
			if port == 443 {
				if e.Temporary || e.Check != "http" || e.Status != StatusDown {
					t.Fatalf("permanent root semantics: %+v", e)
				}
			} else if !e.Temporary || e.Label != "Devbox :8443" || e.Set != "" || e.Version != "" {
				t.Fatalf("pool fallback: %+v", e)
			}
		})
	}
}

func TestPreviewEndpointEligibilityAndOverrides(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	c.Services = &fakeLister{services: []tsapi.Service{{Name: "svc:devbox", Ports: []string{"tcp:80"}}}}
	n.addrs["devbox.ts.test:80"] = serveHTTP(t, 200)
	serveCard(t, n, ca, d, 8443, `{"title":"Card","description":"Card description","icon":"card-icon","preview":true,"set":"Set","version":"B"}`, 200, 0)
	c.OverridesPath = writeOverrides(t, "devbox: {label: Parent, description: Parent, icon: parent, group: Parent, order: 9}\n'devbox:8443': {label: Operator, description: Operator description, icon: operator, group: Work, order: -1, temporary: false, url: 'https://link.example/'}\n")
	c.Refresh(t.Context())
	e := deviceEntry(t, c, "devbox:8443")
	if e.Temporary || e.Check != "tls" || e.Label != "Operator" || e.Description != "Operator description" || e.Icon != iconURL("operator") || e.Group != "Work" || e.order != -1 || e.URL != "https://link.example/" || e.Set != "" || e.Version != "" {
		t.Fatalf("endpoint precedence: %+v", e)
	}
	if len(c.admitted) != 1 || deviceEntry(t, c, "devbox").Source != "service" {
		t.Fatal("Service collision suppressed extra port")
	}
	c.OverridesPath = writeOverrides(t, "devbox: {hidden: true}\n'devbox:8443': {hidden: false}\n")
	c.Refresh(t.Context())
	if len(c.admitted) != 1 || len(c.View().Previews) != 1 {
		t.Fatal("hidden Service suppressed extra-port device preview")
	}
	c.Services = &fakeLister{}
	c.Refresh(t.Context())
	if len(c.admitted) != 0 {
		t.Fatal("endpoint undid device parent hidden")
	}
}

func TestPreviewCardFetchFailures(t *testing.T) {
	for _, occasion := range []string{"manual", "return"} {
		for _, result := range []string{"absent", "invalid", "status", "transport", "body"} {
			t.Run(occasion+"/"+result, func(t *testing.T) {
				logs := captureLogs(t)
				d := catalogDevice("node", "devbox", "100.64.0.1")
				c, l, n, ca := deviceCatalog(t, d)
				var mu sync.Mutex
				calls := 0
				response := "valid"
				n.addrs["100.64.0.1:443"] = serveDeviceTLS(t, ca.leaf(t, d.DNSName, false), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					if r.URL.Path != cardPath {
						w.WriteHeader(200)
						return
					}
					calls++
					switch response {
					case "valid":
						fmt.Fprint(w, `{"title":"Old","preview":true}`)
					case "absent":
						w.WriteHeader(404)
					case "invalid":
						fmt.Fprint(w, `{"preview":null}`)
					case "status":
						w.Header().Set("Location", "/")
						w.WriteHeader(302)
					case "transport":
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
					case "body":
						w.Header().Set("Content-Length", "100")
						fmt.Fprint(w, `{"title":`)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					}
				}))
				c.Refresh(t.Context())
				if occasion == "return" {
					l.devices[0].Online = false
					c.Refresh(t.Context())
					l.devices[0].Online = true
				}
				mu.Lock()
				response = result
				mu.Unlock()
				if result == "body" {
					c.DeviceProber.timeout = 250 * time.Millisecond
				}
				c.RefreshManualIfOlder(t.Context(), 0)
				if result == "status" && !strings.Contains(logs.String(), "device devbox card request: HTTP 302") {
					t.Fatalf("HTTP diagnostic missing status: %s", logs.String())
				}
				if (result == "transport" || result == "body") && !strings.Contains(logs.String(), "device devbox card request: HTTPS request failed") {
					t.Fatalf("transport diagnostic unclear: %s", logs.String())
				}
				e := deviceEntry(t, c, "devbox")
				keepPreview := result == "status" || result == "transport" || result == "body"
				wantTitle := "Devbox"
				if keepPreview && occasion == "manual" {
					wantTitle = "Old"
				}
				if e.Temporary != keepPreview || e.Label != wantTitle {
					t.Fatalf("result: %+v", e)
				}
				if keepPreview && (e.Check != "tls" || e.Status != StatusUp) {
					t.Fatalf("preview health changed after failed card request: %+v", e)
				}
				c.Refresh(t.Context())
				mu.Lock()
				defer mu.Unlock()
				if calls != 2 {
					t.Fatalf("card retried on unchanged periodic check: %d", calls)
				}
			})
		}
	}
}

func TestPreviewUnverifiedCardIsNotImported(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	d.Tags = []string{"tag:dev"}
	c, _, n, _ := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	c.InsecureTLSTags = []string{"tag:dev"}
	other := newDeviceCA(t)
	srv := serveCard(t, n, other, d, 8443, `{"title":"Untrusted title","preview":true}`, 200, 0)
	c.Refresh(t.Context())
	e := deviceEntry(t, c, "devbox:8443")
	requests, _ := srv.counts()
	if !e.Unverified || !e.Temporary || e.Label != "Devbox :8443" || requests != 0 {
		t.Fatalf("unverified metadata: %+v, requests=%d", e, requests)
	}
}

func TestCardless443RetainsRootHealth(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	srv := serveCard(t, n, ca, d, 443, "", 404, 503)
	c.Refresh(t.Context())
	c.Refresh(t.Context())
	if e := deviceEntry(t, c, "devbox"); e.Temporary || e.Check != "http" || e.Status != StatusDown || e.Reason != "HTTP 503" {
		t.Fatalf("legacy health: %+v", e)
	}
	cards, roots := srv.counts()
	if cards != 1 || roots != 2 {
		t.Fatalf("root 5xx caused card recovery: cards=%d roots=%d", cards, roots)
	}
}

func TestPermanentExtraPortHasNoPeriodicHTTP(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	c.OverridesPath = writeOverrides(t, "'devbox:8443': {temporary: false}\n")
	srv := serveCard(t, n, ca, d, 8443, "", 404, 0)
	c.Refresh(t.Context())
	c.Refresh(t.Context())
	c.Refresh(t.Context())
	e := deviceEntry(t, c, "devbox:8443")
	requests, _ := srv.counts()
	if e.Temporary || e.Check != "tls" || requests != 1 {
		t.Fatalf("permanent pool health: %+v requests=%d", e, requests)
	}
}

func TestCardDiagnosticsOnlyOnCategoryTransitions(t *testing.T) {
	logs := captureLogs(t)
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	srv := serveCard(t, n, ca, d, 8443, "{invalid", 200, 0)
	c.Refresh(t.Context())
	c.Refresh(t.Context())
	c.RefreshManualIfOlder(t.Context(), 0)
	if strings.Count(logs.String(), "devbox:8443 card request: invalid") != 1 {
		t.Fatalf("repeated category logged: %s", logs.String())
	}
	srv.set(`{}`)
	c.RefreshManualIfOlder(t.Context(), 0)
	srv.set(`{"title":null}`)
	c.RefreshManualIfOlder(t.Context(), 0)
	if strings.Count(logs.String(), "devbox:8443 card request: invalid") != 2 {
		t.Fatalf("valid card did not reset category: %s", logs.String())
	}
	if strings.Contains(logs.String(), `"title"`) {
		t.Fatal("body leaked to diagnostic")
	}
}

func TestCatalogDeviceTupleCursorAcrossSiblingPorts(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, _, _ := deviceCatalog(t, d)
	for port := uint16(8443); port < 8459; port++ {
		c.PreviewPorts = append(c.PreviewPorts, port)
	}
	c.scanBudget = 20 * time.Millisecond
	var mu sync.Mutex
	seen := map[string]int{}
	c.DeviceProber.dial = func(_ context.Context, _ string, stringAddr string) (net.Conn, error) {
		mu.Lock()
		seen[stringAddr]++
		mu.Unlock()
		time.Sleep(200 * time.Millisecond)
		return nil, errors.New("unreachable")
	}
	c.Refresh(t.Context())
	firstPort := c.cursorPort
	c.Refresh(t.Context())
	if c.cursor != d.DNSName || c.cursorPort <= firstPort {
		t.Fatalf("cursor did not advance across sibling ports: %s:%d after %d", c.cursor, c.cursorPort, firstPort)
	}
	mu.Lock()
	for addr, count := range seen {
		if count != 1 {
			t.Errorf("%s probed %d times before wrap", addr, count)
		}
	}
	if len(seen) != 16 {
		t.Errorf("probed %d slots, want 16", len(seen))
	}
	mu.Unlock()
	c.Refresh(t.Context())
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 17 || c.cursorPort >= firstPort {
		t.Fatalf("cursor failed to wrap: %d slots, port %d", len(seen), c.cursorPort)
	}
}

func TestSlowCardConsumesRootCheckDeadline(t *testing.T) {
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	c.DeviceProber.timeout = 100 * time.Millisecond
	n.addrs["100.64.0.1:443"] = serveDeviceTLS(t, ca.leaf(t, d.DNSName, false), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cardPath {
			// Start an object, then consume the rest of the endpoint deadline.
			fmt.Fprint(w, `{"title":`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	c.Refresh(t.Context())
	if len(c.admitted) != 0 || len(c.View().all()) != 0 {
		t.Fatalf("root check admitted an endpoint after its card consumed the deadline: %+v", c.View())
	}
}

func TestPreviewIPv6ExtraPortIsPinned(t *testing.T) {
	d := catalogDevice("node", "devbox", "fd7a:115c:a1e0::1")
	c, _, n, ca := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	n.addrs["[fd7a:115c:a1e0::1]:8443"] = serveDeviceTLS(t, ca.leaf(t, d.DNSName, false), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "devbox.ts.test:8443" || r.TLS.ServerName != d.DNSName || r.URL.Path != cardPath {
			t.Errorf("unexpected IPv6 endpoint request: host=%s SNI=%s path=%s", r.Host, r.TLS.ServerName, r.URL.Path)
		}
		fmt.Fprint(w, `{"title":"IPv6 preview"}`)
	}))
	c.Refresh(t.Context())
	if e := deviceEntry(t, c, "devbox:8443"); e.Label != "IPv6 preview" || e.URL != "https://devbox.ts.test:8443/" {
		t.Fatalf("IPv6 extra port: %+v", e)
	}
}

func TestNonObjectCardClearsMetadataWithoutDiagnostic(t *testing.T) {
	logs := captureLogs(t)
	d := catalogDevice("node", "devbox", "100.64.0.1")
	c, _, n, ca := deviceCatalog(t, d)
	c.PreviewPorts = []uint16{8443}
	srv := serveCard(t, n, ca, d, 8443, `{"title":"Old preview","preview":true}`, 200, 0)
	c.Refresh(t.Context())
	srv.set(" \t\n<!doctype html><title>App catch-all</title>")
	c.RefreshManualIfOlder(t.Context(), 0)
	e := deviceEntry(t, c, "devbox:8443")
	if e.Label != "Devbox :8443" || strings.Contains(logs.String(), "card request:") {
		t.Fatalf("HTML fallback was not quiet absence: %+v logs=%s", e, logs.String())
	}
	if state := c.endpoints[endpointID{d.ID, 8443}]; state.card != nil || state.priorPreview {
		t.Fatalf("HTML fallback retained card classification: %+v", state)
	}
}

func TestManualUnverifiedListenerClearsImportedCard(t *testing.T) {
	logs := captureLogs(t)
	d := catalogDevice("node", "devbox", "100.64.0.1")
	d.Tags = []string{"tag:dev"}
	c, _, n, ca := deviceCatalog(t, d)
	c.InsecureTLSTags = []string{"tag:dev"}
	serveCard(t, n, ca, d, 443, `{"title":"Trusted title","preview":true}`, 200, 0)
	c.Refresh(t.Context())
	other := newDeviceCA(t)
	n.addrs["100.64.0.1:443"] = serveDeviceTLS(t, other.leaf(t, d.DNSName, false), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unverified listener received HTTP request")
	}))
	c.RefreshManualIfOlder(t.Context(), 0)
	e := deviceEntry(t, c, "devbox")
	if !e.Unverified || !e.Temporary || e.Label != "Devbox" || strings.Contains(logs.String(), "card:") {
		t.Fatalf("unverified manual metadata: %+v logs=%s", e, logs.String())
	}
}

func TestPreviewHintDisabledWithoutDeviceDiscovery(t *testing.T) {
	c := newCatalog(&fakeLister{}, &fakeNet{}, "")
	c.PreviewPorts = []uint16{8443}
	c.Refresh(t.Context())
	if c.View().PreviewEnabled {
		t.Fatal("preview hint enabled when discovery is off")
	}
}
