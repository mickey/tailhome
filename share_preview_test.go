package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func previewTestBinding() ShareBinding {
	return ShareBinding{Kind: "device", NodeID: "preview-node", FQDN: "preview.example.ts.net", Scheme: "https", Port: 8443, BasePath: "/"}
}

func previewTestResolve(ctx context.Context) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("100.64.0.42")}, nil
}

// The real transport verifies the certificate; only the supplied node dial is
// mapped from a validated Tailscale literal to the loopback test listener.
func previewTestServer(t *testing.T, handler http.Handler) (func(context.Context, string, string) (net.Conn, error), *x509.CertPool) {
	t.Helper()
	ca := newDeviceCA(t)
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{ca.leaf(t, previewTestBinding().FQDN, false)}}
	server.StartTLS()
	t.Cleanup(server.Close)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}, ca.roots
}

func TestPreviewFingerprintExactBytesAndBinding(t *testing.T) {
	binding := previewTestBinding()
	raw := []byte(`{"title":"Preview"}`)
	original := previewFingerprint(binding, raw)
	decoded, err := hex.DecodeString(original)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("fingerprint is not a SHA256 hex digest: %q", original)
	}
	if previewFingerprint(binding, append([]byte(nil), raw...)) != original {
		t.Fatal("identical bytes changed fingerprint")
	}
	for _, body := range [][]byte{nil, {}, []byte("{ \"title\":\"Preview\"}"), []byte(`{"title":"Other"}`)} {
		if previewFingerprint(binding, body) == original {
			t.Fatalf("different body retained fingerprint: %q", body)
		}
	}
	if previewFingerprint(binding, nil) == previewFingerprint(binding, []byte{}) {
		t.Fatal("absent and present empty body are ambiguous")
	}
	for _, field := range []string{"node", "fqdn", "port"} {
		changed := binding
		switch field {
		case "node":
			changed.NodeID += "-new"
		case "fqdn":
			changed.FQDN = "other.example.ts.net"
		case "port":
			changed.Port++
		}
		if previewFingerprint(changed, raw) == original {
			t.Fatalf("changed %s retained fingerprint", field)
		}
	}
}

func TestPreviewCheckerExactChangesAndPresence(t *testing.T) {
	status, body := http.StatusNotFound, ""
	dial, roots := previewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.RequestURI() != cardPath || r.Host != "preview.example.ts.net:8443" {
			t.Errorf("unexpected request: %s %s %s", r.Method, r.URL, r.Host)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	for _, step := range []struct {
		status int
		body   string
		raw    []byte
		ok     bool
	}{
		{404, "", nil, true},
		{200, `{"title":"Preview"}`, []byte(`{"title":"Preview"}`), true},
		{200, " {\"title\":\"Preview\"}\n", []byte(" {\"title\":\"Preview\"}\n"), true},
		{404, "", nil, true},
		{200, "<!doctype html><title>App fallback</title>", nil, true},
		{200, "[]", nil, true},
		{200, "", nil, true},
		{410, "", nil, true},
		{500, "", nil, false},
		{204, "", nil, false},
	} {
		status, body = step.status, step.body
		got, err := checkPreviewCard(t.Context(), previewTestBinding(), dial, previewTestResolve, false, roots)
		want := ""
		if step.ok {
			want = previewFingerprint(previewTestBinding(), step.raw)
		}
		if (err == nil) != step.ok || got != want {
			t.Fatalf("HTTP %d body %q: fingerprint=%q want=%q err=%v", status, body, got, want, err)
		}
	}
}

func TestPreviewCheckerRejectsRedirects(t *testing.T) {
	var requests atomic.Int32
	dial, roots := previewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Location", cardPath+"?next=1")
		w.WriteHeader(http.StatusFound)
	}))
	if _, err := checkPreviewCard(t.Context(), previewTestBinding(), dial, previewTestResolve, false, roots); err == nil {
		t.Fatal("redirect accepted")
	}
	if requests.Load() != 1 {
		t.Fatalf("followed redirect: %d requests", requests.Load())
	}
}

func TestPreviewCheckerVerifiesTLS(t *testing.T) {
	for _, mode := range []string{"untrusted", "wrong hostname"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			dial, roots := previewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, "{}")
			}))
			binding := previewTestBinding()
			if mode == "untrusted" {
				roots = x509.NewCertPool()
			} else {
				binding.FQDN = "wrong.example.ts.net"
			}
			if _, err := checkPreviewCard(t.Context(), binding, dial, previewTestResolve, false, roots); err == nil {
				t.Fatal("invalid TLS certificate accepted")
			}
			if requests.Load() != 0 {
				t.Fatal("HTTP request sent before verified TLS")
			}
		})
	}
}

func TestPreviewCheckerRejectsUnsafeResolution(t *testing.T) {
	for _, addresses := range [][]netip.Addr{
		nil,
		{netip.MustParseAddr("127.0.0.1")},
		{netip.MustParseAddr("100.64.0.42"), netip.MustParseAddr("203.0.113.1")},
		{shareDNSIPv4},
		{shareDNSIPv6},
		{netip.MustParseAddr("fd7a:115c:a1e0::1%zone")},
	} {
		_, err := checkPreviewCard(t.Context(), previewTestBinding(), func(context.Context, string, string) (net.Conn, error) {
			t.Error("unsafe resolution reached dial")
			return nil, errors.New("unexpected dial")
		}, func(context.Context) ([]netip.Addr, error) { return addresses, nil }, false, nil)
		if err == nil {
			t.Fatalf("unsafe addresses accepted: %v", addresses)
		}
	}
}

func TestPreviewCheckerDetachedDialDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered := make(chan time.Duration, 1)
	terminated := make(chan error, 1)
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		deadline, ok := ctx.Deadline()
		remaining := time.Duration(0)
		if ok {
			remaining = time.Until(deadline)
		}
		entered <- remaining
		<-ctx.Done()
		terminated <- ctx.Err()
		return nil, ctx.Err()
	}
	checked := make(chan error, 1)
	go func() {
		_, err := checkPreviewCard(ctx, previewTestBinding(), dial, previewTestResolve, false, nil)
		checked <- err
	}()
	select {
	case remaining := <-entered:
		if remaining <= 0 || remaining > 2*time.Second {
			t.Errorf("detached dial deadline is %v away, want within two seconds", remaining)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("dial did not start")
	}
	cancel()
	select {
	case err := <-checked:
		if err == nil {
			t.Error("canceled check succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("check did not return after cancellation")
	}
	select {
	case err := <-terminated:
		if err == nil {
			t.Fatal("detached dial terminated without a cancellation error")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("detached dial outlived its independent deadline")
	}
}

func TestPreviewCheckerNoCardFailures(t *testing.T) {
	for _, noCard := range []bool{false, true} {
		for _, mode := range []string{"500", "502", "503", "504", "unserved port", "TLS error"} {
			name := "present-card/"
			if noCard {
				name = "no-card/"
			}
			t.Run(name+mode, func(t *testing.T) {
				status := map[string]int{"500": 500, "502": 502, "503": 503, "504": 504}[mode]
				dial, roots := previewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(status)
				}))
				if mode == "unserved port" {
					dial = func(context.Context, string, string) (net.Conn, error) {
						return nil, errors.New("port unserved")
					}
				} else if mode == "TLS error" {
					roots = x509.NewCertPool()
				}
				fingerprint, err := checkPreviewCard(t.Context(), previewTestBinding(), dial, previewTestResolve, noCard, roots)
				if noCard && (status == 502 || status == 503 || status == 504) {
					if err != nil || fingerprint != previewFingerprint(previewTestBinding(), nil) {
						t.Fatal("verified Serve gateway response changed a no-card preview", err)
					}
				} else if err == nil || fingerprint != "" {
					t.Fatal("no-card approval suppressed an unrelated HTTP or transport failure")
				}
			})
		}
	}
}
