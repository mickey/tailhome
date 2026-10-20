package main

import (
	"cmp"
	"context"
	"iter"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	tsapi "tailscale.com/client/tailscale/v2"
)

// ServiceLister is implemented by *tsapi.ServicesResource and localServices.
type ServiceLister interface {
	List(ctx context.Context) ([]tsapi.Service, error)
}

// localServices lists the Services this node may reach, as its netmap reports
// them. It needs no API credentials but sees no tags or descriptions.
type localServices struct{ lc *local.Client }

func (l localServices) List(ctx context.Context) ([]tsapi.Service, error) {
	details, err := l.lc.GetServices(ctx)
	if err != nil {
		return nil, err
	}
	services := make([]tsapi.Service, 0, len(details))
	for _, d := range details {
		ports := make([]string, len(d.Ports))
		for i, p := range d.Ports {
			ports[i] = p.String()
		}
		services = append(services, tsapi.Service{Name: string(d.Name), DisplayName: d.DisplayName, Ports: ports})
	}
	return services, nil
}

type Status string

const (
	StatusUnknown Status = "unknown"
	StatusUp      Status = "up"
	StatusDown    Status = "down"
)

type Entry struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Group       string   `json:"group"`
	Icon        string   `json:"icon"`
	Host        string   `json:"host"`
	Ports       []string `json:"ports"`
	URL         string   `json:"url,omitempty"` // empty for non-web Services
	Status      Status   `json:"status"`
	Reason      string   `json:"reason,omitempty"` // why the last check failed

	order int
	probe []string // candidate URLs for a Service on an unusual port
}

type Group struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

type View struct {
	Groups  []Group   `json:"groups"`
	Other   []Entry   `json:"other"`
	Updated time.Time `json:"updated"`
	Error   string    `json:"error,omitempty"`
}

func (v View) all() []Entry {
	var all []Entry
	for _, g := range v.Groups {
		all = append(all, g.Entries...)
	}
	return append(all, v.Other...)
}

type Catalog struct {
	Services      ServiceLister
	OverridesPath string
	DNSSuffix     string
	Self          string // own Service, left off the page
	Prober        *Prober

	refreshMu   sync.Mutex        // serializes refreshes; guards detected, lastAttempt, warned
	detected    map[string]string // Service name → URL that once answered HTTP
	lastAttempt time.Time
	warned      map[string]bool

	mu   sync.RWMutex // guards view
	view View
}

func (c *Catalog) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		c.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (c *Catalog) View() View {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := c.view
	if v.Groups == nil { // before the first refresh: JSON [] rather than null
		v.Groups, v.Other = []Group{}, []Entry{}
	}
	return v
}

// Refresh rebuilds the View. On failure the previous entries stay, with the error.
func (c *Catalog) Refresh(ctx context.Context) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.refresh(ctx)
}

// RefreshIfOlder skips the refresh if the last attempt, failed or not, is
// younger than age, so repeated clicks don't hammer the API.
func (c *Catalog) RefreshIfOlder(ctx context.Context, age time.Duration) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if time.Since(c.lastAttempt) >= age {
		c.refresh(ctx)
	}
}

func (c *Catalog) refresh(ctx context.Context) {
	// Stamped after the work, so a click queued behind a slow refresh is still throttled.
	defer func() { c.lastAttempt = time.Now() }()
	view, err := c.build(ctx)
	if ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		log.Printf("refresh: %v", err)
		c.view.Error = err.Error()
		return
	}
	c.view = view
}

func (c *Catalog) warnOnce(key, format string, args ...any) {
	if c.warned[key] {
		return
	}
	if c.warned == nil {
		c.warned = map[string]bool{}
	}
	c.warned[key] = true
	log.Printf(format, args...)
}

func (c *Catalog) build(ctx context.Context) (View, error) {
	overrides, err := LoadOverrides(c.OverridesPath)
	if err != nil {
		return View{}, err
	}
	services, err := c.Services.List(ctx)
	if err != nil {
		return View{}, err
	}

	var entries []Entry
	selfFound := false
	for _, svc := range services {
		if svc.Name == c.Self {
			selfFound = true
			continue
		}
		name := strings.TrimPrefix(svc.Name, "svc:")
		o := overrides[name]
		if o.Hidden {
			continue
		}
		e := newEntry(name, c.DNSSuffix, svc, o)
		// A URL detected on an earlier refresh sticks, so a web Service that
		// stops answering stays a tile marked down instead of moving to Other.
		if u, ok := c.detected[name]; ok && e.probe != nil {
			e.URL = u
		}
		entries = append(entries, e)
	}
	if c.Self != "" && !selfFound {
		c.warnOnce("self", "warning: %s is not defined in the admin console (Services → Advertise → Define a Service), so the page is unreachable", c.Self)
	}
	if len(services) == 0 {
		c.warnOnce("empty", "warning: no Services to list: define some on the Services page of the admin console; without an OAuth client, also check that your policy lets this node reach them")
	}
	for name := range overrides {
		if !slices.ContainsFunc(services, func(s tsapi.Service) bool { return s.Name == "svc:"+name }) {
			c.warnOnce("override:"+name, "warning: overrides file names %s, which is not a Service", name)
		}
	}

	c.Prober.Check(ctx, entries)
	if err := ctx.Err(); err != nil { // shutting down: don't publish a view of failed probes
		return View{}, err
	}

	prev := map[string]Status{}
	for _, e := range c.View().all() {
		prev[e.Name] = e.Status
	}
	view := View{Groups: []Group{}, Other: []Entry{}, Updated: time.Now()}
	detected := map[string]string{}
	groups := map[string][]Entry{}
	for _, e := range entries {
		switch {
		case e.Status == StatusDown && prev[e.Name] != StatusDown:
			log.Printf("%s is down: %s", e.Name, e.Reason)
		case e.Status == StatusUp && prev[e.Name] == StatusDown:
			log.Printf("%s is back up", e.Name)
		}
		if e.URL == "" {
			view.Other = append(view.Other, e)
			continue
		}
		if e.probe != nil {
			detected[e.Name] = e.URL
		}
		groups[e.Group] = append(groups[e.Group], e)
	}
	c.detected = detected
	for name, es := range groups {
		sortEntries(es)
		view.Groups = append(view.Groups, Group{Name: name, Entries: es})
	}
	slices.SortFunc(view.Groups, func(a, b Group) int { return cmp.Compare(a.Name, b.Name) })
	sortEntries(view.Other)
	if len(entries) != len(prev) {
		log.Printf("listing %d Services", len(entries))
	}
	return view, nil
}

// newEntry applies overrides first, then the Service's own fields, then conventions.
func newEntry(name, suffix string, svc tsapi.Service, o Override) Entry {
	e := Entry{
		Name:        name,
		Label:       cmp.Or(o.Label, svc.DisplayName, titleCase(name)),
		Description: cmp.Or(o.Description, svc.Comment),
		Group:       cmp.Or(o.Group, groupFromTags(svc.Tags), "Services"),
		Icon:        iconURL(cmp.Or(o.Icon, name)),
		Host:        name + "." + suffix,
		Ports:       svc.Ports,
		Status:      StatusUnknown,
		order:       o.Order,
	}
	switch {
	case o.URL != "":
		e.URL = o.URL
	case o.Web != nil && !*o.Web: // forced non-web: TCP check only
	case servesPort(svc.Ports, 443):
		e.URL = "https://" + e.Host + "/"
	case servesPort(svc.Ports, 80):
		e.URL = "http://" + e.Host + "/"
	default:
		base := e.Host
		if port, ok := firstTCPPort(svc.Ports); ok {
			base += ":" + port
		}
		e.probe = []string{"https://" + base + "/", "http://" + base + "/"}
		if o.Web != nil && *o.Web { // a tile even before it ever answers
			e.URL = e.probe[0]
		}
	}
	if e.Ports == nil {
		e.Ports = []string{}
	}
	return e
}

// tcpRanges parses "tcp:443", "tcp:8000-8010" and "tcp:*"; other protocols
// and values such as "do-not-validate" are skipped.
func tcpRanges(ports []string) iter.Seq2[int, int] {
	return func(yield func(int, int) bool) {
		for _, p := range ports {
			spec, ok := strings.CutPrefix(p, "tcp:")
			if !ok {
				continue
			}
			if spec == "*" {
				if !yield(1, 65535) {
					return
				}
				continue
			}
			lo, hi, isRange := strings.Cut(spec, "-")
			if !isRange {
				hi = lo
			}
			l, err1 := strconv.Atoi(lo)
			h, err2 := strconv.Atoi(hi)
			if err1 == nil && err2 == nil && !yield(l, h) {
				return
			}
		}
	}
}

func servesPort(ports []string, port int) bool {
	for lo, hi := range tcpRanges(ports) {
		if lo <= port && port <= hi {
			return true
		}
	}
	return false
}

// firstTCPPort returns the lowest port of the first explicit TCP range.
func firstTCPPort(ports []string) (string, bool) {
	for lo, hi := range tcpRanges(ports) {
		if lo != 1 || hi != 65535 {
			return strconv.Itoa(lo), true
		}
	}
	return "", false
}

// groupFromTags turns tag:home-lab into "Home Lab".
func groupFromTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return titleCase(strings.TrimPrefix(tags[0], "tag:"))
}

func titleCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

func iconURL(icon string) string {
	if strings.Contains(icon, "/") {
		return icon
	}
	return "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/svg/" + icon + ".svg"
}

func sortEntries(es []Entry) {
	slices.SortFunc(es, func(a, b Entry) int {
		return cmp.Or(cmp.Compare(a.order, b.order), cmp.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label)))
	})
}
