package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tailcfg/nodecap"
)

type resumeReadyListener struct {
	net.Listener
	once  sync.Once
	ready chan struct{}
}

func (l *resumeReadyListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.ready) })
	return l.Listener.Accept()
}

type resumeServingNode struct {
	listener         net.Listener
	client           *local.Client
	status           *ipnstate.Status
	loggedIn         atomic.Bool
	listened, closed atomic.Int32
}

func (n *resumeServingNode) Up(context.Context) (*ipnstate.Status, error) {
	if !n.loggedIn.Load() {
		return &ipnstate.Status{BackendState: "NeedsLogin"}, nil
	}
	return n.status, nil
}
func (n *resumeServingNode) LocalClient() (*local.Client, error) { return n.client, nil }
func (n *resumeServingNode) ListenFunnel() (net.Listener, error) {
	n.listened.Add(1)
	return n.listener, nil
}
func (n *resumeServingNode) Close() error { n.closed.Add(1); _ = n.listener.Close(); return nil }
func (n *resumeServingNode) Dial(context.Context, string, string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		if _, err := http.ReadRequest(bufio.NewReader(server)); err == nil {
			_ = proxyTestResponse(200, nil, "resumed app").Write(server)
		}
	}()
	return client, nil
}
func TestShareResumeRestoresListenerAndIdentity(t *testing.T) {
	for _, garbageCollected := range []bool{false, true} {
		t.Run(map[bool]string{false: "cached", true: "garbage collected"}[garbageCollected], func(t *testing.T) {
			root := t.TempDir()
			store, err := openShareStore(root)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			expiry := now.Add(time.Hour)
			id := randomShareID()
			hostname := "public-" + id
			host := hostname + ".example.ts.net"
			entry := Entry{Name: "app", Label: "App", Source: "service", Host: "app.example.ts.net", URL: "http://app.example.ts.net:8443/", Ports: []string{"tcp:8443"}}
			target := targetForEntry(entry, "example.ts.net", "svc:home")
			record := &ShareRecord{ID: id, Hostname: hostname, TargetID: target.Tile.TargetID, Revision: "retained-revision", State: "public", Binding: target.Binding, CreatedAt: now.Add(-time.Hour), NextReviewAt: now.Add(29 * 24 * time.Hour), ExpiresAt: &expiry}
			// Target IDs may change format without changing the saved binding or consent.
			record.TargetID = "previous-target-format"
			if err = store.Save(map[string]*ShareRecord{id: record}); err != nil {
				t.Fatal(err)
			}

			dir := filepath.Join(root, "shares", "nodes", id)
			if err = os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			key := []byte(`{"_current-profile":"retained-profile"}`)
			if err = os.WriteFile(filepath.Join(dir, "tailscaled.state"), key, 0600); err != nil {
				t.Fatal(err)
			}
			ca := newDeviceCA(t)
			socket, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			listener := &resumeReadyListener{Listener: tls.NewListener(socket, &tls.Config{Certificates: []tls.Certificate{ca.leaf(t, host, false)}}), ready: make(chan struct{})}
			defer listener.Close()
			status := shareEnrollmentFixture()
			status.Self.DNSName = host + "."
			status.CertDomains = []string{host}
			status.Self.CapMap = tailcfg.NodeCapMap{nodecap.HTTPS: nil, nodecap.Funnel: nil, tailcfg.NodeCapability(string(nodecap.FunnelPorts) + "?ports=443"): nil}
			node := &resumeServingNode{listener: listener, status: status}
			node.loggedIn.Store(!garbageCollected)
			var minted, logouts atomic.Int32
			node.client = &local.Client{Transport: shareLocalTransport(func(request *http.Request) (*http.Response, error) {
				body := "{}"
				code := 200
				switch {
				case strings.HasSuffix(request.URL.Path, "/start"):
					code = 204
				case strings.HasSuffix(request.URL.Path, "/login-interactive"):
					node.loggedIn.Store(true)
					code = 204
				case strings.HasSuffix(request.URL.Path, "/status"):
					data, _ := json.Marshal(status)
					body = string(data)
				case strings.HasSuffix(request.URL.Path, "/services"):
					data, _ := json.Marshal(map[tailcfg.ServiceName]tailcfg.ServiceDetails{"svc:app": {Name: "svc:app", Addrs: []netip.Addr{netip.MustParseAddr("100.100.1.1")}, Ports: []tailcfg.ProtoPortRange{{Proto: 6, Ports: tailcfg.PortRange{First: 8443, Last: 8443}}}}})
					body = string(data)
				case strings.HasSuffix(request.URL.Path, "/tka/status"):
					body = `{"Enabled":false}`
				case strings.Contains(request.URL.Path, "/cert/"):
					body = "-----BEGIN PRIVATE KEY-----\ntest\n-----END PRIVATE KEY-----\n-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n"
				case strings.HasSuffix(request.URL.Path, "/logout"):
					logouts.Add(1)
					code = 204
				default:
					t.Errorf("unexpected LocalAPI route %s", request.URL.Path)
				}
				return &http.Response{StatusCode: code, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			manager, err := newShareManager(root, true, func(r ShareRecord) (shareRuntime, error) {
				runtime := newShareRuntime(r.Binding, r.Hostname, dir, "example.ts.net", "home", func(context.Context) (string, error) { minted.Add(1); return "replacement-test-key", nil }).(*nodeRuntime)
				runtime.newNode = func(string) shareNode { return node }
				return runtime, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			manager.catalogUpdated(View{Updated: now, Groups: []Group{{Entries: []Entry{entry}}}}, "example.ts.net", "svc:home")
			select {
			case <-listener.ready:
			case <-time.After(2 * time.Second):
				t.Fatal("resume never served its listener")
			}
			transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: ca.roots, ServerName: host}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, socket.Addr().String())
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 2 * time.Second}
			response, err := client.Get("https://" + host + "/")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || string(body) != "resumed app" {
				t.Fatal("retained share did not serve its app")
			}
			resumed := manager.snapshot(true).Records[0]
			if !resumed.ExpiresAt.Equal(expiry) || resumed.Hostname != hostname || node.listened.Load() != 1 {
				t.Fatal("resume replaced or extended approval")
			}
			wantMints := int32(0)
			if garbageCollected {
				wantMints = 1
			}
			if minted.Load() != wantMints {
				t.Fatal("unexpected reenrollment")
			}
			if garbageCollected {
				if err = manager.stop(id, resumed.Revision, "admin", "admin_stop"); err != nil {
					t.Fatal(err)
				}
				manager.workers.Wait()
				if logouts.Load() != 1 || node.closed.Load() != 1 {
					t.Fatal("terminal Stop did not remove identity")
				}
			} else {
				manager.Close()
				if logouts.Load() != 0 || node.closed.Load() != 0 {
					t.Fatal("orderly shutdown discarded live identity")
				}
				if saved, err := os.ReadFile(filepath.Join(dir, "tailscaled.state")); err != nil || string(saved) != string(key) {
					t.Fatal("node key changed")
				}
				if response, err := client.Get("https://" + host + "/"); err == nil {
					response.Body.Close()
					t.Fatal("closed share still served")
				}
			}
		})
	}
}
