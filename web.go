package main

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"time"
)

//go:embed index.html
var assets embed.FS

var page = template.Must(template.New("index.html").Funcs(template.FuncMap{"link": link}).ParseFS(assets, "index.html"))

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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("refresh") {
			// A closed tab must not cancel probes and paint everything red.
			c.RefreshIfOlder(context.WithoutCancel(r.Context()), minManualRefresh)
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := struct {
			Title string
			Local bool
			View
		}{title, c.Local, c.View()}
		if err := page.Execute(w, data); err != nil {
			log.Printf("render: %v", err)
		}
	})
	mux.HandleFunc("GET /api/services.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, c.View())
	})
	// One flat array in page order, for dashboards that render a list,
	// such as Homepage's customapi dynamic-list.
	mux.HandleFunc("GET /api/list.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, c.View().all())
	})
	return mux
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}
