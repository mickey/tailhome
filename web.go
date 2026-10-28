package main

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed index.html
var indexHTML string

//go:embed icons
var icons embed.FS

var page = template.Must(template.New("index.html").Funcs(template.FuncMap{
	"link": link, "previewBlocks": previewBlocks, "previewName": previewName,
	"bareHost": bareHost,
}).Parse(indexHTML))

type previewBlock struct {
	Set      string
	Host     string
	ShowHost bool
	Entries  []Entry
}

// The catalog already orders previews; keep each host-scoped set contiguous.
func previewBlocks(entries []Entry) []previewBlock {
	var blocks []previewBlock
	for _, e := range entries {
		n := len(blocks)
		if n == 0 || e.Set == "" || blocks[n-1].Set != e.Set || blocks[n-1].Host != e.Host {
			blocks = append(blocks, previewBlock{Set: e.Set, Host: e.Host})
		}
		blocks[len(blocks)-1].Entries = append(blocks[len(blocks)-1].Entries, e)
	}
	for i, b := range blocks { // Sorted input keeps equal set names adjacent.
		blocks[i].ShowHost = b.Set != "" && (i > 0 && blocks[i-1].Set == b.Set || i+1 < len(blocks) && blocks[i+1].Set == b.Set)
	}
	return blocks
}
func bareHost(name string) string {
	host, _, _ := strings.Cut(name, ":")
	return host
}

func previewName(e Entry) string {
	name := e.Label
	if e.Set != "" && !strings.Contains(name, e.Set) {
		name = e.Set + ", " + name
	}
	if e.Version != "" && !titleHasVersion(e.Label, e.Version) {
		name += ", version " + e.Version
	}
	return name + ", " + bareHost(e.Name)
}

func titleHasVersion(title, version string) bool {
	for word := range strings.FieldsFuncSeq(title, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) {
		if word == version {
			return true
		}
	}
	return false
}

// linkSchemes extends html/template's http, https and mailto with launcher links.
var linkSchemes = map[string]bool{
	"http": true, "https": true, "mailto": true,
	"vnc": true, "ssh": true, "rdp": true, "smb": true, "ftp": true, "sftp": true, "telnet": true,
}

// link marks allowlisted URLs safe; anything else (javascript:, data:, …) stays
// a plain string, which html/template replaces with #ZgotmplZ.
func link(raw string) any {
	if u, err := url.Parse(raw); err == nil && linkSchemes[u.Scheme] {
		return template.URL(raw)
	}
	return raw
}

const minManualRefresh = 10 * time.Second

func NewHandler(c *Catalog, title string) http.Handler {
	// The web app manifest lets phones add the page to the home screen as an app.
	manifest, err := json.Marshal(map[string]any{
		"name": title, "short_name": title, "id": "/", "start_url": "/", "scope": "/",
		"display": "standalone", "background_color": "#1c1c1a", "theme_color": "#1c1c1a",
		"icons": []map[string]string{
			{"src": "/icons/icon-192.png", "sizes": "192x192", "type": "image/png"},
			{"src": "/icons/icon-512.png", "sizes": "512x512", "type": "image/png"},
			{"src": "/icons/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	})
	if err != nil {
		panic(err)
	}
	address := "https://" + title + "." + c.DNSSuffix + "/"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := struct {
			Title   string
			Address string // the page's full https address, for other devices
			Local   bool
			View
		}{title, address, c.Local, c.View()}
		if err := page.Execute(w, data); err != nil {
			log.Printf("render: %v", err)
		}
	})
	// A refresh probes every device, so another site must not trigger one through a visitor's browser.
	mux.Handle("POST /refresh", http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A closed tab must not cancel probes and paint everything red.
		c.RefreshManualIfOlder(context.WithoutCancel(r.Context()), minManualRefresh)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})))
	mux.HandleFunc("GET /api/services.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, c.View())
	})
	// One flat array in page order, for dashboards that render a list,
	// such as Homepage's customapi dynamic-list.
	mux.HandleFunc("GET /api/list.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, c.View().all())
	})
	mux.HandleFunc("GET /manifest.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Write(manifest)
	})
	mux.HandleFunc("GET /icons/{name}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400") // embedded files have no modification time
		http.ServeFileFS(w, r, icons, "icons/"+r.PathValue("name"))
	})
	// Shortcuts for the address bar: home/go/jellyfin opens the jellyfin tile.
	goTo := func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		if u := shortcut(c.View(), name); u != "" {
			http.Redirect(w, r, u, http.StatusFound) // not cached: the tile's URL can change
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>App not listed</title><style>
:root { color-scheme: light dark; }
body { margin: 0; background: #f6f6f4; color: #1c1c1a; font: 15px/1.4 system-ui, sans-serif; }
main { max-width: 960px; margin: 0 auto; padding: 32px 20px; }
h1 { font-size: 22px; }
a { color: inherit; text-underline-offset: 2px; }
a:focus-visible { outline: 2px solid currentColor; outline-offset: 2px; }
@media (prefers-color-scheme: dark) { body { background: #151514; color: #ececea; } }
@media (max-width: 520px) { main { padding: 24px 14px; } }
</style><main><h1>App not listed</h1><p>This app or preview is not currently listed.</p><p><a href="/">Back to home</a></p></main></html>`))
	}
	mux.HandleFunc("GET /go/{name}", goTo)
	mux.HandleFunc("GET /go/{name}/{$}", goTo) // pasted with a trailing slash
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Icon requests to jsDelivr would otherwise carry the page's address, which names the tailnet.
		// nosniff keeps browsers from reading JSON or icons as HTML.
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

// shortcut returns the URL of the tile with this Service or device name, ignoring case.
// Only tile URLs qualify, so /go/ can't redirect anywhere else. A prefix
// doesn't match: a bookmark must not break when a similar name appears.
func shortcut(v View, name string) string {
	for _, e := range v.all() {
		if _, ok := link(e.URL).(template.URL); ok && strings.EqualFold(e.Name, name) {
			return e.URL // an override such as javascript: fails link and never matches
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}
