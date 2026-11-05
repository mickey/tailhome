package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tailscale.com/client/local"
	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/types/key"
	"tailscale.com/types/views"
)

func shareEnrollmentFixture() *ipnstate.Status {
	tags := views.SliceOf([]string{shareTag})
	return &ipnstate.Status{
		BackendState:   "Running",
		Self:           &ipnstate.PeerStatus{ID: "share-node", DNSName: "public-example.example.ts.net.", Tags: &tags},
		CurrentTailnet: &ipnstate.TailnetStatus{MagicDNSSuffix: "example.ts.net", MagicDNSEnabled: true},
		CertDomains:    []string{"public-example.example.ts.net"},
	}
}

func TestShareEnrollmentIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ipnstate.Status)
	}{
		{"collision suffix", func(s *ipnstate.Status) { s.Self.DNSName = "public-example-1.example.ts.net." }},
		{"wrong tailnet", func(s *ipnstate.Status) { s.CurrentTailnet.MagicDNSSuffix = "other.ts.net" }},
		{"no MagicDNS", func(s *ipnstate.Status) { s.CurrentTailnet.MagicDNSEnabled = false }},
		{"no certificates", func(s *ipnstate.Status) { s.CertDomains = nil }},
		{"untagged", func(s *ipnstate.Status) { s.Self.Tags = nil }},
		{"additional authority", func(s *ipnstate.Status) {
			tags := views.SliceOf([]string{shareTag, "tag:admin"})
			s.Self.Tags = &tags
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := shareEnrollmentFixture()
			tc.change(s)
			if _, err := validateShareEnrollment(s, "public-example", "example.ts.net"); err == nil {
				t.Fatal("accepted changed enrollment")
			}
		})
	}
	if host, err := validateShareEnrollment(shareEnrollmentFixture(), "public-example", "example.ts.net"); err != nil || host != "public-example.example.ts.net" {
		t.Fatalf("valid enrollment: %q %v", host, err)
	}
}

func TestShareServiceUsesAuthoritativeAddrsAndPorts(t *testing.T) {
	b := ShareBinding{Kind: "service", Service: "svc:photos", FQDN: "photos.example.ts.net", Port: 443, Scheme: "https", BasePath: "/"}
	svc := tailcfg.ServiceDetails{Name: "svc:photos", Addrs: []netip.Addr{netip.MustParseAddr("100.100.1.1")}, Ports: []tailcfg.ProtoPortRange{{Proto: 6, Ports: tailcfg.PortRange{First: 443, Last: 443}}}}
	services := map[tailcfg.ServiceName]tailcfg.ServiceDetails{"svc:photos": svc}
	if a, err := resolveShareService(b, services, "example.ts.net"); err != nil || a[0] != svc.Addrs[0] {
		t.Fatalf("resolve: %v %v", a, err)
	}
	svc.Addrs = []netip.Addr{netip.MustParseAddr("100.100.1.2")}
	services[svc.Name] = svc
	if a, err := resolveShareService(b, services, "example.ts.net"); err != nil || a[0] != svc.Addrs[0] {
		t.Fatalf("service movement: %v %v", a, err)
	}
	svc.Ports = nil
	services[svc.Name] = svc
	if _, err := resolveShareService(b, services, "example.ts.net"); err == nil {
		t.Fatal("accepted unadvertised port")
	}
}

func TestShareDevicePinsStableIdentity(t *testing.T) {
	b := ShareBinding{Kind: "device", NodeID: "device-1", FQDN: "app.example.ts.net", Port: 8443}
	s := shareEnrollmentFixture()
	peer := &ipnstate.PeerStatus{ID: "device-1", DNSName: "app.example.ts.net.", TailscaleIPs: []netip.Addr{netip.MustParseAddr("100.64.2.1")}}
	s.Peer = map[key.NodePublic]*ipnstate.PeerStatus{{}: peer}
	if _, err := resolveShareDevice(b, s, "home"); err != nil {
		t.Fatal(err)
	}
	peer.ID = "replacement"
	if _, err := resolveShareDevice(b, s, "home"); err == nil {
		t.Fatal("accepted replacement device")
	}
	peer.ID = "device-1"
	peer.DNSName = "renamed.example.ts.net."
	if _, err := resolveShareDevice(b, s, "home"); err == nil {
		t.Fatal("accepted renamed device")
	}
	peer.DNSName = "app.example.ts.net."
	if _, err := resolveShareDevice(b, s, "device-1"); err == nil {
		t.Fatal("accepted home node")
	}
	tags := views.SliceOf([]string{"tag:tailhome"})
	peer.Tags = &tags
	if _, err := resolveShareDevice(b, s, "home"); err != nil {
		t.Fatal("rejected an application solely for its home-style tag:", err)
	}
	tags = views.SliceOf([]string{shareTag})
	peer.Tags = &tags
	if _, err := resolveShareDevice(b, s, "home"); err == nil {
		t.Fatal("accepted another share node")
	}
}

type shareCleanupNode struct {
	up, listen, close int
	lc                *local.Client
	upFunc            func(context.Context) (*ipnstate.Status, error)
}

func (n *shareCleanupNode) Up(ctx context.Context) (*ipnstate.Status, error) {
	n.up++
	if n.upFunc != nil {
		return n.upFunc(ctx)
	}
	return shareEnrollmentFixture(), nil
}
func (n *shareCleanupNode) LocalClient() (*local.Client, error) { return n.lc, nil }
func (n *shareCleanupNode) ListenFunnel() (net.Listener, error) {
	n.listen++
	return nil, shareErr("unexpected_listener")
}
func (n *shareCleanupNode) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, shareErr("unexpected_dial")
}
func (n *shareCleanupNode) Close() error { n.close++; return nil }

type shareLocalTransport func(*http.Request) (*http.Response, error)

func (f shareLocalTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestShareCleanupNeverMintsOrListens(t *testing.T) {
	for _, cached := range []bool{false, true} {
		dir := t.TempDir()
		if cached {
			if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte(`{"_current-profile":"cHJvZmlsZS0x"}`), 0600); err != nil {
				t.Fatal(err)
			}
		}
		minted, loggedOut := 0, 0
		r := newShareRuntime(ShareBinding{}, "public-example", dir, "example.ts.net", "home", func(context.Context) (string, error) { minted++; return "bad", nil }).(*nodeRuntime)
		n := &shareCleanupNode{lc: &local.Client{Transport: shareLocalTransport(func(req *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(req.URL.Path, "/logout") {
				t.Errorf("unexpected LocalAPI request: %s", req.URL.Path)
			}
			loggedOut++
			return &http.Response{StatusCode: 204, Body: http.NoBody, Header: make(http.Header)}, nil
		})}}
		r.newNode = func(authKey string) shareNode {
			if authKey == "" {
				t.Error("ambient auth permitted")
			}
			return n
		}
		if err := r.Logout(t.Context()); err != nil {
			t.Fatal(err)
		}
		if minted != 0 || n.listen != 0 {
			t.Fatal("cleanup provisioned public node")
		}
		if cached && (n.up != 1 || loggedOut != 1) {
			t.Fatal("cached node not logged out")
		}
		if !cached && (n.up != 0 || loggedOut != 0) {
			t.Fatal("absent node started")
		}
	}
}

func TestShareAuthKeyCapabilities(t *testing.T) {
	for _, variant := range []string{"valid", "persistent", "reusable", "wrong-tag", "unapproved"} {
		t.Run(variant, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(req.URL.Path, "/oauth/token") {
					w.Write([]byte(`{"access_token":"test","token_type":"Bearer","expires_in":3600}`))
					return
				}
				var requested tsapi.CreateKeyRequest
				if err := json.NewDecoder(req.Body).Decode(&requested); err != nil {
					t.Error(err)
				}
				caps := requested.Capabilities.Devices.Create
				if requested.ExpirySeconds != 600 || !caps.Ephemeral || caps.Reusable || !caps.Preauthorized || len(caps.Tags) != 1 || caps.Tags[0] != shareTag {
					t.Errorf("unsafe request: %+v", requested)
				}
				switch variant {
				case "persistent":
					requested.Capabilities.Devices.Create.Ephemeral = false
				case "reusable":
					requested.Capabilities.Devices.Create.Reusable = true
				case "wrong-tag":
					requested.Capabilities.Devices.Create.Tags = []string{"tag:other"}
				case "unapproved":
					requested.Capabilities.Devices.Create.Preauthorized = false
				}
				json.NewEncoder(w).Encode(tsapi.Key{Key: "test-key", Capabilities: requested.Capabilities})
			}))
			defer srv.Close()
			base, _ := url.Parse(srv.URL)
			api := &tsapi.Client{BaseURL: base, Auth: oauthClient{ctx: t.Context(), id: "id", secret: "secret", timeout: time.Second}}
			secret, err := mintAuthKey(t.Context(), api, shareTag, time.Second, true)
			if variant == "valid" {
				if err != nil || secret != "test-key" {
					t.Fatalf("valid key: %q %v", secret, err)
				}
			} else if secret != "" || shareCode(err) != "key_capabilities_invalid" {
				t.Fatalf("accepted unsafe key capabilities: secret=%q code=%q", secret, shareCode(err))
			}
		})
	}
}

// fakeLocalAPI exercises the real LocalAPI client without a running node.
type fakeLocalAPI struct {
	t                                          *testing.T
	state                                      string
	started, loggedIn, loggedOut, statusChecks int
}

func (f *fakeLocalAPI) client() *local.Client {
	return &local.Client{Transport: shareLocalTransport(func(req *http.Request) (*http.Response, error) {
		body, code := "", http.StatusNoContent
		switch {
		case strings.HasSuffix(req.URL.Path, "/status"):
			if req.Context().Err() != nil {
				f.t.Error("status check inherited expired Up context")
			}
			f.statusChecks++
			body, code = `{"BackendState":"`+f.state+`"}`, http.StatusOK
		case strings.HasSuffix(req.URL.Path, "/start"):
			var opts ipn.Options
			if err := json.NewDecoder(req.Body).Decode(&opts); err != nil || opts.AuthKey != "replacement-key" {
				f.t.Errorf("reauthentication options: %+v, %v", opts, err)
			}
			f.started++
		case strings.HasSuffix(req.URL.Path, "/login-interactive"):
			f.loggedIn++
		case strings.HasSuffix(req.URL.Path, "/logout"):
			f.loggedOut++
		default:
			f.t.Errorf("unexpected LocalAPI request: %s", req.URL.Path)
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
}

func TestShareCachedNodeLifecycle(t *testing.T) {
	for _, mode := range []string{"reenroll", "transient", "cleanup_gc"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			statePath := filepath.Join(dir, "tailscaled.state")
			state := []byte(`{"_current-profile":"cHJvZmlsZS0x"}`)
			if err := os.WriteFile(statePath, state, 0600); err != nil {
				t.Fatal(err)
			}
			certPath := filepath.Join(dir, "cached-cert")
			if err := os.WriteFile(certPath, []byte("preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			minted, constructed := 0, 0
			r := newShareRuntime(ShareBinding{}, "public-example", dir, "example.ts.net", "home", func(ctx context.Context) (string, error) {
				if mode != "reenroll" || ctx.Err() != nil {
					t.Error("minted without a viable NeedsLogin reenrollment")
				}
				minted++
				return "replacement-key", nil
			}).(*nodeRuntime)
			api := &fakeLocalAPI{t: t, state: "NeedsLogin"}
			if mode == "transient" {
				api.state = "Starting"
			}
			n := &shareCleanupNode{lc: api.client()}
			n.upFunc = func(ctx context.Context) (*ipnstate.Status, error) {
				if mode == "cleanup_gc" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if n.up == 1 || mode == "transient" {
					return nil, context.DeadlineExceeded
				}
				if api.started != 1 || api.loggedIn != 1 {
					t.Error("cached profile not explicitly reauthenticated")
				}
				return shareEnrollmentFixture(), nil
			}
			r.newNode = func(authKey string) shareNode {
				constructed++
				if authKey == "" || authKey == "replacement-key" {
					t.Error("cached startup permitted ambient auth or minted a new key")
				}
				return n
			}
			if mode == "cleanup_gc" {
				cleanupGCShareThroughManager(t, r)
				if minted != 0 || api.started != 0 || api.loggedIn != 0 || api.loggedOut != 0 || api.statusChecks != 1 || n.listen != 0 {
					t.Fatal("GC cleanup mutated the already deleted identity")
				}
				return
			}
			// No HTTPS capability: reaching dns_missing proves the recovered
			// identity was Running and passed enrollment validation.
			_, _, _, err := r.Prepare(t.Context(), nil)
			wantCode, wantMint := "dns_missing", 1
			if mode == "transient" {
				wantCode, wantMint = "setup_failed", 0
				if err := r.Logout(t.Context()); shareCode(err) != "cleanup_pending" {
					t.Fatalf("unconfirmed deletion treated as complete: %v", err)
				}
			}
			if shareCode(err) != wantCode || minted != wantMint || constructed != 1 || n.close != 0 {
				t.Fatalf("resume: err=%v minted=%d constructed=%d close=%d", err, minted, constructed, n.close)
			}
			if r.dir != dir || r.hostname != "public-example" {
				t.Fatal("resume changed the share identity")
			}
			if got, err := os.ReadFile(statePath); err != nil || string(got) != string(state) {
				t.Fatalf("cached profile changed: %q, %v", got, err)
			}
			if got, err := os.ReadFile(certPath); err != nil || string(got) != "preserved" {
				t.Fatalf("certificate cache changed: %q, %v", got, err)
			}
		})
	}
}
func cleanupGCShareThroughManager(t *testing.T, r *nodeRuntime) {
	t.Helper()
	r.cleanupUpTimeout = time.Millisecond
	m, err := newShareManager(t.TempDir(), true, func(ShareRecord) (shareRuntime, error) { return r, nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	record := &ShareRecord{ID: "0123456789abcdef", Hostname: r.hostname, State: "expired", Reason: "expired", CleanupPending: true}
	m.mu.Lock()
	m.records[record.ID] = record
	m.startCleanupLocked(record)
	m.mu.Unlock()
	m.workers.Wait()
	if got := m.snapshot(true).Records; len(got) != 0 {
		t.Fatalf("already deleted identity remains pending: %+v", got)
	}
	if !r.closed {
		t.Fatal("manager did not close the cleanup runtime")
	}
}

func TestShareNodeDialUsesGatewayPinnedLiteral(t *testing.T) {
	n := &shareCleanupNode{lc: &local.Client{Transport: shareLocalTransport(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("dial repeated LocalAPI resolution: %s", req.URL.Path)
		return nil, context.Canceled
	})}}
	r := &nodeRuntime{binding: ShareBinding{Port: 8443}, node: n}
	for _, tc := range []struct {
		network, address string
		want             string
	}{
		{"tcp", "100.64.2.1:8443", "unexpected_dial"},
		{"tcp", "[fd7a:115c:a1e0::1]:8443", "unexpected_dial"},
		{"udp", "100.64.2.1:8443", "target_changed"},
		{"tcp", "app.example.ts.net:8443", "target_changed"},
		{"tcp", "100.64.2.1:443", "target_changed"},
		{"tcp", "127.0.0.1:8443", "target_changed"},
		{"tcp", "[::ffff:100.64.2.1]:8443", "target_changed"},
	} {
		if _, err := r.Dial(t.Context(), tc.network, tc.address); shareCode(err) != tc.want {
			t.Errorf("Dial(%q, %q) = %v, want %v", tc.network, tc.address, err, tc.want)
		}
	}
}
