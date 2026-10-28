// Command tailhome serves the start page that builds itself from your tailnet.
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
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"
	"unicode"

	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tsnet"
	"tailscale.com/util/dnsname"
)

var version = "dev" // set by release builds

func init() {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version // go install …@vX.Y.Z
	}
}

// userAgent names tailhome in app logs, so admins can tell its requests apart and allow them.
// It is a func because version is final only after init.
func userAgent() string { return "tailhome/" + version + " (+https://github.com/mickey/tailhome)" }

func main() {
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "The start page that builds itself from your tailnet.")
		fmt.Fprintf(flag.CommandLine.Output(), "\nUsage: %s [flags]\n", os.Args[0])
		flag.PrintDefaults()
	}
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
	if (clientID == "") != (clientSecret == "") {
		return errors.New("set both TAILHOME_CLIENT_ID and TAILHOME_CLIENT_SECRET, or neither")
	}
	apiMode := clientID != ""
	refresh, err := time.ParseDuration(env("TAILHOME_REFRESH", "5m"))
	if err != nil || refresh <= 0 {
		return fmt.Errorf("TAILHOME_REFRESH must be a positive duration, got %q", os.Getenv("TAILHOME_REFRESH"))
	}
	devicesOn := env("TAILHOME_DEVICES", "on")
	if devicesOn != "on" && devicesOn != "off" {
		return fmt.Errorf("TAILHOME_DEVICES must be on or off, got %q", devicesOn)
	}
	deviceTags, err := envTags("TAILHOME_DEVICE_TAGS")
	if err != nil {
		return err
	}
	insecureTLSTags, err := envTags("TAILHOME_DEVICE_INSECURE_TLS_TAGS")
	if err != nil {
		return err
	}
	previewPorts, err := parsePreviewPorts(os.Getenv("TAILHOME_PREVIEW_PORTS"))
	if err != nil {
		return err
	}
	log.Printf("tailhome %s", version)
	if devicesOn == "off" && len(previewPorts) != 0 {
		log.Printf("TAILHOME_PREVIEW_PORTS is ignored while TAILHOME_DEVICES=off")
	}

	dir, err := stateDir()
	if err != nil {
		return err
	}
	srv := &tsnet.Server{Dir: dir}
	defer srv.Close()
	var api *tsapi.Client
	if apiMode {
		tag := env("TAILHOME_TAG", "tag:tailhome")
		api = &tsapi.Client{
			Tailnet: os.Getenv("TAILHOME_TAILNET"),
			Auth: oauthClient{
				ctx: ctx, id: clientID, secret: clientSecret,
				scopes:  []string{"services:read", "auth_keys"},
				timeout: 30 * time.Second,
			},
		}
		srv.Hostname = env("TAILHOME_HOSTNAME", "tailhome")
		srv.AdvertiseTags = []string{tag}
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
	} else {
		// Without credentials the node serves the page on its own name, so it
		// needs no tag, no Service definition and no approval.
		srv.Hostname = env("TAILHOME_HOSTNAME", "home")
	}

	// Up honors ctx, unlike ListenService, so Ctrl-C works during login.
	// Without an auth key, tsnet logs a login URL until someone opens it.
	st, err := srv.Up(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("joining the tailnet: %w", err)
	}
	lc, err := srv.LocalClient()
	if err != nil {
		return err
	}

	var (
		httpsLn, httpLn net.Listener
		fqdn, self      string
		services        ServiceLister
		notices         func(context.Context) []Notice
	)
	if apiMode {
		self = env("TAILHOME_SERVICE", "svc:home")
		ln, err := srv.ListenService(self, tsnet.ServiceModeHTTP{HTTPS: true, Port: 443})
		if errors.Is(err, tsnet.ErrUntaggedServiceHost) {
			return fmt.Errorf("publishing %s: this node joined without a tag, and only tagged nodes can host a Service: delete %s and restart to join again with %s", self, filepath.Join(dir, "tailscaled.state"), srv.AdvertiseTags[0])
		}
		if err != nil {
			return fmt.Errorf("publishing %s on port 443: %w", self, err)
		}
		httpsLn, fqdn = ln, ln.FQDN
		if httpLn, err = srv.ListenService(self, tsnet.ServiceModeHTTP{Port: 80}); err != nil {
			return fmt.Errorf("publishing %s on port 80: %w", self, err)
		}
		services = api.Services()
	} else {
		if httpsLn, err = srv.ListenTLS("tcp", ":443"); err != nil {
			return fmt.Errorf("listening on port 443: %w", err)
		}
		if httpLn, err = srv.Listen("tcp", ":80"); err != nil {
			return fmt.Errorf("listening on port 80: %w", err)
		}
		fqdn = srv.CertDomains()[0] // ListenTLS fails when there is none
		services = localServices{lc}
		hostname := srv.Hostname
		for _, n := range nodeNotices(st.Self, hostname) {
			if n.URL != "" {
				log.Printf("warning: %s %s on %s", n.Text, n.LinkText, n.URL)
			} else {
				log.Printf("warning: %s", n.Text)
			}
		}
		// Rechecked on every refresh, so fixing the problem clears the notice.
		notices = func(ctx context.Context) []Notice {
			st, err := lc.StatusWithoutPeers(ctx)
			if err != nil {
				return nil
			}
			return nodeNotices(st.Self, hostname)
		}
	}
	defer httpsLn.Close()
	defer httpLn.Close()
	log.Printf("serving https://%s", fqdn)
	title, dnsSuffix, _ := strings.Cut(fqdn, ".") // home.tn.ts.net → home, tn.ts.net

	icons := &Icons{Fetch: fetchIconNames}
	catalog := &Catalog{
		Services:        services,
		OverridesPath:   env("TAILHOME_OVERRIDES", "overrides.yaml"),
		DNSSuffix:       dnsSuffix,
		Self:            self,
		Prober:          NewProber(srv.Dial),
		Local:           !apiMode,
		Notices:         notices,
		Icons:           icons,
		DeviceTags:      deviceTags,
		InsecureTLSTags: insecureTLSTags,
		PreviewPorts:    previewPorts,
		DeviceProber:    &DeviceProber{dial: srv.Dial},
	}
	if devicesOn == "on" {
		catalog.Devices = localDevices{lc}
	}
	icons.Loaded = func() { catalog.Refresh(ctx) } // show the matched icons now, not at the next refresh
	go catalog.Run(ctx, refresh)

	errc := make(chan error, 2)
	go func() { errc <- serve(httpsLn, NewHandler(catalog, title)) }()
	go func() { errc <- serve(httpLn, redirectTo("https://"+fqdn)) }()
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

// envTags parses a comma-separated tag list; whitespace around tags is ignored.
func envTags(key string) ([]string, error) {
	tags := strings.FieldsFunc(os.Getenv(key), func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "tag:") || tag == "tag:" {
			return nil, fmt.Errorf("%s must list tags starting with tag:, got %q", key, tag)
		}
	}
	return tags, nil
}

// nodeNotices reports problems with this node that the admin can fix: a name
// other than the one asked for, and a key that will expire.
func nodeNotices(self *ipnstate.PeerStatus, hostname string) []Notice {
	if self == nil {
		return nil
	}
	var notices []Notice
	name, _, _ := strings.Cut(self.DNSName, ".")
	if name != "" && !strings.EqualFold(name, dnsname.SanitizeHostname(hostname)) {
		notices = append(notices, Notice{Text: fmt.Sprintf("This page is at http://%s/ because %s is taken or this node was renamed.", name, hostname)})
	}
	if t := self.KeyExpiry; t != nil {
		notices = append(notices, Notice{
			Text:     fmt.Sprintf("This page goes offline on %s, when this node's key expires.", t.Format(time.DateOnly)),
			LinkText: "Disable key expiry for " + cmp.Or(name, hostname),
			URL:      "https://console.tailscale.com/admin/machines",
		})
	}
	return notices
}
