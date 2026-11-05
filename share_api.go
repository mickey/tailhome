package main

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"tailscale.com/client/tailscale/apitype"
)

type shareWhoIs interface {
	WhoIs(context.Context, string) (*apitype.WhoIsResponse, error)
}
type shareWeb struct {
	manager       *shareManager
	who           shareWhoIs
	admins        map[string]bool
	host, service string
}

func (s *shareWeb) admin(r *http.Request) (string, bool) {
	if s.service != "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !ip.IsLoopback() {
			return "", false
		}
		login, err := new(mime.WordDecoder).DecodeHeader(r.Header.Get("Tailscale-User-Login"))
		return login, err == nil && login != "" && s.admins[login]
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	caller, err := s.who.WhoIs(ctx, r.RemoteAddr)
	if err != nil || caller == nil || caller.Node == nil || caller.UserProfile == nil || len(caller.Node.Tags) != 0 {
		return "", false
	}
	login := caller.UserProfile.LoginName
	return login, s.admins[login]
}
func (s *shareWeb) snapshot(r *http.Request) SharePage {
	_, admin := s.admin(r)
	return s.manager.snapshot(admin)
}
func (s *shareWeb) decorate(v *View, page SharePage) {
	v.Previews = append([]Entry(nil), v.Previews...)
	v.Groups = append([]Group(nil), v.Groups...)
	tiles := make(map[string]ShareTile, len(page.Targets))
	for _, target := range page.Targets {
		tiles[target.entryKey] = target
	}
	attach := func(entries []Entry) {
		for i := range entries {
			if tile, ok := tiles[entries[i].Source+":"+entries[i].Name]; ok {
				entries[i].Share = &tile
			}
		}
	}
	attach(v.Previews)
	for i := range v.Groups {
		v.Groups[i].Entries = append([]Entry(nil), v.Groups[i].Entries...)
		attach(v.Groups[i].Entries)
	}
}
func (s *shareWeb) mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /share/status", func(w http.ResponseWriter, r *http.Request) { sharePrivateHeaders(w); writeJSON(w, s.snapshot(r)) })
	protection := http.NewCrossOriginProtection()
	for _, action := range []string{"create", "stop", "review", "dismiss"} {
		mux.Handle("POST /share/"+action, protection.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.mutate(w, r, action) })))
	}
}
func sharePrivateHeaders(w http.ResponseWriter) { w.Header().Set("Cache-Control", "no-store") }
func shareAPIError(w http.ResponseWriter, err error) {
	status, code, message := shareHTTPError(err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"code": code, "error": message})
}
func (s *shareWeb) mutate(w http.ResponseWriter, r *http.Request, action string) {
	sharePrivateHeaders(w)
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Host != s.host || r.Header.Get("Origin") != "https://"+s.host || r.Header.Get("X-Tailhome-Share-Request") != "1" || err != nil || media != "application/json" {
		shareAPIError(w, shareErr("authorization"))
		return
	}
	by, ok := s.admin(r)
	if !ok {
		shareAPIError(w, shareErr("authorization"))
		return
	}
	var request struct {
		TargetID    string `json:"targetID"`
		Revision    string `json:"revision"`
		Duration    string `json:"duration"`
		OperationID string `json:"operationID"`
		ID          string `json:"id"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Revision == "" || (action != "create" && request.ID == "") {
		shareAPIError(w, shareErr("invalid_request"))
		return
	}
	switch action {
	case "create":
		err = s.manager.create(request.TargetID, request.Revision, request.Duration, request.OperationID, by)
	case "stop":
		err = s.manager.stop(request.ID, request.Revision, by, "admin_stop")
	case "review":
		err = s.manager.review(request.ID, request.Revision, by)
	case "dismiss":
		err = s.manager.dismiss(request.ID, request.Revision)
	}
	if err != nil {
		shareAPIError(w, err)
		return
	}
	writeJSON(w, s.manager.snapshot(true))
}
