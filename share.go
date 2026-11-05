package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const shareTag = "tag:tailhome-share"
const shareOAuthMessage = "Sharing needs the existing tailhome OAuth client. Ask the operator to configure its credentials."
const shareRevisionMessage = "This app or share changed. Reload its details before sharing."
const sharePermissionMessage = "You no longer have permission to change public sharing. Reload this page."
const shareStorageMessage = "Sharing state could not be read. No public listeners were started. Ask the operator to repair the state."
const shareStopStorageMessage = "Access stopped on this process, but Stop could not be saved. Keep this process running and repair storage before restarting."
const shareWriteMessage = "Sharing state could not be saved. Repair storage before restarting."

type shareConfig struct {
	Enabled bool
	Admins  map[string]bool
}

func parseShareConfig(admins string) (shareConfig, error) {
	c := shareConfig{Admins: map[string]bool{}}
	admins = strings.TrimSpace(admins)
	if admins == "" {
		return c, nil
	}
	for _, name := range strings.Split(admins, ",") {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "*?") {
			return c, errors.New("TAILHOME_SHARE_ADMINS must contain exact nonempty Tailscale login names")
		}
		c.Admins[name] = true
	}
	c.Enabled = true
	return c, nil
}

type ShareBinding struct {
	Kind     string `json:"kind"`
	Service  string `json:"service,omitempty"`
	NodeID   string `json:"nodeID,omitempty"`
	FQDN     string `json:"fqdn"`
	Scheme   string `json:"scheme"`
	BasePath string `json:"basePath"`
	Port     uint16 `json:"port"`
}
type ShareRecord struct {
	ID             string       `json:"id"`
	Revision       string       `json:"revision"`
	OperationID    string       `json:"operationID,omitempty"`
	TargetID       string       `json:"targetID"`
	Label          string       `json:"label"`
	Hostname       string       `json:"hostname"`
	URL            string       `json:"url,omitempty"`
	Binding        ShareBinding `json:"-"`
	State          string       `json:"state"`
	CreatedAt      time.Time    `json:"createdAt"`
	CreatedBy      string       `json:"createdBy,omitempty"`
	ExpiresAt      *time.Time   `json:"expiresAt"`
	LastReviewedAt time.Time    `json:"lastReviewedAt"`
	LastReviewedBy string       `json:"lastReviewedBy,omitempty"`
	NextReviewAt   time.Time    `json:"nextReviewAt"`
	StoppedAt      *time.Time   `json:"stoppedAt,omitempty"`
	StoppedBy      string       `json:"stoppedBy,omitempty"`
	Reason         string       `json:"reason,omitempty"`
	CleanupPending bool         `json:"cleanupPending"`
	Phase          string       `json:"-"`
	StartedAt      time.Time    `json:"-"`
}

func (r *ShareRecord) live() bool { return r.State == "starting" || r.State == "public" }

type shareView struct {
	ShareRecord
	Phase      string    `json:"phase,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	Message    string    `json:"message"`
	Public     bool      `json:"public"`
	CanDismiss bool      `json:"canDismiss"`
	ReviewDue  bool      `json:"reviewDue"`
}
type ShareTile struct {
	TargetID string `json:"targetID"`
	Revision string `json:"revision"`
	Origin   string `json:"origin"`
	Reason   string `json:"reason"`
	Label    string `json:"label"`
	entryKey string
	hidden   bool
}
type SharePage struct {
	Admin   bool        `json:"admin"`
	Records []shareView `json:"records"`
	Targets []ShareTile `json:"targets"`
	Error   string      `json:"error,omitempty"`
}
type shareTarget struct {
	Tile     ShareTile
	Binding  ShareBinding
	eligible bool
}
type shareRuntime interface {
	Prepare(context.Context, func()) (string, net.Listener, func(context.Context) ([]netip.Addr, error), error)
	Dial(context.Context, string, string) (net.Conn, error)
	Logout(context.Context) error
	Close() error
}
type shareInstance struct {
	cancel   context.CancelFunc
	runtime  shareRuntime
	gateway  *shareGateway
	listener net.Listener
	timer    *time.Timer
	done     chan struct{}
	cleaning bool
	retries  int
}
type shareManager struct {
	mu               sync.Mutex
	store            *shareStore
	records          map[string]*ShareRecord
	targets          map[string]shareTarget
	undetected       map[string]bool
	active           map[string]*shareInstance
	factory          func(ShareRecord) (shareRuntime, error)
	now              func() time.Time
	afterFunc        func(time.Duration, func()) *time.Timer
	removeNodeDir    func(string) error
	err, unavailable string
	closing          bool
	workers          sync.WaitGroup
}

func randomShareID() string {
	var bytes [10]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic("sharing random source unavailable")
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes[:]))
}
func targetForEntry(e Entry, suffix, self string) shareTarget {
	t := shareTarget{Tile: ShareTile{Label: e.Label, entryKey: e.Source + ":" + e.Name, hidden: e.hidden}}
	binding := ShareBinding{Kind: e.Source}
	u, err := url.Parse(e.URL)
	switch {
	case e.Temporary:
		t.Tile.Reason = "Sharing previews is not available yet."
	case e.Unverified:
		t.Tile.Reason = "This app cannot be shared: its HTTPS certificate is not verified."
	case err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http"):
		t.Tile.Reason = "This app cannot be shared: only web apps are supported."
	}
	if u == nil {
		u = &url.URL{}
	}
	binding.Scheme = u.Scheme
	binding.FQDN = strings.ToLower(u.Hostname())
	binding.BasePath = u.Path
	if binding.BasePath == "" {
		binding.BasePath = "/"
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if binding.Scheme == "https" {
			port = "443"
		}
	}
	number, _ := strconv.ParseUint(port, 10, 16)
	binding.Port = uint16(number)
	if binding.Kind == "service" {
		binding.Service = "svc:" + e.Name
	} else {
		binding.NodeID = string(e.nodeID)
	}
	if t.Tile.Reason == "" && (binding.FQDN != strings.ToLower(e.Host) || !strings.HasSuffix(binding.FQDN, "."+suffix) || binding.Port == 0 || !servesPort(e.Ports, int(binding.Port)) || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (binding.Kind != "service" && binding.Kind != "device") || (binding.Kind == "device" && binding.NodeID == "") || (binding.Kind == "service" && binding.Service == self) || strings.HasPrefix(binding.FQDN, "public-")) {
		t.Tile.Reason = "This app cannot be shared: the destination is not an approved advertised app endpoint."
	}
	if shareUnsafePath(u.EscapedPath()) {
		t.Tile.Reason = "This app cannot be shared: its base path is unsafe."
	}
	t.eligible = t.Tile.Reason == ""
	if e.hidden && t.eligible {
		t.Tile.Reason = "This app cannot be shared while its tile is hidden."
	}
	t.Tile.TargetID = targetIDForBinding(binding)
	t.Tile.Revision = t.Tile.TargetID
	t.Tile.Origin = e.URL
	t.Binding = binding
	return t
}
func targetIDForBinding(binding ShareBinding) string {
	canonical := strings.Join([]string{binding.Kind, binding.Service, binding.NodeID, binding.FQDN, binding.Scheme, strconv.Itoa(int(binding.Port)), binding.BasePath}, "\x00")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:16])
}
func newShareManager(root string, enabled bool, factory func(ShareRecord) (shareRuntime, error)) (*shareManager, error) {
	if !enabled {
		if _, err := os.Stat(filepath.Join(root, "shares")); errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
	}
	store, err := openShareStore(root)
	if err != nil {
		return nil, err
	}
	m := &shareManager{store: store, records: store.ledger.Records, targets: map[string]shareTarget{}, active: map[string]*shareInstance{}, factory: factory, now: time.Now}
	if !enabled {
		m.mu.Lock()
		defer m.mu.Unlock()
		dirty := false
		for _, r := range m.records {
			if r.live() || r.CleanupPending {
				r.State = "stopped"
				r.Reason = "sharing_disabled"
				r.CleanupPending = true
				r.Revision = randomShareID()
				dirty = true
			}
			if normalShareEnd(r) {
				delete(m.records, r.ID)
				dirty = true
			}
		}
		if dirty {
			err = m.saveLocked()
		}
		pending := false
		for _, r := range m.records {
			if r.CleanupPending {
				pending = true
				m.startCleanupLocked(r)
			}
		}
		if pending {
			return m, err
		}
		return nil, err
	}
	for _, r := range m.records {
		if r.live() {
			r.State = "starting"
		}
	}
	return m, nil
}
func (m *shareManager) saveLocked() error {
	if err := m.store.Save(m.records); err != nil {
		m.err = shareWriteMessage
		return &shareError{code: "storage", cause: err}
	}
	m.err = ""
	return nil
}
func (m *shareManager) setTargetsLocked(v View, suffix, self string) {
	m.targets = map[string]shareTarget{}
	m.undetected = map[string]bool{}
	entries := append(v.all(), v.hidden...)
	for _, entry := range entries {
		if entry.Source == "service" && entry.probe != nil && entry.URL == "" {
			m.undetected["svc:"+entry.Name] = true
		}
		target := targetForEntry(entry, suffix, self)
		if target.Tile.Reason == "" && m.unavailable != "" {
			target.Tile.Reason = m.unavailable
		}
		m.targets[target.Tile.TargetID] = target
	}
}
func (m *shareManager) snapshot(admin bool) SharePage {
	m.mu.Lock()
	defer m.mu.Unlock()
	page := SharePage{Admin: admin, Records: []shareView{}, Targets: []ShareTile{}}
	if admin {
		page.Error = m.err
		if page.Error == "" {
			page.Error = m.unavailable
		}
	}
	for _, r := range m.records {
		copy := shareView{ShareRecord: *r, Phase: r.Phase, StartedAt: r.StartedAt, Message: shareMessage(*r), Public: r.State == "public", CanDismiss: !r.live() && !r.CleanupPending, ReviewDue: r.live() && !m.now().Before(r.NextReviewAt)}
		copy.OperationID = ""
		if !admin {
			copy.CreatedBy = ""
			copy.LastReviewedBy = ""
			copy.StoppedBy = ""
			copy.Reason = ""
			if copy.State == "failed" {
				copy.Message = shareErrorMessage("setup_failed")
			}
		}
		page.Records = append(page.Records, copy)
	}
	sort.Slice(page.Records, func(i, j int) bool {
		if page.Records[i].CreatedAt.Equal(page.Records[j].CreatedAt) {
			return page.Records[i].ID < page.Records[j].ID
		}
		return page.Records[i].CreatedAt.Before(page.Records[j].CreatedAt)
	})
	for _, target := range m.targets {
		if !target.Tile.hidden {
			page.Targets = append(page.Targets, target.Tile)
		}
	}
	sort.Slice(page.Targets, func(i, j int) bool { return page.Targets[i].TargetID < page.Targets[j].TargetID })
	return page
}
func shareMessage(r ShareRecord) string {
	switch r.State {
	case "starting":
		if r.Phase == "certificate" {
			return "Getting a certificate for the public address."
		}
		return "Starting public sharing. Access may become public before setup finishes."
	case "public":
		if r.ExpiresAt == nil {
			return "Public until an admin stops it."
		}
		return "Public until " + r.ExpiresAt.Format(time.RFC1123) + "."
	case "failed":
		return shareErrorMessage(r.Reason)
	case "cleanup":
		return "Public access stopped. Removing the public node is still pending."
	case "expired":
		if r.ExpiresAt != nil {
			return "Public sharing expired at " + r.ExpiresAt.Format(time.RFC1123) + "."
		}
	case "stopped":
		if r.Reason == "sharing_disabled" {
			return "Sharing disabled on this instance. Re-enable sharing and confirm a new share to publish again."
		}
		if r.Reason == "target_changed" {
			return "The app's address or port changed; sharing stopped. Confirm a new share."
		}
		return "Public sharing stopped."
	}
	return shareErrorMessage("setup_failed")
}
