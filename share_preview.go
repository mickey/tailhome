package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// previewFingerprint binds the exact published card (or its absence) to the
// canonical device endpoint identity.
func previewFingerprint(binding ShareBinding, raw []byte) string {
	h := sha256.New()
	io.WriteString(h, targetIDForBinding(binding))
	presence := byte(0)
	if raw != nil {
		presence = 1
	}
	h.Write([]byte{0, presence})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

func checkPreviewCard(ctx context.Context, binding ShareBinding, dial func(context.Context, string, string) (net.Conn, error), resolve func(context.Context) ([]netip.Addr, error), noCard bool, roots *x509.CertPool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addresses, err := resolve(ctx)
	if err != nil {
		return "", err
	}
	addresses, err = validateShareAddresses(addresses)
	if err != nil {
		return "", err
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			// net/http detaches dials from the request's cancellation. Bound each
			// dial independently so an unreachable peer cannot leave it running.
			ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			var err error
			for _, ip := range addresses {
				if err = ctx.Err(); err != nil {
					return nil, err
				}
				conn, dialErr := dial(ctx, "tcp", netip.AddrPortFrom(ip, binding.Port).String())
				if dialErr == nil {
					return conn, nil
				}
				err = dialErr
			}
			return nil, err
		},
		TLSClientConfig:       &tls.Config{ServerName: binding.FQDN, RootCAs: roots, MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   2 * time.Second,
		ResponseHeaderTimeout: 2 * time.Second,
		DisableKeepAlives:     true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("preview card redirect rejected")
	}}
	card, result, status := fetchCard(ctx, client, strings.TrimSuffix(endpointURL(binding.FQDN, binding.Port), "/")+cardPath)
	if noCard && (status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout) {
		return previewFingerprint(binding, nil), nil
	}
	switch result {
	case "ok":
		return previewFingerprint(binding, card.raw), nil
	case "absent", "invalid":
		return previewFingerprint(binding, nil), nil
	default:
		return "", fmt.Errorf("preview card: %s", result)
	}
}

// A preview must match its approved card before the gateway admits requests.
func (m *shareManager) startPreviewWatch(ctx context.Context, record ShareRecord, runtime shareRuntime, resolve func(context.Context) ([]netip.Addr, error)) bool {
	noCard := previewFingerprint(record.Binding, nil)
	check := m.checkPreview
	if check == nil {
		check = func(ctx context.Context) (string, error) {
			return checkPreviewCard(ctx, record.Binding, runtime.Dial, resolve, record.PreviewFingerprint == noCard, nil)
		}
	}
	ready := make(chan bool, 1)
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		defer close(ready)
		ticks := m.previewTick
		if ticks == nil {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			ticks = ticker.C
		}
		failures := 0
		announced := false
		for {
			fingerprint, err := check(ctx)
			if ctx.Err() != nil {
				return
			}
			reason := ""
			if err != nil {
				failures++
				if failures == 3 {
					reason = "preview_unreachable"
				}
			} else {
				failures = 0
				if fingerprint != record.PreviewFingerprint {
					reason = "preview_changed"
					if fingerprint == noCard {
						reason = "preview_gone"
					}
				}
			}
			if reason != "" {
				_ = m.stop(record.ID, "", "", reason)
				return
			}
			if err == nil && !announced {
				ready <- true
				announced = true
			}
			select {
			case <-ctx.Done():
				return
			case <-ticks:
			}
		}
	}()
	select {
	case <-ctx.Done():
		return false
	case matched := <-ready:
		return matched
	}
}
