package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"tailscale.com/ipn"
)

// Public resource accounting is independent of the private control server.
var sharePublicConnections atomic.Int64
var sharePublicRequests atomic.Int64

var shareIPv4 = netip.MustParsePrefix("100.64.0.0/10")
var shareIPv6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
var shareDNSIPv4 = netip.MustParseAddr("100.100.100.100")
var shareDNSIPv6 = netip.MustParseAddr("fd7a:115c:a1e0::53")

func shareTailnetAddress(ip netip.Addr) bool {
	return shareIPv4.Contains(ip) || shareIPv6.Contains(ip)
}

// validateShareAddresses rejects the whole resolution if any address is unsafe.
// Authoritative node resolution and gateway dial admission share this boundary.
func validateShareAddresses(addresses []netip.Addr) ([]netip.Addr, error) {
	if len(addresses) == 0 {
		return nil, shareErr("target_unavailable")
	}
	for _, ip := range addresses {
		if !ip.IsValid() || ip.Zone() != "" || !shareTailnetAddress(ip) || ip == shareDNSIPv4 || ip == shareDNSIPv6 {
			return nil, shareErr("target_changed")
		}
	}
	return addresses, nil
}

type shareVisitorKey struct{}

type shareGateway struct {
	binding             ShareBinding
	publicHost          string
	upstream            *url.URL
	expiresAt           *time.Time
	dial                func(context.Context, string, string) (net.Conn, error)
	resolve             func(context.Context) ([]netip.Addr, error)
	transport           *http.Transport
	proxy               *httputil.ReverseProxy
	server              *http.Server
	ctx                 context.Context
	cancel              context.CancelFunc
	mu                  sync.Mutex
	opened, closed      bool
	connections         map[net.Conn]bool // true for public sockets, false for upstream sockets
	listeners           map[net.Listener]bool
	requests            int
	upstreamConnections int
	addresses           string
}

func newShareGateway(binding ShareBinding, publicHost string, dial func(context.Context, string, string) (net.Conn, error), resolve func(context.Context) ([]netip.Addr, error), expiresAt *time.Time) (*shareGateway, error) {
	if dial == nil || resolve == nil || binding.Port == 0 || (binding.Scheme != "http" && binding.Scheme != "https") || !shareDNSName(binding.FQDN) || !shareDNSName(publicHost) {
		return nil, errors.New("invalid share gateway configuration")
	}
	base := binding.BasePath
	if base == "" {
		base = "/"
	}
	if !strings.HasPrefix(base, "/") || shareUnsafePath(base) || path.Clean(base) != strings.TrimSuffix(base, "/") && base != "/" {
		return nil, errors.New("invalid share base path")
	}
	ctx, cancel := context.WithCancel(context.Background())
	g := &shareGateway{binding: binding, publicHost: strings.ToLower(publicHost), upstream: &url.URL{Scheme: binding.Scheme, Host: net.JoinHostPort(binding.FQDN, strconv.Itoa(int(binding.Port))), Path: base}, expiresAt: expiresAt, dial: dial, resolve: resolve, ctx: ctx, cancel: cancel, connections: make(map[net.Conn]bool), listeners: make(map[net.Listener]bool)}
	if binding.Scheme == "https" && binding.Port == 443 || binding.Scheme == "http" && binding.Port == 80 {
		g.upstream.Host = binding.FQDN
	}
	g.transport = &http.Transport{Proxy: nil, DialContext: g.dialUpstream, TLSClientConfig: &tls.Config{ServerName: binding.FQDN, MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, ExpectContinueTimeout: time.Second, IdleConnTimeout: 90 * time.Second, MaxConnsPerHost: 32, MaxIdleConns: 32, MaxIdleConnsPerHost: 32, ForceAttemptHTTP2: true}
	g.proxy = &httputil.ReverseProxy{Transport: g.transport, FlushInterval: -1, Rewrite: g.rewrite, ModifyResponse: g.modifyResponse, ErrorLog: log.New(shareDiscardLog{}, "", 0), ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
		sharePublicError(w, http.StatusBadGateway)
	}}
	g.server = &http.Server{Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 1 << 20, IdleTimeout: 60 * time.Second, ErrorLog: log.New(shareDiscardLog{}, "", 0), ConnContext: g.connContext, ConnState: g.connState}
	return g, nil
}

func shareDNSName(s string) bool {
	if len(s) == 0 || len(s) > 253 || strings.HasSuffix(s, ".") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Error text from net/http may contain credentials, paths, or issuer responses.
// Public failures are deliberately represented only by generic HTTP responses.
type shareDiscardLog struct{}

func (shareDiscardLog) Write(p []byte) (int, error) { return len(p), nil }

func sharePublicError(w http.ResponseWriter, status int) {
	text := "Share unavailable."
	switch status {
	case 400:
		text = "Invalid request."
	case 502:
		text = "App unavailable."
	}
	http.Error(w, text, status)
}

func (g *shareGateway) expired() bool { return g.expiresAt != nil && !time.Now().Before(*g.expiresAt) }
func (g *shareGateway) Open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.expired() {
		return false
	}
	g.opened = true
	return true
}

func (g *shareGateway) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w = &shareResponseWriter{ResponseWriter: w, gateway: g}
		g.mu.Lock()
		if !g.opened || g.closed || g.expired() {
			g.mu.Unlock()
			sharePublicError(w, 503)
			return
		}
		if g.requests >= 32 || !shareAcquire(&sharePublicRequests, 128) {
			g.mu.Unlock()
			sharePublicError(w, 503)
			return
		}
		g.requests++
		g.mu.Unlock()
		defer func() { g.mu.Lock(); g.requests--; g.mu.Unlock(); sharePublicRequests.Add(-1) }()
		if !g.validRequest(r) {
			sharePublicError(w, 400)
			return
		}
		if !g.mountedPath(r.URL.Path) {
			if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
				destination := *r.URL
				destination.Path = g.upstream.Path
				http.Redirect(w, r, destination.String(), http.StatusTemporaryRedirect)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		stop := context.AfterFunc(g.ctx, cancel)
		defer func() { stop(); cancel() }()
		r = r.WithContext(ctx)
		if r.Body != nil && r.Body != http.NoBody {
			rc := http.NewResponseController(w)
			_ = rc.SetReadDeadline(time.Now().Add(60 * time.Second))
			r.Body = &shareUploadBody{ReadCloser: r.Body, controller: rc}
			defer rc.SetReadDeadline(time.Time{})
		}
		g.proxy.ServeHTTP(w, r)
	})
}

func (g *shareGateway) validRequest(r *http.Request) bool {
	host := strings.ToLower(r.Host)
	if host != g.publicHost && host != g.publicHost+":443" {
		return false
	}
	if r.TLS == nil || !strings.EqualFold(r.TLS.ServerName, g.publicHost) || r.Method == http.MethodConnect || r.URL.IsAbs() || r.URL.Host != "" || !strings.HasPrefix(r.RequestURI, "/") {
		return false
	}
	if upgrade := r.Header.Get("Upgrade"); upgrade != "" && !strings.EqualFold(upgrade, "websocket") {
		return false
	}
	if g.upstream.Path != "/" {
		if shareUnsafePath(r.URL.EscapedPath()) {
			return false
		}
	}
	return true
}

func (g *shareGateway) mountedPath(p string) bool {
	prefix := strings.TrimSuffix(g.upstream.Path, "/")
	return prefix == "" || p == prefix || strings.HasPrefix(p, prefix+"/")
}

func shareUnsafePath(p string) bool {
	for {
		if !utf8.ValidString(p) || strings.ContainsAny(p, "\\;\x00") {
			return true
		}
		for _, segment := range strings.Split(p, "/") {
			if segment == "." || segment == ".." {
				return true
			}
		}
		lower := strings.ToLower(p)
		if strings.Contains(lower, "%2e") || strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") {
			return true
		}
		if !strings.Contains(p, "%") {
			return false
		}
		decoded, err := url.PathUnescape(p)
		if err != nil {
			return true
		}
		if decoded == p {
			return false
		}
		p = decoded
	}
}

func (g *shareGateway) rewrite(pr *httputil.ProxyRequest) {
	pr.Out.URL.Scheme = g.upstream.Scheme
	pr.Out.URL.Host = g.upstream.Host
	pr.Out.Host = g.upstream.Host
	for key := range pr.Out.Header {
		k := strings.ToLower(key)
		if k == "forwarded" || strings.HasPrefix(k, "x-forwarded-") || k == "x-real-ip" || strings.HasPrefix(k, "tailscale-") || strings.HasPrefix(k, "x-tailscale-") || strings.HasPrefix(k, "remote-") || strings.HasPrefix(k, "x-remote-") || strings.HasPrefix(k, "x-auth-request-") || k == "x-webauth-user" || strings.HasPrefix(k, "x-authentik-") || k == "x-original-url" || k == "x-rewrite-url" || k == "true-client-ip" || k == "cf-connecting-ip" {
			delete(pr.Out.Header, key)
		}
	}
	pr.Out.Header.Set("X-Forwarded-Host", g.publicHost)
	pr.Out.Header.Set("X-Forwarded-Proto", "https")
	pr.Out.Header.Set("X-Forwarded-Port", "443")
	if src, ok := pr.In.Context().Value(shareVisitorKey{}).(netip.Addr); ok && src.IsValid() {
		pr.Out.Header.Set("X-Forwarded-For", src.String())
	}
}

func (g *shareGateway) modifyResponse(resp *http.Response) error {
	if err := g.rewriteRedirect(resp); err != nil {
		return err
	}
	cookies := resp.Header.Values("Set-Cookie")
	if len(cookies) != 0 {
		resp.Header.Del("Set-Cookie")
		for _, cookie := range cookies {
			parts := strings.Split(cookie, ";")
			kept := parts[:1]
			for _, attr := range parts[1:] {
				if !strings.EqualFold(strings.TrimSpace(strings.SplitN(attr, "=", 2)[0]), "domain") {
					kept = append(kept, attr)
				}
			}
			resp.Header.Add("Set-Cookie", strings.Join(kept, ";"))
		}
	}
	return nil
}

func (g *shareGateway) rewriteRedirect(resp *http.Response) error {
	location := resp.Header.Get("Location")
	if location == "" {
		return nil
	}
	if strings.Contains(location, "\\") {
		return errors.New("invalid upstream redirect")
	}
	u, err := url.Parse(location)
	if err != nil || u.Scheme != "" && u.Host == "" {
		return errors.New("invalid upstream redirect")
	}
	if u.Host == "" {
		return nil
	}
	if strings.EqualFold(u.Host, g.upstream.Host) || strings.EqualFold(u.Host, net.JoinHostPort(g.binding.FQDN, strconv.Itoa(int(g.binding.Port)))) {
		if u.User != nil || u.Scheme != "" && u.Scheme != g.binding.Scheme {
			return errors.New("invalid upstream redirect")
		}
		u.Scheme, u.Host = "https", g.publicHost
		resp.Header.Set("Location", u.String())
	}
	return nil
}

func shareAcquire(counter *atomic.Int64, limit int64) bool {
	for {
		n := counter.Load()
		if n >= limit {
			return false
		}
		if counter.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

func (g *shareGateway) dialUpstream(ctx context.Context, network, _ string) (net.Conn, error) {
	g.mu.Lock()
	if !g.opened || g.closed || g.expired() || g.upstreamConnections >= 32 {
		g.mu.Unlock()
		return nil, errors.New("share unavailable")
	}
	g.upstreamConnections++
	g.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			g.mu.Lock()
			g.upstreamConnections--
			g.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	stop := context.AfterFunc(g.ctx, cancel)
	defer stop()
	addresses, err := g.resolve(ctx)
	if err != nil {
		return nil, errors.New("target unavailable")
	}
	addresses, err = validateShareAddresses(addresses)
	if err != nil {
		return nil, err
	}
	var signature strings.Builder
	for _, ip := range addresses {
		signature.WriteString(ip.String())
		signature.WriteByte(',')
	}
	g.mu.Lock()
	rotated := g.addresses != "" && g.addresses != signature.String()
	g.addresses = signature.String()
	g.mu.Unlock()
	if rotated {
		g.transport.CloseIdleConnections()
	}
	for _, ip := range addresses {
		g.mu.Lock()
		allowed := g.opened && !g.closed && !g.expired()
		g.mu.Unlock()
		if !allowed {
			return nil, errors.New("share unavailable")
		}
		conn, dialErr := g.dial(ctx, network, net.JoinHostPort(ip.String(), strconv.Itoa(int(g.binding.Port))))
		if dialErr != nil {
			err = dialErr
			continue
		}
		tracked := &shareTrackedConn{Conn: conn, gateway: g}
		g.mu.Lock()
		if g.closed || g.expired() {
			g.mu.Unlock()
			conn.Close()
			return nil, errors.New("share unavailable")
		}
		g.connections[tracked] = false
		g.mu.Unlock()
		reserved = false
		return tracked, nil
	}
	return nil, errors.New("target unavailable")
}

type shareTrackedConn struct {
	net.Conn
	gateway *shareGateway
	once    sync.Once
}

func (c *shareTrackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.gateway.releaseConnection(c) })
	return err
}
func (g *shareGateway) releaseConnection(c net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	public, ok := g.connections[c]
	if !ok {
		return
	}
	delete(g.connections, c)
	if public {
		sharePublicConnections.Add(-1)
	} else {
		g.upstreamConnections--
	}
}

type shareAdmissionListener struct {
	net.Listener
	gateway *shareGateway
}

func (l *shareAdmissionListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		g := l.gateway
		g.mu.Lock()
		n := 0
		for _, public := range g.connections {
			if public {
				n++
			}
		}
		if !g.closed && g.opened && !g.expired() && n < 64 && shareAcquire(&sharePublicConnections, 256) {
			g.connections[c] = true
			g.mu.Unlock()
			return c, nil
		}
		g.mu.Unlock()
		c.Close()
	}
}
func (g *shareGateway) Serve(l net.Listener) error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		l.Close()
		return http.ErrServerClosed
	}
	g.listeners[l] = true
	g.mu.Unlock()
	return g.server.Serve(&shareAdmissionListener{Listener: l, gateway: g})
}
func (g *shareGateway) connState(c net.Conn, state http.ConnState) {
	if state == http.StateClosed {
		g.releaseConnection(c)
	}
}
func (g *shareGateway) connContext(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if fc, ok := c.(*ipn.FunnelConn); ok {
		if fc.Src.IsValid() {
			return context.WithValue(ctx, shareVisitorKey{}, fc.Src.Addr())
		}
		return ctx
	}
	return ctx
}

// Close revokes locally before asynchronous listener teardown: Funnel's control
// plane must not hold active sockets or private Stop hostage.
func (g *shareGateway) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed, g.opened = true, false
	sockets := make([]net.Conn, 0, len(g.connections))
	for c := range g.connections {
		sockets = append(sockets, c)
	}
	listeners := make([]net.Listener, 0, len(g.listeners))
	for l := range g.listeners {
		listeners = append(listeners, l)
	}
	g.mu.Unlock()
	g.cancel()
	for _, c := range sockets {
		c.Close()
		g.releaseConnection(c)
	}
	g.transport.CloseIdleConnections()
	for _, l := range listeners {
		go l.Close()
	}
	return nil
}

type shareUploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
}

func (b *shareUploadBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_ = b.controller.SetReadDeadline(time.Now().Add(60 * time.Second))
	}
	if err != nil {
		_ = b.controller.SetReadDeadline(time.Time{})
	}
	return n, err
}

type shareResponseWriter struct {
	http.ResponseWriter
	gateway *shareGateway
}

func (w *shareResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *shareResponseWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *shareResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return &shareHijackedConn{Conn: c, gateway: w.gateway}, rw, nil
}

type shareHijackedConn struct {
	net.Conn
	gateway *shareGateway
	once    sync.Once
}

func (c *shareHijackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.gateway.releaseConnection(c.Conn) })
	return err
}
