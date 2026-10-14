// Command tailhome serves a start page for a tailnet's Tailscale Services.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/tsnet"
)

var version = "dev" // set by release builds

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version // go install …@vX.Y.Z
	}
}

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	clientID := os.Getenv("TAILHOME_CLIENT_ID")
	clientSecret := os.Getenv("TAILHOME_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return errors.New("TAILHOME_CLIENT_ID and TAILHOME_CLIENT_SECRET are required")
	}
	service := env("TAILHOME_SERVICE", "svc:home")
	refresh, err := time.ParseDuration(env("TAILHOME_REFRESH", "5m"))
	if err != nil || refresh <= 0 {
		return fmt.Errorf("TAILHOME_REFRESH must be a positive duration, got %q", os.Getenv("TAILHOME_REFRESH"))
	}
	log.Printf("tailhome %s", version)

	tag := env("TAILHOME_TAG", "tag:tailhome")
	api := &tsapi.Client{
		Tailnet: os.Getenv("TAILHOME_TAILNET"),
		Auth: oauthClient{
			ctx: ctx, id: clientID, secret: clientSecret,
			scopes:  []string{"services:read", "auth_keys"},
			timeout: 30 * time.Second,
		},
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	srv := &tsnet.Server{
		Hostname:      env("TAILHOME_HOSTNAME", "tailhome"),
		Dir:           dir,
		AdvertiseTags: []string{tag},
	}
	defer srv.Close()
	isEnrolled, err := enrolled(dir)
	if err != nil {
		return err
	}
	if !isEnrolled && os.Getenv("TS_AUTHKEY") == "" {
		if srv.AuthKey, err = mintAuthKey(ctx, api, tag, time.Minute); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("joining the tailnet: creating an auth key for %s: %w", tag, err)
		}
	}

	// Up honors ctx, unlike ListenService, so Ctrl-C works during login.
	if _, err := srv.Up(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("joining the tailnet: %w", err)
	}
	httpsLn, err := srv.ListenService(service, tsnet.ServiceModeHTTP{HTTPS: true, Port: 443})
	if err != nil {
		return fmt.Errorf("publishing %s on port 443: %w", service, err)
	}
	defer httpsLn.Close()
	httpLn, err := srv.ListenService(service, tsnet.ServiceModeHTTP{Port: 80})
	if err != nil {
		return fmt.Errorf("publishing %s on port 80: %w", service, err)
	}
	defer httpLn.Close()
	log.Printf("serving https://%s", httpsLn.FQDN)
	_, dnsSuffix, _ := strings.Cut(httpsLn.FQDN, ".") // home.tn.ts.net → tn.ts.net

	catalog := &Catalog{
		Services:      api.Services(),
		OverridesPath: env("TAILHOME_OVERRIDES", "overrides.yaml"),
		DNSSuffix:     dnsSuffix,
		Self:          service,
		Prober:        NewProber(srv.Dial),
	}
	go catalog.Run(ctx, refresh)

	title := strings.TrimPrefix(service, "svc:")
	errc := make(chan error, 2)
	go func() { errc <- serve(httpsLn, NewHandler(catalog, title)) }()
	go func() { errc <- serve(httpLn, redirectTo("https://"+httpsLn.FQDN)) }()
	select {
	case <-ctx.Done():
		stop() // a second Ctrl-C now kills a slow shutdown
		return nil
	case err := <-errc:
		return err
	}
}

func serve(ln net.Listener, h http.Handler) error {
	return (&http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}

// The Service certificate only covers the FQDN, so "http://home" must land there.
func redirectTo(base string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, base+r.URL.RequestURI(), http.StatusFound)
	})
}

func env(key, fallback string) string {
	return cmp.Or(os.Getenv(key), fallback)
}
