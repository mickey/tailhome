package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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

// Only a token endpoint that refuses the auth_keys scope (403, a read-only
// client) should fall back to the login link. A wrong secret (401), or a
// token that works but cannot create a key for the tag (400 from /keys), are
// configuration errors that must stop tailhome.
func TestReadOnlyClientFallsBackOtherErrorsFail(t *testing.T) {
	for _, tc := range []struct {
		tokenStatus, keysStatus int
		fallback                bool
	}{
		{http.StatusForbidden, 0, true},
		{http.StatusUnauthorized, 0, false},
		{http.StatusOK, http.StatusBadRequest, false},
		{http.StatusOK, http.StatusForbidden, false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case tc.tokenStatus != http.StatusOK:
				w.WriteHeader(tc.tokenStatus)
				w.Write([]byte(`{"message":"OAuth client cannot grant scopes \"auth_keys\""}`))
			case strings.HasSuffix(r.URL.Path, "/oauth/token"):
				w.Write([]byte(`{"access_token":"tskey-test","token_type":"Bearer","expires_in":3600}`))
			default:
				w.WriteHeader(tc.keysStatus)
				w.Write([]byte(`{"message":"requested tags [tag:tailhome] are invalid or not permitted"}`))
			}
		}))
		base, _ := url.Parse(srv.URL)
		api := &tsapi.Client{BaseURL: base, Auth: oauthClient{ctx: t.Context(), id: "id", secret: "secret", scopes: []string{"auth_keys"}, timeout: time.Second}}
		_, err := mintAuthKey(t.Context(), api, "tag:tailhome", time.Second)
		srv.Close()
		if err == nil {
			t.Fatalf("token %d, keys %d: minting succeeded, want an error", tc.tokenStatus, tc.keysStatus)
		}
		if got := cannotMintKeys(err); got != tc.fallback {
			t.Errorf("token %d, keys %d: cannotMintKeys = %v, want %v (err: %v)", tc.tokenStatus, tc.keysStatus, got, tc.fallback, err)
		}
	}
}

// tsnet writes tailscaled.state before login, so a first start that only
// printed a login link must not count as enrolled on the next start.
func TestEnrolledNeedsALoggedInProfile(t *testing.T) {
	const noFile = "<none>"
	for _, tc := range []struct {
		name, state   string
		want, wantErr bool
	}{
		{"no state file", noFile, false, false},
		{"empty state file", "", false, false},
		{"login link never opened", `{"_machinekey":"bWs="}`, false, false},
		{"joined", `{"_current-profile":"cHJvZmlsZS0x","_machinekey":"bWs=","_profiles":"e30=","profile-1":"e30="}`, true, false},
		{"corrupt state file", `{"_current-profile":`, false, true},
	} {
		dir := t.TempDir()
		if tc.state != noFile {
			if err := os.WriteFile(filepath.Join(dir, "tailscaled.state"), []byte(tc.state), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := enrolled(dir)
		if got != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("%s: enrolled = %v, %v; want %v, error %v", tc.name, got, err, tc.want, tc.wantErr)
		}
	}
}
