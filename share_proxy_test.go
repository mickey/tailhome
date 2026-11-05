package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func proxyTestBinding() ShareBinding {
	return ShareBinding{Kind: "device", NodeID: "node", FQDN: "app.example.ts.net", Port: 8443, Scheme: "http", BasePath: "/mount"}
}

func proxyTestGateway(t *testing.T, binding ShareBinding, upstream func(*http.Request) *http.Response) *shareGateway {
	t.Helper()
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != net.JoinHostPort("100.101.102.103", strconv.Itoa(int(binding.Port))) {
			t.Errorf("unpinned dial: %q %q", network, address)
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			req, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			if req.Body != nil {
				defer req.Body.Close()
			}
			response := upstream(req)
			response.Close = true
			_ = response.Write(server)
		}()
		return client, nil
	}
	g, err := newShareGateway(binding, "public-abc.example.ts.net", dial, func(context.Context) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("100.101.102.103")}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	if !g.Open() {
		t.Fatal("admission did not open")
	}
	return g
}

func proxyTestResponse(status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), ProtoMajor: 1, ProtoMinor: 1}
}
func proxyTestRequest(target string) *http.Request {
	r := httptest.NewRequest("GET", target, nil)
	r.Host = "public-abc.example.ts.net"
	r.TLS = &tls.ConnectionState{ServerName: r.Host}
	return r
}

func TestShareGatewayPinnedRewrite(t *testing.T) {
	g := proxyTestGateway(t, proxyTestBinding(), func(r *http.Request) *http.Response {
		if r.Host != "app.example.ts.net:8443" || r.URL.RequestURI() != "/mount/a%20b?q=one%2Ftwo" {
			t.Errorf("outbound authority/path: %s %s", r.Host, r.URL.RequestURI())
		}
		for _, name := range []string{"Forwarded", "X-Real-IP", "Tailscale-User-Login", "X-Tailscale-User", "Remote-User", "X-Remote-User", "X-Auth-Request-User", "X-WEBAUTH-USER", "X-Authentik-Uid", "X-Original-URL", "X-Rewrite-URL", "True-Client-IP", "CF-Connecting-IP"} {
			if r.Header.Get(name) != "" {
				t.Errorf("identity header retained: %s", name)
			}
		}
		if r.Header.Get("X-Forwarded-For") != "203.0.113.4" || r.Header.Get("X-Forwarded-Host") != "public-abc.example.ts.net" || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Port") != "443" {
			t.Errorf("forwarded headers: %v", r.Header)
		}
		for _, name := range []string{"Authorization", "Cookie", "Origin", "Referer"} {
			if r.Header.Get(name) != "preserved" {
				t.Errorf("app header removed: %s", name)
			}
		}
		return proxyTestResponse(200, http.Header{"Set-Cookie": {"sid=123; Domain=.example.ts.net; Secure; HttpOnly", "other=ok; dOmAiN=example.ts.net; Path=/"}, "Content-Security-Policy": {"default-src 'self'"}}, "ok")
	})
	r := proxyTestRequest("/mount/a%20b?q=one%2Ftwo")
	for _, name := range []string{"Forwarded", "X-Real-IP", "Tailscale-User-Login", "X-Tailscale-User", "Remote-User", "X-Remote-User", "X-Auth-Request-User", "X-WEBAUTH-USER", "X-Authentik-Uid", "X-Original-URL", "X-Rewrite-URL", "True-Client-IP", "CF-Connecting-IP", "X-Forwarded-For"} {
		r.Header.Set(name, "spoofed")
	}
	for _, name := range []string{"Authorization", "Cookie", "Origin", "Referer"} {
		r.Header.Set(name, "preserved")
	}
	r = r.WithContext(context.WithValue(r.Context(), shareVisitorKey{}, netip.MustParseAddr("203.0.113.4")))
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("response: %d %q", w.Code, w.Body.String())
	}
	for _, cookie := range w.Header().Values("Set-Cookie") {
		if strings.Contains(strings.ToLower(cookie), "domain") {
			t.Errorf("domain retained: %q", cookie)
		}
	}
	if len(w.Header().Values("Set-Cookie")) != 2 || w.Header().Get("Content-Security-Policy") != "default-src 'self'" {
		t.Fatal("cookies or security policy lost")
	}
}

func TestShareGatewayRequestBoundary(t *testing.T) {
	g := proxyTestGateway(t, proxyTestBinding(), func(r *http.Request) *http.Response { return proxyTestResponse(200, nil, "app") })
	cases := []struct {
		name  string
		alter func(*http.Request)
	}{
		{"foreign host", func(r *http.Request) { r.Host = "home.example.ts.net" }},
		{"foreign sni", func(r *http.Request) { r.TLS.ServerName = "home.example.ts.net" }},
		{"missing tls", func(r *http.Request) { r.TLS = nil }},
		{"connect", func(r *http.Request) { r.Method = "CONNECT" }},
		{"absolute form", func(r *http.Request) { r.RequestURI = "https://public-abc.example.ts.net/a" }},
		{"url authority", func(r *http.Request) { r.URL.Host = "private.example.ts.net" }},
		{"non-websocket upgrade", func(r *http.Request) { r.Header.Set("Upgrade", "h2c") }},
		{"multiple upgrades", func(r *http.Request) { r.Header.Set("Upgrade", "websocket, h2c") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := proxyTestRequest("/a")
			tc.alter(r)
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, r)
			if w.Code != 400 || w.Body.String() != "Invalid request.\n" {
				t.Fatalf("response: %d %q", w.Code, w.Body.String())
			}
		})
	}
	for _, p := range []string{"/../secret", "/%2fsecret", "/%252e%252e/secret", "/a;parameter", "/%ff/secret"} {
		t.Run(p, func(t *testing.T) {
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, proxyTestRequest(p))
			if w.Code != 400 {
				t.Fatalf("unsafe path accepted: %d", w.Code)
			}
		})
	}
	for _, p := range []string{"/safe/path", "/caf%C3%A9", "/a%20b?parameter=..;%00"} {
		if !g.validRequest(proxyTestRequest(p)) {
			t.Errorf("safe path rejected: %q", p)
		}
	}
	// Management-looking routes are app-owned, not a route into the home mux.
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, proxyTestRequest("/mount/api/shares"))
	if w.Code != 200 || w.Body.String() != "app" {
		t.Fatal("app route reserved")
	}
	binding := proxyTestBinding()
	binding.BasePath = "/"
	root := proxyTestGateway(t, binding, func(*http.Request) *http.Response { return proxyTestResponse(200, nil, "app") })
	if !root.validRequest(proxyTestRequest("/a;parameter")) {
		t.Fatal("root mount unexpectedly restricts app path parameters")
	}
}

func TestShareGatewayRedirectBoundary(t *testing.T) {
	for _, tc := range []struct {
		location, want string
		status         int
	}{
		{"http://app.example.ts.net:8443/mount/login?q=x", "https://public-abc.example.ts.net/mount/login?q=x", 302},
		{"//app.example.ts.net:8443/mount/a%20b?q=x#fragment", "https://public-abc.example.ts.net/mount/a%20b?q=x#fragment", 302},
		{"/mount/login", "/mount/login", 302},
		{"https://id.example.com/login", "https://id.example.com/login", 302},
		{"http://app.example.ts.net:8443/other", "https://public-abc.example.ts.net/other", 302},
		{"http://app.example.ts.net/mount", "http://app.example.ts.net/mount", 302},
		{"http://app.example.ts.net.evil.com:8443/mount", "http://app.example.ts.net.evil.com:8443/mount", 302},
		{"/\\nas.example.ts.net/", "", 502},
		{"https:nas.example.ts.net", "", 502},
		{"https:/nas.example.ts.net", "", 502},
		{"//id.example.com/\\nas.example.ts.net", "", 502},
		{"http://[invalid/", "", 502},
		{"https://other.example.ts.net/", "https://other.example.ts.net/", 302},
	} {
		t.Run(tc.location, func(t *testing.T) {
			g := proxyTestGateway(t, proxyTestBinding(), func(*http.Request) *http.Response {
				return proxyTestResponse(302, http.Header{"Location": {tc.location}}, "redirect")
			})
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, proxyTestRequest("/mount"))
			if w.Code != tc.status || w.Header().Get("Location") != tc.want {
				t.Fatalf("response: %d %q", w.Code, w.Header().Get("Location"))
			}
			if w.Code == 502 && w.Body.String() != "App unavailable.\n" {
				t.Fatal("target leaked in public error")
			}
		})
	}
}

func TestShareUnsafePathDecodedBytes(t *testing.T) {
	for _, p := range []string{
		"/a\x00b", "/a\xffb", "/a\xc0\xaeb", "/a%", "/a%2", "/a%zz", "/a;%252e",
		"/../secret", "/./secret", "/%2e%2e/secret", "/%2fsecret", "/%5csecret",
		"/%252e%252e/secret", "/%25252fsecret", "/a\\b",
		"/..;/secret", "/..%3B/secret", "/..%253b/secret", "/..%25253B/secret",
		"/%252e%252e;/secret", "/a;parameter/secret", "/%00/secret", "/%2500/secret",
		"/%ff/secret", "/%c0%ae%c0%ae/secret", "/%25c0%25ae/secret",
		"/%e0%80%ae/secret", "/%f0%80%80%ae/secret", "/%ed%a0%80/secret",
	} {
		if !shareUnsafePath(p) {
			t.Errorf("unsafe bytes accepted: %q", p)
		}
	}
	if shareUnsafePath("/café/東京") {
		t.Fatal("valid UTF-8 rejected")
	}
}

func TestShareGatewayMountBoundary(t *testing.T) {
	g := proxyTestGateway(t, proxyTestBinding(), func(r *http.Request) *http.Response {
		return proxyTestResponse(200, nil, r.URL.RequestURI())
	})
	for _, tc := range []struct {
		method, target string
		status         int
		want           string
	}{
		{"GET", "/?q=one", 307, "/mount?q=one"},
		{"HEAD", "/", 307, "/mount"},
		{"POST", "/", 404, ""},
		{"GET", "/other", 404, ""},
		{"GET", "/mountain", 404, ""},
		{"GET", "/mount", 200, "/mount"},
		{"GET", "/mount/child?q=one", 200, "/mount/child?q=one"},
	} {
		t.Run(tc.method+" "+tc.target, func(t *testing.T) {
			request := proxyTestRequest(tc.target)
			request.Method = tc.method
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, request)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
			if tc.status == 307 && w.Header().Get("Location") != tc.want || tc.status == 200 && w.Body.String() != tc.want {
				t.Fatalf("path changed: location=%q body=%q", w.Header().Get("Location"), w.Body.String())
			}
		})
	}
}

func TestShareGatewayDefaultUpstreamHost(t *testing.T) {
	binding := proxyTestBinding()
	binding.Port = 80
	g := proxyTestGateway(t, binding, func(r *http.Request) *http.Response {
		if r.Host != binding.FQDN {
			t.Errorf("standard port retained in Host: %q", r.Host)
		}
		return proxyTestResponse(302, http.Header{"Location": {"http://app.example.ts.net:80/mount/login"}}, "")
	})
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, proxyTestRequest("/mount"))
	if w.Code != 302 || w.Header().Get("Location") != "https://public-abc.example.ts.net/mount/login" {
		t.Fatalf("default-port redirect: %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestShareGatewayPreservesCookiePaths(t *testing.T) {
	// The proxy strips Domain only; apps may configure their public cookie
	// paths explicitly, so do not silently rewrite their Path attributes.
	g := proxyTestGateway(t, proxyTestBinding(), func(*http.Request) *http.Response {
		return proxyTestResponse(200, http.Header{"Set-Cookie": {
			"root=1; Path=/; Secure; HttpOnly",
			"mount=2; Path=/mount; Domain=example.ts.net; SameSite=Lax",
			"child=3; Path=/mount/child",
			"default=4; Secure",
		}}, "app")
	})
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, proxyTestRequest("/mount"))
	want := []string{
		"root=1; Path=/; Secure; HttpOnly",
		"mount=2; Path=/mount; SameSite=Lax",
		"child=3; Path=/mount/child",
		"default=4; Secure",
	}
	got := w.Header().Values("Set-Cookie")
	if len(got) != len(want) {
		t.Fatalf("cookie count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cookie %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestShareGatewayRejectsUnapprovedAddresses(t *testing.T) {
	for _, address := range []string{"", "127.0.0.1", "169.254.1.1", "192.168.1.1", "10.0.0.1", "8.8.8.8", "::1", "fe80::1", "fd00::1", "::ffff:100.64.1.2", "100.100.100.100", "fd7a:115c:a1e0::53", "fd7a:115c:a1e0::1%zone"} {
		t.Run(address, func(t *testing.T) {
			g, err := newShareGateway(proxyTestBinding(), "public-abc.example.ts.net", func(context.Context, string, string) (net.Conn, error) {
				t.Error("dialed rejected address")
				return nil, errors.New("unexpected")
			}, func(context.Context) ([]netip.Addr, error) {
				if address == "" {
					return nil, nil
				}
				return []netip.Addr{netip.MustParseAddr("100.64.1.1"), netip.MustParseAddr(address)}, nil
			}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer g.Close()
			g.Open()
			if _, err := g.dialUpstream(context.Background(), "tcp", "ignored.invalid:1"); err == nil {
				t.Fatal("address accepted")
			}
		})
	}
}

func TestShareGatewayAdmissionExpiryAndCaps(t *testing.T) {
	g := proxyTestGateway(t, proxyTestBinding(), func(*http.Request) *http.Response { return proxyTestResponse(200, nil, "ok") })
	g.mu.Lock()
	g.opened = false
	g.mu.Unlock()
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, proxyTestRequest("/"))
	if w.Code != 503 {
		t.Fatal("closed admission served request")
	}
	g.Open()
	g.mu.Lock()
	g.requests = 32
	g.mu.Unlock()
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, proxyTestRequest("/"))
	if w.Code != 503 {
		t.Fatal("request cap not enforced")
	}
	g.mu.Lock()
	g.requests = 0
	g.upstreamConnections = 32
	g.mu.Unlock()
	if _, err := g.dialUpstream(context.Background(), "tcp", "ignored"); err == nil {
		t.Fatal("upstream cap not enforced")
	}
	g.mu.Lock()
	g.upstreamConnections = 0
	deadline := time.Now().Add(-time.Second)
	g.expiresAt = &deadline
	g.mu.Unlock()
	w = httptest.NewRecorder()
	g.Handler().ServeHTTP(w, proxyTestRequest("/"))
	if w.Code != 503 || g.Open() {
		t.Fatal("expired admission reopened")
	}
	g.Close()
	if g.Open() {
		t.Fatal("stopped admission reopened")
	}
}

func TestShareGatewayCloseCancelsDialAndClosesStreams(t *testing.T) {
	started := make(chan struct{})
	g, err := newShareGateway(proxyTestBinding(), "public-abc.example.ts.net", func(context.Context, string, string) (net.Conn, error) {
		t.Error("dial after blocked resolution")
		return nil, errors.New("unexpected")
	}, func(ctx context.Context) ([]netip.Addr, error) { close(started); <-ctx.Done(); return nil, ctx.Err() }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	g.Open()
	done := make(chan struct{})
	go func() { defer close(done); _, _ = g.dialUpstream(context.Background(), "tcp", "ignored") }()
	<-started
	// Both directions are owned even after an HTTP connection is hijacked.
	inbound, visitor := net.Pipe()
	upstream, app := net.Pipe()
	defer visitor.Close()
	defer app.Close()
	g.mu.Lock()
	g.connections[inbound] = true
	sharePublicConnections.Add(1)
	g.connections[upstream] = false
	g.upstreamConnections++
	g.mu.Unlock()
	g.connState(inbound, http.StateHijacked)
	g.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("dial not canceled")
	}
	for _, c := range []net.Conn{visitor, app} {
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := c.Read(make([]byte, 1)); err != io.EOF {
			t.Errorf("stream not closed: %v", err)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.connections) != 0 || g.upstreamConnections != 0 {
		t.Fatal("connection accounting leaked")
	}
}

func TestShareGatewayConfiguration(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") }
	resolve := func(context.Context) ([]netip.Addr, error) { return nil, nil }
	for _, base := range []string{"relative", "/a/../b", "/a//b", "/%2e", "/%252f", "/a\\b"} {
		binding := proxyTestBinding()
		binding.BasePath = base
		if _, err := newShareGateway(binding, "public.example.ts.net", dial, resolve, nil); err == nil {
			t.Errorf("unsafe base accepted: %q", base)
		}
	}
	binding := proxyTestBinding()
	binding.Scheme = "ftp"
	if _, err := newShareGateway(binding, "public.example.ts.net", dial, resolve, nil); err == nil {
		t.Fatal("unsupported scheme accepted")
	}
	binding = proxyTestBinding()
	binding.Scheme = "https"
	g, err := newShareGateway(binding, "public.example.ts.net", dial, resolve, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if g.transport.Proxy != nil || g.transport.TLSClientConfig.InsecureSkipVerify || g.transport.TLSClientConfig.ServerName != binding.FQDN {
		t.Fatal("untrusted TLS or environment proxy configuration")
	}
}

type proxyQueuedListener struct {
	queue chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func (l *proxyQueuedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.queue:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *proxyQueuedListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *proxyQueuedListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestShareGatewaySocketAdmissionCap(t *testing.T) {
	g := proxyTestGateway(t, proxyTestBinding(), func(*http.Request) *http.Response { return proxyTestResponse(200, nil, "") })
	source := &proxyQueuedListener{queue: make(chan net.Conn, 1), done: make(chan struct{})}
	defer source.Close()
	listener := &shareAdmissionListener{Listener: source, gateway: g}
	for range 64 {
		a, b := net.Pipe()
		defer b.Close()
		source.queue <- a
		if _, err := listener.Accept(); err != nil {
			t.Fatal(err)
		}
	}
	excess, visitor := net.Pipe()
	defer visitor.Close()
	source.queue <- excess
	done := make(chan struct{})
	go func() { defer close(done); _, _ = listener.Accept() }()
	_ = visitor.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := visitor.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("excess socket not closed before handshake: %v", err)
	}
	source.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("accept did not exit")
	}
	g.Close()
}

func TestShareGatewayOmitsRelayAddress(t *testing.T) {
	g := proxyTestGateway(t, proxyTestBinding(), func(r *http.Request) *http.Response {
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Error("visitor IP invented from relay or header")
		}
		return proxyTestResponse(200, nil, "ok")
	})
	r := proxyTestRequest("/mount")
	r.RemoteAddr = "192.0.2.5:443"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

type proxyHijackWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *proxyHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func proxyStreamGateway(t *testing.T, websocket bool) *shareGateway {
	t.Helper()
	dial := func(context.Context, string, string) (net.Conn, error) {
		client, app := net.Pipe()
		go func() {
			defer app.Close()
			if _, err := http.ReadRequest(bufio.NewReader(app)); err != nil {
				return
			}
			if websocket {
				if _, err := io.WriteString(app, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err == nil {
					_, _ = io.Copy(app, app)
				}
			} else {
				if _, err := io.WriteString(app, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\nd\r\ndata: hello\n\n\r\n"); err == nil {
					_, _ = io.Copy(io.Discard, app)
				}
			}
		}()
		return client, nil
	}
	g, err := newShareGateway(proxyTestBinding(), "public-abc.example.ts.net", dial, func(context.Context) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("100.101.102.103")}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	g.Open()
	return g
}

func TestShareGatewayWebsocketStop(t *testing.T) {
	g := proxyStreamGateway(t, true)
	public, visitor := net.Pipe()
	defer visitor.Close()
	g.mu.Lock()
	g.connections[public] = true
	sharePublicConnections.Add(1)
	g.mu.Unlock()
	w := &proxyHijackWriter{ResponseRecorder: httptest.NewRecorder(), conn: public}
	r := proxyTestRequest("/mount/socket")
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	done := make(chan struct{})
	go func() { defer close(done); g.Handler().ServeHTTP(w, r) }()
	_ = visitor.SetDeadline(time.Now().Add(2 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(visitor), r)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 101 {
		t.Fatal(response.StatusCode)
	}
	if _, err := visitor.Write([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 5)
	if _, err := io.ReadFull(visitor, frame); err != nil || string(frame) != "frame" {
		t.Fatalf("websocket stream: %q %v", frame, err)
	}
	g.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("websocket not stopped")
	}
	if _, err := visitor.Read(frame); err != io.EOF {
		t.Fatalf("public websocket still open: %v", err)
	}
}

type proxyFlushWriter struct {
	*httptest.ResponseRecorder
	flushed chan string
}

func (w *proxyFlushWriter) Flush() {
	select {
	case w.flushed <- w.Body.String():
	default:
	}
}

func TestShareGatewaySSEFlushAndStop(t *testing.T) {
	g := proxyStreamGateway(t, false)
	w := &proxyFlushWriter{ResponseRecorder: httptest.NewRecorder(), flushed: make(chan string, 8)}
	done := make(chan struct{})
	go func() { defer close(done); g.Handler().ServeHTTP(w, proxyTestRequest("/mount/events")) }()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case data := <-w.flushed:
			if strings.Contains(data, "data: hello\n\n") {
				g.Close()
				select {
				case <-done:
				case <-time.After(time.Second):
					t.Fatal("SSE not stopped")
				}
				return
			}
		case <-timer.C:
			t.Fatal("SSE data was buffered")
		}
	}
}
