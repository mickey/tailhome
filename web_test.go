package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func renderWebView(t *testing.T, v View, enabled bool) string {
	t.Helper()
	v.PreviewEnabled = enabled
	return get(NewHandler(&Catalog{view: v}, "home"), "/").Body.String()
}

func webPreview(name, label, set, version string) Entry {
	return Entry{Name: name, Label: label, Host: strings.Split(name, ":")[0] + ".example", URL: "https://" + name + "/", Temporary: true, Set: set, Version: version, Check: "tls", Status: StatusUp}
}

func TestWebPreviews(t *testing.T) {
	a := webPreview("devbox:8443", "A · Short hero", "Landing redesign", "A")
	b := webPreview("devbox:8444", "Screenshot-led hero", "Landing redesign", "B")
	b.Description = "A screenshot and pricing first."
	single := webPreview("devbox:8445", strings.Repeat("x", 60), "", "Q")
	single.Unverified = true
	other := webPreview("laptop:8443", "Another hero", "Landing redesign", "")
	html := renderWebView(t, View{Previews: []Entry{single, a, b, other}, Groups: []Group{{Name: "Previews", Entries: []Entry{{Name: "app", Label: "Permanent", URL: "https://app/", Status: StatusUp}}}}}, true)
	for _, want := range []string{"id=\"previews-title\"", "No description provided", "HTTPS certificate could not be verified", "HTTPS connection succeeded at last refresh; pages and app health not checked", "class=\"version\" aria-hidden=\"true\">A", "class=\"version\" aria-hidden=\"true\">Q", "2 versions", "1 version", "<h3", ">devbox</span>", ">laptop</span>", "Previews (apps)", "devbox:8443", "class=\"dot up\"", "aria-label=\"Landing redesign, A · Short hero, devbox\"", `aria-describedby="preview-desc-devbox:8445 preview-origin-devbox:8445 preview-trust-devbox:8445"`, `id="preview-desc-devbox:8445"`, `id="preview-origin-devbox:8445"`, `id="preview-trust-devbox:8445"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Index(html, "id=\"previews-title\"") > strings.Index(html, "class=\"apps-start\"") {
		t.Error("previews not first")
	}
	if strings.Contains(html, "No apps to show") {
		t.Error("onboarding shown with content")
	}
}

func TestWebPermanentTLSStatus(t *testing.T) {
	for _, tc := range []struct{ check, status, want string }{{"tls", "up", "HTTPS connection succeeded at last refresh; pages and app health not checked"}, {"tls", "down", "HTTPS connection failed at last refresh; pages and app health not checked"}, {"http", "up", "class=\"dot up\""}} {
		t.Run(tc.check+tc.status, func(t *testing.T) {
			html := renderWebView(t, View{Groups: []Group{{Name: "Apps", Entries: []Entry{{Label: "App", URL: "https://app/", Check: tc.check, Status: Status(tc.status)}}}}}, false)
			if !strings.Contains(html, tc.want) {
				t.Errorf("missing %q", tc.want)
			}
			if tc.check == "tls" && strings.Contains(html, "class=\"dot ") {
				t.Error("TLS endpoint has HTTP dot")
			}
		})
	}
}

// Phones have no hover, so a down tile must show why in its text.
func TestWebDownReasonIsVisible(t *testing.T) {
	down := Entry{Label: "App", URL: "https://app/", Status: StatusDown, Reason: "HTTP 503"}
	stale := Entry{Label: "Was down", URL: "https://old/", Status: StatusUp, Reason: "HTTP 503"}
	other := Entry{Label: "SSH", Copy: "ssh host", Status: StatusDown, Reason: "Device offline"}
	html := renderWebView(t, View{Groups: []Group{{Name: "Apps", Entries: []Entry{down, stale}}}, Other: []Entry{other}}, false)
	if strings.Count(html, `<div class="sub reason" title="HTTP 503">HTTP 503</div>`) != 1 {
		t.Error("down tile reason not shown exactly once")
	}
	if !strings.Contains(html, `<div class="sub reason" title="Device offline">Device offline</div>`) {
		t.Error("down Other row reason not shown")
	}
}

func TestWebEmptyStates(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		v                              View
		enabled, empty, hint, previews bool
	}{
		{name: "empty", empty: true},
		{name: "preview enabled empty", enabled: true, empty: true, hint: true},
		{name: "failure", v: View{Error: "offline"}},
		{name: "apps only", v: View{Groups: []Group{{Name: "Previews", Entries: []Entry{{Label: "App"}}}}}},
		{name: "previews only", v: View{Previews: []Entry{webPreview("devbox:8443", "Preview", "", "")}, Groups: []Group{}}, previews: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			html := renderWebView(t, tc.v, tc.enabled)
			for _, c := range []struct {
				text string
				want bool
			}{{"No apps to show", tc.empty}, {"Previews are hidden when an HTTPS connection fails or their app stops.", tc.hint}, {"id=\"previews-title\"", tc.previews}, {"Refresh failed: offline", tc.v.Error != ""}, {"Badges such as A, B and C mark alternatives, not a ranking.", tc.enabled || tc.previews}, {"ask whoever published it.", tc.enabled || tc.previews}} {
				if strings.Contains(html, c.text) != c.want {
					t.Errorf("%q presence != %v", c.text, c.want)
				}
			}
			if strings.Contains(html, "Previews (apps)") {
				t.Error("renamed group without previews")
			}
		})
	}
}

func TestWebHelp(t *testing.T) {
	html := get(NewHandler(&Catalog{}, "team-home"), "/").Body.String()
	for _, want := range []string{
		"team-home/go/&lt;name&gt;",
		"sudo tailscale set --operator=$USER",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("help missing %q", want)
		}
	}
}

func TestWebJSONAndShortcuts(t *testing.T) {
	preview := webPreview("devbox:8443", "Preview", "Landing", "A")
	app := Entry{Name: "devbox", Label: "App", URL: "https://devbox/"}
	c := &Catalog{view: View{Previews: []Entry{preview}, Groups: []Group{{Name: "Apps", Entries: []Entry{app}}}}}
	h := NewHandler(c, "home")
	t.Run("list order", func(t *testing.T) {
		w := get(h, "/api/list.json")
		var entries []Entry
		if err := json.Unmarshal(w.Body.Bytes(), &entries); err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 || entries[0].Name != preview.Name || entries[1].Name != app.Name {
			t.Fatalf("wrong order: %+v", entries)
		}
	})
	for _, tc := range []struct{ path, target string }{{"/go/devbox:8443", preview.URL}, {"/go/devbox%3A8443/", preview.URL}, {"/go/DEVBOX:8443", preview.URL}, {"/go/devbox", app.URL}, {"/go/devbox:844", ""}} {
		t.Run(tc.path, func(t *testing.T) {
			w := get(h, tc.path)
			if tc.target != "" {
				if w.Code != http.StatusFound || w.Header().Get("Location") != tc.target {
					t.Fatalf("redirect: %d %s", w.Code, w.Header().Get("Location"))
				}
			} else if w.Code != 404 || !strings.Contains(w.Body.String(), "This app or preview is not currently listed.") || !strings.Contains(w.Body.String(), `<main><h1>App not listed</h1>`) || !strings.Contains(w.Body.String(), `<a href="/">Back to home</a>`) || !strings.Contains(w.Body.String(), "prefers-color-scheme: dark") {
				t.Fatalf("missing helpful 404: %d %s", w.Code, w.Body.String())
			}
		})
	}
	if got := shortcut(View{Previews: []Entry{preview}}, "devbox"); got != "" {
		t.Errorf("bare name picked preview: %s", got)
	}
	{
		h = NewHandler(&Catalog{}, "home")
		w := get(h, "/api/services.json")
		if !strings.Contains(w.Body.String(), `"previews":[]`) {
			t.Errorf("null previews: %s", w.Body.String())
		}
	}
}

func TestWebPreviewAccessibleName(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"A · Short hero", "Landing, A · Short hero, devbox"},
		{"Another hero", "Landing, Another hero, version A, devbox"},
		{"Landing hero", "Landing hero, version A, devbox"},
	} {
		t.Run(tc.title, func(t *testing.T) {
			e := webPreview("devbox:8443", tc.title, "Landing", "A")
			if got := previewName(e); got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}
