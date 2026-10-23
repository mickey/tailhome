package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	tsapi "tailscale.com/client/tailscale/v2"
)

// stalledAPI returns a client whose token endpoint never answers.
func stalledAPI(t *testing.T, ctx context.Context, timeout time.Duration) *tsapi.Client {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	base, _ := url.Parse(srv.URL)
	return &tsapi.Client{
		BaseURL: base,
		Auth:    oauthClient{ctx: ctx, id: "id", secret: "secret", timeout: timeout},
	}
}

func within(t *testing.T, limit time.Duration, f func() error) {
	t.Helper()
	start := time.Now()
	err := f()
	if elapsed := time.Since(start); elapsed > limit {
		t.Fatalf("took %v, want under %v", elapsed, limit)
	}
	if err == nil {
		t.Fatal("call succeeded, want an error")
	}
}

func TestStalledTokenEndpointDoesNotHangRefresh(t *testing.T) {
	api := stalledAPI(t, t.Context(), 200*time.Millisecond)
	within(t, 2*time.Second, func() error { _, err := api.Services().List(t.Context()); return err })
}

func TestCtrlCDuringEnrollment(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	api := stalledAPI(t, ctx, time.Minute)
	time.AfterFunc(100*time.Millisecond, cancel)
	within(t, 2*time.Second, func() error { _, err := mintAuthKey(ctx, api, "tag:tailhome", time.Minute); return err })
}
