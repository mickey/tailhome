package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/tailcfg"
)

// Device is a netmap peer's identity, name, address, tags and presence.
type Device struct {
	ID      tailcfg.StableNodeID
	DNSName string
	IP      netip.Addr
	Tags    []string
	Online  bool
}

// DeviceLister supplies the current netmap peers for device discovery.
type DeviceLister interface {
	Devices(context.Context) ([]Device, error)
}
type localDevices struct{ lc *local.Client }

func (l localDevices) Devices(ctx context.Context) ([]Device, error) {
	status, err := l.lc.Status(ctx)
	if err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(status.Peer))
	for _, peer := range status.Peer {
		if !peer.InNetworkMap {
			continue
		}
		d := Device{ID: peer.ID, DNSName: strings.TrimSuffix(peer.DNSName, "."), Online: peer.Online}
		if len(peer.TailscaleIPs) != 0 {
			d.IP = peer.TailscaleIPs[0]
		}
		if peer.Tags != nil {
			d.Tags = peer.Tags.AsSlice()
		}
		devices = append(devices, d)
	}
	return devices, nil
}

type endpointID struct {
	node tailcfg.StableNodeID
	port uint16
}

// DeviceProber checks listeners dialled by IP so a name that leaves MagicDNS
// can never resolve anywhere else.
type DeviceProber struct {
	dial    func(context.Context, string, string) (net.Conn, error)
	roots   *x509.CertPool
	timeout time.Duration
}

type deviceCheck struct {
	Device
	port       uint16
	insecure   bool
	candidate  bool // not yet admitted; only candidates consume the scan-start budget
	answered   bool
	status     Status
	reason     string
	unverified bool
	state      *endpointState
	override   Override
	diagnostic string
	fetched    bool
}

func (d deviceCheck) tlsOnly() bool { return d.port != 443 || d.state.temporary }
func endpointName(bare string, port uint16) string {
	if port == 443 {
		return bare
	}
	return bare + ":" + strconv.Itoa(int(port))
}
func endpointURL(dnsName string, port uint16) string {
	host := dnsName
	if port != 443 {
		host = net.JoinHostPort(host, strconv.Itoa(int(port)))
	}
	return "https://" + host + "/"
}
func temporary(port uint16, s *endpointState, o Override) bool {
	if o.Temporary != nil {
		return *o.Temporary
	}
	return port != 443 || s.priorPreview || s.card != nil && s.card.Preview
}

// Every connection is pinned and shares the endpoint's one deadline. Cards may
// use the listener connection only after its certificate has been verified.
func (p *DeviceProber) check(parent context.Context, d *deviceCheck, manual bool) {
	s := d.state
	wasUp := s.tlsUp
	d.status = StatusDown
	s.tlsUp = false
	if !d.IP.IsValid() {
		d.reason = "device has no Tailscale IP"
		return
	}
	ctx, cancel := context.WithTimeout(parent, cmp.Or(p.timeout, 5*time.Second))
	defer cancel()
	var unverified atomic.Bool
	cfg := &tls.Config{RootCAs: p.roots, ServerName: d.DNSName, InsecureSkipVerify: d.insecure}
	if d.insecure {
		cfg.VerifyConnection = func(cs tls.ConnectionState) error {
			intermediates := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: p.roots, DNSName: d.DNSName, Intermediates: intermediates})
			unverified.Store(err != nil)
			return nil
		}
	}
	dial := func() (*tls.Conn, error) {
		raw, err := p.dial(ctx, "tcp", netip.AddrPortFrom(d.IP, d.port).String())
		if err != nil {
			return nil, err
		}
		conn := tls.Client(raw, cfg)
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		if err = conn.HandshakeContext(ctx); err != nil {
			stop()
			conn.Close()
			return nil, err
		}
		// The caller owns the connection, while cancellation also interrupts HTTP I/O.
		return conn, nil
	}
	conn, err := dial()
	if err != nil {
		d.reason = err.Error()
		return
	}
	defer conn.Close()
	d.unverified = unverified.Load()
	s.tlsUp = true
	base := endpointURL(d.DNSName, d.port)
	var used atomic.Bool
	transport := &http.Transport{DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
		if !used.Swap(true) {
			return conn, nil
		}
		return dial()
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if d.candidate || !wasUp || manual {
		d.fetched = true
		if d.unverified {
			// An unverifiable listener cannot supply display metadata, even manually.
			s.card = nil
		} else {
			card, result := fetchCard(ctx, client, strings.TrimSuffix(base, "/")+cardPath)
			s.applyCard(card, result, manual, !wasUp && !d.candidate)
			if result != "ok" && result != "absent" {
				d.diagnostic = result
			}
		}
	}
	s.temporary = temporary(d.port, s, d.override)
	if d.tlsOnly() {
		d.answered, d.status = true, StatusUp
		if s.temporary && !d.unverified && upstreamGone(ctx, client, base) {
			// Serve keeps the listener after the app stops; hide the preview then,
			// and treat its return as a recovery so a new app never inherits the card.
			d.status, d.reason = StatusDown, "the app behind this preview does not answer (HTTP 502)"
			s.tlsUp = false
		}
		return
	}
	d.answered, d.status, d.reason = httpCheck(ctx, client, base)
}

// upstreamGone reports whether Tailscale Serve answers 502 Bad Gateway, which
// it does when the local app behind a mapping no longer answers. Any other
// answer, or an error, keeps the preview listed.
func upstreamGone(ctx context.Context, client *http.Client, url string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusBadGateway
}

func fetchCard(ctx context.Context, client *http.Client, url string) (*previewCard, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "HTTPS request failed"
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := client.Do(req)
	if err != nil {
		return nil, "HTTPS request failed"
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200:
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
		if err != nil {
			return nil, "HTTPS request failed"
		}
		card, err := parseCard(bytes.NewReader(body))
		if err != nil {
			return nil, "invalid"
		}
		if card == nil {
			return nil, "absent"
		}
		return card, "ok"
	case 404, 410:
		return nil, "absent"
	default:
		return nil, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
}

// refreshDevices reconciles endpoint admissions, probes eligible listeners and
// builds entries with endpoint-specific overrides before card and fallback text.
func (c *Catalog) refreshDevices(ctx context.Context, reserved map[string]bool, overrides map[string]Override, manual bool) ([]Entry, map[string]bool, error) {
	if c.Devices == nil {
		return nil, nil, nil
	}
	listed, err := c.listDevices(ctx)
	if ctx.Err() != nil {
		return nil, nil, err
	}
	eligible, rejected, members := c.eligibleDevices(listed, reserved, overrides)
	if c.admitted == nil {
		c.admitted = map[endpointID]Device{}
	}
	if c.endpoints == nil {
		c.endpoints = map[endpointID]*endpointState{}
	}
	for id, old := range c.admitted {
		check, ok := eligible[id]
		reason := ""
		switch {
		case !ok:
			reason = cmp.Or(rejected[id], "left the netmap")
		case old.DNSName != check.DNSName:
			reason = "renamed"
		case matchesDeviceTags(old.Tags, c.InsecureTLSTags) != check.insecure:
			reason = "TLS policy changed"
		default:
			c.admitted[id] = check.Device
			continue
		}
		if state := c.endpoints[id]; state == nil || !state.temporary {
			log.Printf("device %s removed: %s", endpointName(c.deviceName(old.DNSName), id.port), reason)
		}
		delete(c.admitted, id)
		delete(c.endpoints, id)
	}
	// Diagnostics for failed candidates are bounded by the current eligible set.
	for id := range c.endpoints {
		if _, ok := eligible[id]; !ok && err == nil {
			delete(c.endpoints, id)
		}
	}
	var checks, candidates []deviceCheck
	for id, check := range eligible {
		state := c.endpoints[id]
		if state == nil || state.dnsName != check.DNSName || state.insecure != check.insecure {
			state = &endpointState{dnsName: check.DNSName, insecure: check.insecure}
			c.endpoints[id] = state
		}
		check.state = state
		state.temporary = temporary(check.port, state, check.override)
		if _, ok := c.admitted[id]; ok {
			if !check.Online {
				check.status, check.reason = StatusDown, "Device offline"
				state.tlsUp = false
			}
			checks = append(checks, check)
		} else if check.Online && err == nil {
			check.candidate = true
			candidates = append(candidates, check)
		}
	}
	byEndpoint := func(a, b deviceCheck) int {
		return cmp.Or(cmp.Compare(a.DNSName, b.DNSName), cmp.Compare(a.port, b.port))
	}
	slices.SortFunc(checks, byEndpoint)
	slices.SortFunc(candidates, byEndpoint)
	start := sort.Search(len(candidates), func(i int) bool {
		d := candidates[i]
		return d.DNSName > c.cursor || d.DNSName == c.cursor && d.port > c.cursorPort
	})
	checks = append(checks, candidates[start:]...)
	checks = append(checks, candidates[:start]...)
	shown := c.probeDevices(ctx, checks, manual)
	if ctx.Err() != nil {
		return nil, members, err
	}
	entries := make([]Entry, 0, shown)
	for _, check := range checks[:shown] {
		bare := c.deviceName(check.DNSName)
		name := endpointName(bare, check.port)
		state := check.state
		if check.fetched && check.diagnostic != state.diagnostic {
			if check.diagnostic != "" {
				log.Printf("device %s card request: %s", name, check.diagnostic)
			}
			state.diagnostic = check.diagnostic
		}
		if check.candidate && !check.answered {
			if strings.Contains(check.reason, "x509:") {
				advice := "tag it and add the tag"
				if len(check.Tags) != 0 {
					advice = "add one of its tags"
				}
				c.warnOnce("insecure:"+name, "%s answers HTTPS with a certificate tailhome can't verify, so it has no tile. To show it anyway, %s to TAILHOME_DEVICE_INSECURE_TLS_TAGS.", name, advice)
			}
			continue
		}
		if check.candidate {
			c.admitted[endpointID{check.ID, check.port}] = check.Device
			if !state.temporary {
				log.Printf("device %s listed", name)
			}
		}
		entries = append(entries, c.entryFor(check, name, bare))
	}
	return entries, members, err
}

func (c *Catalog) entryFor(check deviceCheck, name, bare string) Entry {
	state := check.state
	e := Entry{
		Name: name, Source: "device", Host: check.DNSName, nodeID: check.ID,
		Description: check.override.Description, Group: cmp.Or(check.override.Group, "Apps"),
		Ports: []string{fmt.Sprintf("tcp:%d", check.port)},
		URL:   cmp.Or(check.override.URL, endpointURL(check.DNSName, check.port)),
		order: check.override.Order, Unverified: check.unverified,
	}
	fallback := titleCase(bare)
	if check.port != 443 {
		fallback += " :" + strconv.Itoa(int(check.port))
	}
	e.Label = cmp.Or(check.override.Label, fallback)
	icon := bare
	if state.card != nil {
		e.Label = cmp.Or(check.override.Label, state.card.Title, fallback)
		e.Description = cmp.Or(check.override.Description, state.card.Description)
		icon = cmp.Or(state.card.Icon, bare)
		if state.temporary {
			e.Set, e.Version = state.card.Set, state.card.Version
		}
	}
	if check.override.Icon != "" {
		e.Icon = iconURL(check.override.Icon)
	} else if icon == bare && c.Icons != nil {
		e.Icon = c.Icons.url(bare)
	} else {
		e.Icon = iconURL(icon)
	}
	e.Temporary = state.temporary
	e.Check = "http"
	if check.tlsOnly() {
		e.Check = "tls"
	}
	e.Status, e.Reason = check.status, check.reason
	return e
}

// Failed listings supply deduplicated peers for checks, never evidence of removal.
func (c *Catalog) listDevices(ctx context.Context) ([]Device, error) {
	listed, err := c.Devices.Devices(ctx)
	if ctx.Err() != nil {
		return nil, err
	}
	if err != nil {
		listed = nil
		if !c.devicesFailing {
			log.Printf("devices: listing failed: %v", err)
		}
		c.devicesFailing = true
		seen := map[tailcfg.StableNodeID]bool{}
		for id, d := range c.admitted {
			if !seen[id.node] {
				d.Online = true
				listed = append(listed, d)
				seen[id.node] = true
			}
		}
	} else if c.devicesFailing {
		log.Printf("devices: listing works again")
		c.devicesFailing = false
	}
	return listed, err
}

// eligibleDevices expands each peer into independent port slots; device-owned
// hidden parents cover every slot, while a Service collision reserves only 443.
func (c *Catalog) eligibleDevices(listed []Device, reserved map[string]bool, overrides map[string]Override) (map[endpointID]deviceCheck, map[endpointID]string, map[string]bool) {
	eligible := map[endpointID]deviceCheck{}
	rejected := map[endpointID]string{}
	members := map[string]bool{}
	ports := append([]uint16{443}, c.PreviewPorts...)
	for _, d := range listed {
		bare := c.deviceName(d.DNSName)
		for _, port := range ports {
			id := endpointID{d.ID, port}
			name := endpointName(bare, port)
			if bare == "" {
				rejected[id] = "no longer a member of this tailnet"
				continue
			}
			members[name] = true
			switch {
			case !reserved[bare] && overrides[bare].Hidden || port != 443 && overrides[name].Hidden:
				rejected[id] = "hidden"
			case len(c.DeviceTags) != 0 && !matchesDeviceTags(d.Tags, c.DeviceTags):
				rejected[id] = "no longer matches device tags"
			case port == 443 && reserved[bare]:
				rejected[id] = "Service name collision"
				c.warnOnce("collision:"+bare, "warning: device %s shares a Service name; the Service takes precedence", bare)
			default:
				eligible[id] = deviceCheck{Device: d, port: port, insecure: matchesDeviceTags(d.Tags, c.InsecureTLSTags), override: overrides[name]}
			}
		}
	}
	return eligible, rejected, members
}

// probeDevices checks admitted endpoints first, then starts candidates within
// the scan budget. Each worker gives an endpoint its own shared deadline.
func (c *Catalog) probeDevices(ctx context.Context, checks []deviceCheck, manual bool) (shown int) {
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(checks)) {
		workers.Go(func() {
			for i := range jobs {
				c.DeviceProber.check(ctx, &checks[i], manual)
			}
		})
	}
	var budget <-chan time.Time
scan:
	for i, check := range checks {
		if !check.candidate {
			shown = i + 1
			if check.Online {
				select {
				case jobs <- i:
				case <-ctx.Done():
					break scan
				}
			}
			continue
		}
		if budget == nil {
			budget = time.After(cmp.Or(c.scanBudget, 10*time.Second))
		}
		select {
		case jobs <- i:
			c.cursor, c.cursorPort, shown = check.DNSName, check.port, i+1
		case <-ctx.Done():
			break scan
		case <-budget:
			break scan
		}
	}
	close(jobs)
	workers.Wait()
	return shown
}
func matchesDeviceTags(tags, filter []string) bool {
	return slices.ContainsFunc(tags, func(tag string) bool { return slices.Contains(filter, tag) })
}

// deviceName accepts only a single host label in this tailnet's DNS suffix.
func (c *Catalog) deviceName(dnsName string) string {
	name, ok := strings.CutSuffix(dnsName, "."+c.DNSSuffix)
	if !ok || name == "" || strings.Contains(name, ".") {
		return ""
	}
	return strings.ToLower(name)
}
