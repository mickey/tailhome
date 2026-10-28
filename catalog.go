package main

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	tsapi "tailscale.com/client/tailscale/v2"
	"tailscale.com/tailcfg"
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

// Notice is a setup problem worth showing on the page, with a link to fix it.
type Notice struct {
	Text     string `json:"text"`
	LinkText string `json:"linkText,omitempty"`
	URL      string `json:"url,omitempty"`
}

type Entry struct {
	Name        string   `json:"name"`
	Source      string   `json:"source"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Group       string   `json:"group"`
	Icon        string   `json:"icon"`
	Host        string   `json:"host"`
	Ports       []string `json:"ports"`
	URL         string   `json:"url,omitempty"`  // empty for non-web Services
	Copy        string   `json:"copy,omitempty"` // what a click copies under Other
	Status      Status   `json:"status"`
	Reason      string   `json:"reason,omitempty"` // why the last check failed
	// Temporary entries are previews: listed under Previews while they answer,
	// hidden instead of shown down.
	Temporary bool `json:"temporary,omitempty"`
	// Set and Version come from a preview card: versions of one thing, side by side.
	Set     string `json:"set,omitempty"`
	Version string `json:"version,omitempty"`
	// Check names what Status measured on a device: "tls" for a handshake
	// only, "http" for an answer to GET /. Empty on Services.
	Check string `json:"check,omitempty"`
	// Unverified marks a device admitted under TAILHOME_DEVICE_INSECURE_TLS_TAGS
	// whose certificate failed verification.
	Unverified bool `json:"unverified,omitempty"`

	order  int
	nodeID tailcfg.StableNodeID
	probe  []string // candidate URLs for a Service on an unusual port
}

type Group struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

type View struct {
	Previews       []Entry   `json:"previews"` // sorted for display: single previews, then sets
	Groups         []Group   `json:"groups"`
	Other          []Entry   `json:"other"`
	Updated        time.Time `json:"updated"`
	Error          string    `json:"error,omitempty"`
	Notices        []Notice  `json:"notices,omitempty"`
	PreviewEnabled bool      `json:"-"`
}

// all flattens the page in order, one Entry per tile: a web Service's
// copy row under Other repeats its tile and is skipped.
func (v View) all() []Entry {
	all := []Entry{} // never nil: /api/list.json encodes [] rather than null
	seen := map[string]bool{}
	for _, e := range v.Previews {
		seen[e.Name] = true
	}
	all = append(all, v.Previews...)
	for _, g := range v.Groups {
		for _, e := range g.Entries {
			seen[e.Name] = true
		}
		all = append(all, g.Entries...)
	}
	for _, e := range v.Other {
		if !seen[e.Name] {
			all = append(all, e)
		}
	}
	return all
}

type Catalog struct {
	Services        ServiceLister
	OverridesPath   string
	DNSSuffix       string
	Self            string // own Service, left off the page
	Prober          *Prober
	Local           bool                           // listing from the netmap, without API credentials
	Notices         func(context.Context) []Notice // setup problems, rechecked on every refresh; may be nil
	Icons           *Icons                         // matches names to icons; nil uses names as-is
	Devices         DeviceLister                   // nil: discovery off
	DeviceTags      []string                       // TAILHOME_DEVICE_TAGS; empty tries every member
	InsecureTLSTags []string                       // TAILHOME_DEVICE_INSECURE_TLS_TAGS; members with one skip certificate verification
	DeviceProber    *DeviceProber                  // required when Devices is set
	PreviewPorts    []uint16

	refreshMu      sync.Mutex        // serializes refreshes; guards discovery state, lastAttempt, warned
	detected       map[string]string // Service name → URL that once answered HTTP
	lastAttempt    time.Time
	warned         map[string]bool
	admitted       map[endpointID]Device // endpoints that once answered; kept until ineligible
	endpoints      map[endpointID]*endpointState
	cursor         string // last candidate DNS name and port started; resume after this tuple
	cursorPort     uint16
	devicesFailing bool          // last listing failed; logged on transition
	scanBudget     time.Duration // zero uses the ten-second candidate scan budget

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
		v.Previews, v.Groups, v.Other = []Entry{}, []Group{}, []Entry{}
	}
	return v
}

// Refresh rebuilds the View. On failure the previous entries stay, with the error.
func (c *Catalog) Refresh(ctx context.Context) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	c.refresh(ctx, false)
}

// RefreshManualIfOlder runs a manual refresh, which also rereads preview
// cards, unless the last attempt, failed or not, is younger than age, so
// repeated clicks don't hammer the API.
func (c *Catalog) RefreshManualIfOlder(ctx context.Context, age time.Duration) {
	c.refreshMu.Lock()
	defer c.refreshMu.Unlock()
	if time.Since(c.lastAttempt) >= age {
		c.refresh(ctx, true)
	}
}

func (c *Catalog) refresh(ctx context.Context, manual bool) {
	// Stamped after the work, so a click queued behind a slow refresh is still throttled.
	defer func() { c.lastAttempt = time.Now() }()
	view, err := c.build(ctx, manual)
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

func (c *Catalog) build(ctx context.Context, manual bool) (View, error) {
	overrides, err := LoadOverrides(c.OverridesPath)
	if err != nil {
		return View{}, err
	}
	services, err := c.Services.List(ctx)
	if err != nil {
		return View{}, err
	}
	if c.Icons != nil {
		c.Icons.load(ctx)
	}

	var entries []Entry
	selfFound := false
	reserved := make(map[string]bool, len(services))
	for _, svc := range services {
		name := strings.TrimPrefix(svc.Name, "svc:")
		reserved[name] = true
		if overrides[name].Temporary != nil {
			return View{}, fmt.Errorf("override %s: temporary applies only to device endpoints, not Services", name)
		}
		if svc.Name == c.Self {
			selfFound = true
			continue
		}
		o := overrides[name]
		if o.Hidden {
			continue
		}
		e := newEntry(name, c.DNSSuffix, svc, o)
		if o.Icon == "" && c.Icons != nil {
			e.Icon = c.Icons.url(name)
		}
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

	c.Prober.Check(ctx, entries) // Services only: the insecure Service client must never vouch for a device, so device entries come after, already checked
	deviceEntries, members, err := c.refreshDevices(ctx, reserved, overrides, manual)
	entries = append(entries, deviceEntries...)
	if err != nil {
		return View{}, fmt.Errorf("devices: listing failed: %w", err)
	}
	if err := ctx.Err(); err != nil { // shutting down: don't publish a view of failed probes
		return View{}, err
	}
	for name := range overrides {
		if !reserved[name] && !members[name] {
			c.warnOnce("override:"+name, "warning: overrides file names %s, which is not a Service or device", name)
		}
	}

	prev := map[string]Status{}
	permanent := 0
	for _, e := range c.View().all() {
		prev[e.Name] = e.Status
		if !e.Temporary {
			permanent++
		}
	}
	view := View{Previews: []Entry{}, Groups: []Group{}, Other: []Entry{}, Updated: time.Now(), PreviewEnabled: c.Devices != nil && len(c.PreviewPorts) != 0}
	for _, state := range c.endpoints {
		view.PreviewEnabled = view.PreviewEnabled || state.temporary || state.priorPreview
	}
	if c.Notices != nil {
		view.Notices = c.Notices(ctx)
	}
	detected := map[string]string{}
	groups := map[string][]Entry{}
	currentPermanent := 0
	for _, e := range entries {
		if e.Temporary {
			if e.Status == StatusUp {
				view.Previews = append(view.Previews, e)
			}
			continue
		}
		currentPermanent++
		switch {
		case e.Status == StatusDown && prev[e.Name] != StatusDown:
			log.Printf("%s is down: %s", e.Name, e.Reason)
		case e.Status == StatusUp && prev[e.Name] == StatusDown:
			log.Printf("%s is back up", e.Name)
		}
		if e.URL == "" {
			if e.Copy == "" {
				e.Copy = copyValue(e.Host, e.Ports)
			}
			view.Other = append(view.Other, e)
			continue
		}
		if e.Copy != "" { // a web Service with a `copy` override also gets a row under Other
			row := e
			row.URL, row.Description = "", "" // the tile already shows the description
			view.Other = append(view.Other, row)
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
	sortPreviews(view.Previews)
	if currentPermanent != permanent {
		log.Printf("listing %d apps", currentPermanent)
	}
	return view, nil
}

// newEntry applies overrides first, then the Service's own fields, then conventions.
func newEntry(name, suffix string, svc tsapi.Service, o Override) Entry {
	e := Entry{
		Name:        name,
		Source:      "service",
		Label:       cmp.Or(o.Label, svc.DisplayName, titleCase(name)),
		Description: cmp.Or(o.Description, svc.Comment),
		Group:       cmp.Or(o.Group, groupFromTags(svc.Tags), "Apps"),
		Icon:        iconURL(cmp.Or(o.Icon, name)),
		Host:        name + "." + suffix,
		Ports:       svc.Ports,
		Status:      StatusUnknown,
		Copy:        o.Copy,
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

// copyValue is what a click copies for a non-web Service: a connection
// string for well-known ports, else the hostname.
func copyValue(host string, ports []string) string {
	port, n := 0, 0
	for lo, hi := range tcpRanges(ports) {
		if lo != hi {
			return host
		}
		port, n = lo, n+1
	}
	if n != 1 {
		return host
	}
	switch port {
	case 22:
		return "ssh " + host
	case 5900:
		return "vnc://" + host
	case 5432:
		return "postgres://" + host + ":5432/"
	case 3306:
		return "mysql://" + host + ":3306/"
	case 6379:
		return "redis://" + host + ":6379"
	case 27017:
		return "mongodb://" + host + ":27017/"
	}
	return host
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
	return iconBase + icon + ".svg"
}

func sortEntries(es []Entry) {
	slices.SortFunc(es, compareEntries)
}

func compareEntries(a, b Entry) int {
	return cmp.Or(cmp.Compare(a.order, b.order), cmp.Compare(strings.ToLower(a.Label), strings.ToLower(b.Label)), cmp.Compare(a.Name, b.Name))
}

func sortPreviews(entries []Entry) {
	rank := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	slices.SortFunc(entries, func(a, b Entry) int {
		if n := cmp.Compare(rank(a.Set != ""), rank(b.Set != "")); n != 0 || a.Set == "" {
			return cmp.Or(n, compareEntries(a, b)) // Singles first, by the app convention.
		}
		return cmp.Or(cmp.Compare(a.Set, b.Set), cmp.Compare(a.Host, b.Host), cmp.Compare(a.nodeID, b.nodeID),
			cmp.Compare(rank(a.Version == ""), rank(b.Version == "")), cmp.Compare(a.Version, b.Version), compareEntries(a, b))
	})
}
