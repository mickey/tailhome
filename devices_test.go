package main

import (
	"bytes"
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
	old := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(old) })
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
				if c.admitted[d.ID].IP != l.devices[0].IP {
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
			if !tagged {
				if !strings.Contains(logs.String(), "tag it and add the tag") {
					t.Fatalf("missing tag advice: %s", logs.String())
				}
			}
			l.devices[0].Tags = append(l.devices[0].Tags, "tag:nas")
			c.Refresh(t.Context())
			if e := deviceEntry(t, c, "nas"); e.Description != "Unverified HTTPS" || e.Status != StatusUp {
				t.Fatalf("exception: %+v", e)
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
		if e := deviceEntry(t, c, "app"); e.Description != step.description || e.Status != StatusUp {
			t.Fatalf("certificate description: %+v, want %q", e, step.description)
		}
		if n.dials.Load() != before+1 {
			t.Fatalf("probe used %d dials, want 1", n.dials.Load()-before)
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
	l.err = errors.New("netmap unavailable")
	l.devices = nil
	delete(n.addrs, "100.64.0.1:443")
	c.Refresh(t.Context())
	c.Refresh(t.Context())
	if e := deviceEntry(t, c, "app"); e.Status != StatusDown {
		t.Fatalf("recorded device not probed: %+v", e)
	}
	if e := deviceEntry(t, c, "web"); e.Status != StatusUp || c.View().Error != "" {
		t.Fatalf("Service failure isolation: %+v, %s", e, c.View().Error)
	}
	if len(c.admitted) != 1 || strings.Count(logs.String(), "devices: listing failed:") != 1 {
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
	c.admitted = map[tailcfg.StableNodeID]Device{}
	for _, d := range devices {
		c.admitted[d.ID] = d
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
