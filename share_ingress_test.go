package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/netip"
	"testing"
	"time"

	"tailscale.com/ipn"
)

type shareAddressedConn struct {
	net.Conn
	address net.Addr
}

func (c *shareAddressedConn) RemoteAddr() net.Addr { return c.address }

func TestShareBothIngressPathsCloseTogether(t *testing.T) {
	for _, ending := range []string{"stop", "expiry"} {
		t.Run(ending, func(t *testing.T) {
			m, runtime, target := fakeShareManager(t)
			var expire func()
			m.afterFunc = func(_ time.Duration, f func()) *time.Timer { expire = f; return time.NewTimer(24 * time.Hour) }
			target.Binding = proxyTestBinding()
			m.targets[target.Tile.TargetID] = target
			if err := m.create(target.Tile.TargetID, target.Tile.Revision, "24h", "dual-ingress", "admin"); err != nil {
				t.Fatal(err)
			}
			<-runtime.started
			record := m.snapshot(true).Records[0]
			requests := make(chan *http.Request, 2)
			dial := func(context.Context, string, string) (net.Conn, error) {
				client, app := net.Pipe()
				go func() {
					defer app.Close()
					request, err := http.ReadRequest(bufio.NewReader(app))
					if err != nil {
						return
					}
					requests <- request
					_, _ = io.WriteString(app, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\nd\r\ndata: hello\n\n\r\n")
					_, _ = io.Copy(io.Discard, app)
				}()
				return client, nil
			}
			gateway, err := newShareGateway(target.Binding, "public-abc.example.ts.net", dial, func(context.Context) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("100.101.102.103")}, nil
			}, record.ExpiresAt)
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.Close()
			gateway.Open()
			m.mu.Lock()
			m.active[record.ID].gateway = gateway
			m.mu.Unlock()
			ca := newDeviceCA(t)
			certificate := ca.leaf(t, "public-abc.example.ts.net", false)
			source := &proxyQueuedListener{queue: make(chan net.Conn, 2), done: make(chan struct{})}
			listener := tls.NewListener(source, &tls.Config{Certificates: []tls.Certificate{certificate}})
			served := make(chan struct{})
			go func() { defer close(served); _ = gateway.Serve(listener) }()
			disconnected := make(chan error, 2)
			for _, ingress := range []string{"funnel", "tailnet"} {
				server, client := net.Pipe()
				var raw net.Conn = server
				if ingress == "funnel" {
					raw = &ipn.FunnelConn{Conn: server, Src: netip.MustParseAddrPort("203.0.113.8:1234")}
				} else {
					raw = &shareAddressedConn{Conn: server, address: &net.TCPAddr{IP: net.ParseIP("100.64.0.42"), Port: 1234}}
				}
				source.queue <- raw
				visitor := tls.Client(client, &tls.Config{RootCAs: ca.roots, ServerName: "public-abc.example.ts.net"})
				defer visitor.Close()
				_ = visitor.SetDeadline(time.Now().Add(3 * time.Second))
				request, _ := http.NewRequest("GET", "https://public-abc.example.ts.net/mount/share/status", nil)
				request.Header.Set("Tailscale-User-Login", "owner@example.com")
				request.Header.Set("X-Forwarded-For", "forged")
				if err = request.Write(visitor); err != nil {
					t.Fatal(err)
				}
				response, err := http.ReadResponse(bufio.NewReader(visitor), request)
				if err != nil {
					t.Fatal(err)
				}
				body := make([]byte, 13)
				if _, err = io.ReadFull(response.Body, body); err != nil || response.StatusCode != 200 || string(body) != "data: hello\n\n" {
					t.Fatalf("%s not admitted to app: status=%d body=%q err=%v", ingress, response.StatusCode, body, err)
				}
				upstream := <-requests
				if upstream.Header.Get("Tailscale-User-Login") != "" {
					t.Fatal("tailnet identity header forwarded")
				}
				expectedIP := ""
				if ingress == "funnel" {
					expectedIP = "203.0.113.8"
				}
				if upstream.Header.Get("X-Forwarded-For") != expectedIP {
					t.Fatalf("%s visitor metadata leaked: %v", ingress, upstream.Header)
				}
				if upstream.URL.Path != "/mount/share/status" {
					t.Fatal("share node did not proxy app-owned path")
				}
				go func() { _, err := response.Body.Read(make([]byte, 1)); disconnected <- err }()
			}
			gateway.mu.Lock()
			if gateway.requests != 2 || gateway.upstreamConnections != 2 {
				t.Error("ingress paths did not share request/upstream accounting")
			}
			public := 0
			for _, isPublic := range gateway.connections {
				if isPublic {
					public++
				}
			}
			gateway.mu.Unlock()
			if public != 2 {
				t.Fatalf("public socket count=%d", public)
			}
			if ending == "expiry" {
				expire()
			} else if err = m.stop(record.ID, record.Revision, "admin", "admin_stop"); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				select {
				case err := <-disconnected:
					if err == nil {
						t.Fatal("stream survived shutdown")
					}
				case <-time.After(time.Second):
					t.Fatal("one ingress stream stayed open")
				}
			}
			select {
			case <-served:
			case <-time.After(time.Second):
				t.Fatal("combined listener stayed open")
			}
			m.workers.Wait()
			assertShareRetired(t, m, record.ID)
		})
	}
}
