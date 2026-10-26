package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync"
	"time"
)

// Prober checks Services from the tailnet node, so it tests the real network path.
// Each check uses a fresh connection and does not follow redirects.
type Prober struct {
	client  *http.Client
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	timeout time.Duration // per Service, across all its attempts
}

func NewProber(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *Prober {
	return &Prober{
		client: &http.Client{
			Transport: &http.Transport{
				DialContext:         dial,
				TLSHandshakeTimeout: 5 * time.Second,
				DisableKeepAlives:   true,
				// Reachability check only: self-signed backends count as up.
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		dial:    dial,
		timeout: 5 * time.Second,
	}
}

// Check fills in Status and Reason, and URL for entries that answer HTTP on a probed port.
func (p *Prober) Check(ctx context.Context, entries []Entry) {
	var wg sync.WaitGroup
	for i := range entries {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()
			p.check(ctx, &entries[i])
		})
	}
	wg.Wait()
}

func (p *Prober) check(ctx context.Context, e *Entry) {
	if e.probe == nil {
		if e.URL == "" {
			e.Status, e.Reason = p.tcpCheck(ctx, e.Host, e.Ports)
			return
		}
		u, err := url.Parse(e.URL)
		switch {
		case err == nil && (u.Scheme == "http" || u.Scheme == "https"):
			_, e.Status, e.Reason = httpCheck(ctx, p.client, e.URL)
		case err == nil && u.Port() != "": // e.g. a vnc://host:5900 override
			e.Status, e.Reason = p.dialCheck(ctx, u.Host)
		default: // a link without a port, such as ssh://host: check the Service's own
			e.Status, e.Reason = p.tcpCheck(ctx, e.Host, e.Ports)
		}
		return
	}
	// Reachability first: a daemon that swallows the TLS hello without answering
	// would otherwise spend the whole budget on the https attempt.
	tcpStatus, tcpReason := p.tcpCheck(ctx, e.Host, e.Ports)
	if tcpStatus == StatusDown {
		e.Status, e.Reason = StatusDown, tcpReason
		return
	}
	candidates := e.probe
	if e.URL != "" { // known from an earlier refresh: try it first
		candidates = append([]string{e.URL}, slices.DeleteFunc(slices.Clone(e.probe), func(u string) bool { return u == e.URL })...)
	}
	// Any HTTP answer, even a 503, makes the Service web; its code decides health.
	var reason string
	for _, u := range candidates {
		answered, status, r := httpCheck(ctx, p.client, u)
		if answered {
			e.URL, e.Status, e.Reason = u, status, r
			return
		}
		reason = r
	}
	if e.URL != "" { // stays a tile, marked down
		e.Status, e.Reason = StatusDown, reason
		return
	}
	e.Status, e.Reason = tcpStatus, tcpReason
}

// httpCheck treats 5xx as down: `tailscale serve` answers 502 when its backend is gone.
func httpCheck(ctx context.Context, client *http.Client, u string) (answered bool, s Status, reason string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, StatusDown, err.Error()
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := client.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return false, StatusDown, err.Error()
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return true, StatusDown, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return true, StatusUp, ""
}

func (p *Prober) tcpCheck(ctx context.Context, host string, ports []string) (Status, string) {
	port, ok := firstTCPPort(ports)
	if !ok && servesPort(ports, 443) { // tcp:*
		port, ok = "443", true
	}
	if !ok {
		return StatusUnknown, "no TCP port to check"
	}
	return p.dialCheck(ctx, net.JoinHostPort(host, port))
}

func (p *Prober) dialCheck(ctx context.Context, addr string) (Status, string) {
	conn, err := p.dial(ctx, "tcp", addr)
	if err != nil {
		return StatusDown, err.Error()
	}
	conn.Close()
	return StatusUp, ""
}
