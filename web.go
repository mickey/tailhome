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

var page = template.Must(template.New("index.html").Funcs(template.FuncMap{"link": link}).Parse(indexHTML))

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
		"display": "standalone", "background_color": "#2f9e5b", "theme_color": "#2f9e5b",
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
		c.RefreshIfOlder(context.WithoutCancel(r.Context()), minManualRefresh)
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
		http.Error(w, "No tile matches "+name+". Open "+title+"/ for the tile list.", http.StatusNotFound)
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
	for _, g := range v.Groups {
		for _, e := range g.Entries {
			if _, ok := link(e.URL).(template.URL); ok && strings.EqualFold(e.Name, name) {
				return e.URL // an override such as javascript: fails link and never matches
			}
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
