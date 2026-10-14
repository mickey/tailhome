package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	tsapi "tailscale.com/client/tailscale/v2"
)

// oauthClient replaces tsapi.OAuth, whose token fetch uses context.Background()
// and http.DefaultClient: a stalled token endpoint would hang every refresh.
type oauthClient struct {
	ctx     context.Context // cancels token fetches on shutdown
	id      string
	secret  string
	scopes  []string
	timeout time.Duration // per token fetch
}

func (o oauthClient) HTTPClient(orig *http.Client, baseURL string) *http.Client {
	cfg := clientcredentials.Config{
		ClientID:     o.id,
		ClientSecret: o.secret,
		Scopes:       o.scopes,
		TokenURL:     baseURL + "/api/v2/oauth/token",
	}
	ctx := context.WithValue(o.ctx, oauth2.HTTPClient, &http.Client{Timeout: o.timeout})
	return &http.Client{
		Transport:     &oauth2.Transport{Base: orig.Transport, Source: oauth2.ReuseTokenSource(nil, cfg.TokenSource(ctx))},
		CheckRedirect: orig.CheckRedirect,
		Timeout:       orig.Timeout,
	}
}

// stateDir mirrors tsnet's default, but with a fixed name so it doesn't
// depend on what the binary is called.
func stateDir() (string, error) {
	if dir := os.Getenv("TAILHOME_STATE_DIR"); dir != "" {
		return dir, nil
	}
	conf, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("no TAILHOME_STATE_DIR and no user config dir: %w", err)
	}
	return filepath.Join(conf, "tsnet-tailhome"), nil
}

func enrolled(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, "tailscaled.state"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// mintAuthKey creates the one-off key tsnet joins with. Doing it here rather
// than through tsnet's ClientSecret keeps it cancellable and bounded: tsnet
// mints with an internal context that Ctrl-C can't reach.
func mintAuthKey(ctx context.Context, api *tsapi.Client, tag string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var req tsapi.CreateKeyRequest
	req.Description = "tailhome enrollment"
	req.ExpirySeconds = 600
	req.Capabilities.Devices.Create.Preauthorized = true
	req.Capabilities.Devices.Create.Tags = []string{tag}
	key, err := api.Keys().CreateAuthKey(ctx, req)
	if err != nil {
		return "", err
	}
	return key.Key, nil
}
