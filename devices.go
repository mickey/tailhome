package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"sort"
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

// DeviceLister is implemented by localDevices and by test fakes.
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

// DeviceProber fetches https://<name>/ from a device, dialled by IP so a name
// that leaves MagicDNS can never resolve anywhere else.
type DeviceProber struct {
	dial    func(ctx context.Context, network, addr string) (net.Conn, error)
	roots   *x509.CertPool // nil: system roots; tests pin a CA
	timeout time.Duration  // zero: 5 s
}

func (p *DeviceProber) probe(ctx context.Context, d Device, insecure bool) (answered bool, s Status, reason string, unverified bool) {
	if !d.IP.IsValid() {
		return false, StatusDown, "device has no Tailscale IP", false
	}
	timeout := cmp.Or(p.timeout, 5*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var verificationFailed atomic.Bool
	tlsConfig := &tls.Config{RootCAs: p.roots, InsecureSkipVerify: insecure}
	if insecure {
		// The exception permits certificate errors, but only certificates that
		// actually fail verification should carry the warning on their tile.
		tlsConfig.VerifyConnection = func(cs tls.ConnectionState) error {
			intermediates := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{
				Roots: p.roots, DNSName: cs.ServerName, Intermediates: intermediates,
			})
			verificationFailed.Store(err != nil)
			return nil
		}
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				// The Transport detaches dials from the request's cancellation,
				// so a SYN nobody answers would otherwise outlive the probe.
				ctx, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				return p.dial(ctx, network, netip.AddrPortFrom(d.IP, 443).String())
			},
			TLSHandshakeTimeout: timeout, // the handshake runs on the detached dial too
			DisableKeepAlives:   true,
			TLSClientConfig:     tlsConfig,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	answered, status, reason := httpCheck(ctx, client, "https://"+d.DNSName+"/")
	return answered, status, reason, verificationFailed.Load()
}

type deviceCheck struct {
	Device
	insecure   bool
	candidate  bool // not yet admitted; an answer this refresh admits it
	answered   bool
	status     Status
	reason     string
	unverified bool // certificate failed verification under the exception
}

// refreshDevices returns tiles for admitted devices and candidates that answered
// this refresh, plus every member name for the override warning.
func (c *Catalog) refreshDevices(ctx context.Context, reserved map[string]bool, overrides map[string]Override) ([]Entry, map[string]bool) {
	if c.Devices == nil {
		return nil, nil
	}
	listed := c.listDevices(ctx)
	if ctx.Err() != nil {
		return nil, nil
	}
	eligible, rejected, members := c.eligibleDevices(listed, reserved, overrides)
	if c.admitted == nil {
		c.admitted = map[tailcfg.StableNodeID]Device{}
	}
	for id, old := range c.admitted {
		check, ok := eligible[id]
		var reason string
		switch {
		case !ok:
			reason = cmp.Or(rejected[id], "left the netmap")
		case old.DNSName != check.DNSName:
			reason = "renamed"
		case matchesDeviceTags(old.Tags, c.InsecureTLSTags) != check.insecure:
			reason = "TLS policy changed"
		default:
			c.admitted[id] = check.Device // keeps the IP current
			continue
		}
		log.Printf("device %s removed: %s", c.deviceName(old.DNSName), reason)
		delete(c.admitted, id)
	}
	var checks, candidates []deviceCheck
	for id, check := range eligible {
		switch _, admitted := c.admitted[id]; {
		case admitted:
			if !check.Online {
				check.status, check.reason = StatusDown, "Device offline"
			}
			checks = append(checks, check)
		case check.Online:
			check.candidate = true
			candidates = append(candidates, check)
		}
	}
	byName := func(a, b deviceCheck) int { return cmp.Compare(a.DNSName, b.DNSName) }
	slices.SortFunc(checks, byName)
	slices.SortFunc(candidates, byName)
	// Resume after the last candidate started and wrap, so short scans still reach every peer.
	start := sort.Search(len(candidates), func(i int) bool { return candidates[i].DNSName > c.cursor })
	checks = append(checks, candidates[start:]...)
	checks = append(checks, candidates[:start]...)
	shown := c.probeDevices(ctx, checks)
	if ctx.Err() != nil {
		return nil, members
	}
	entries := make([]Entry, 0, shown)
	for _, check := range checks[:shown] {
		name := c.deviceName(check.DNSName)
		if check.candidate && !check.answered {
			// Go renders certificate verification failures with an "x509:" reason.
			if !check.insecure && strings.Contains(check.reason, "x509:") {
				advice := "add one of its tags to TAILHOME_DEVICE_INSECURE_TLS_TAGS."
				if len(check.Tags) == 0 {
					advice = "tag it and add the tag to TAILHOME_DEVICE_INSECURE_TLS_TAGS."
				}
				c.warnOnce("insecure:"+name, "%s answers HTTPS with a certificate tailhome can't verify, so it has no tile. To show it anyway, %s", name, advice)
			}
			continue
		}
		if check.candidate {
			c.admitted[check.ID] = check.Device
			log.Printf("device %s listed", name)
		}
		e := newDeviceEntry(name, check.DNSName, check.unverified, overrides[name])
		if overrides[name].Icon == "" && c.Icons != nil {
			e.Icon = c.Icons.url(name)
		}
		e.Status, e.Reason = check.status, check.reason
		entries = append(entries, e)
	}
	return entries, members
}

// listDevices returns the tailnet's devices or, while listing fails, the
// admitted ones as last seen, so they are still checked.
func (c *Catalog) listDevices(ctx context.Context) []Device {
	listed, err := c.Devices.Devices(ctx)
	if ctx.Err() != nil { // shutting down: refreshDevices drops the result
		return nil
	}
	if err != nil {
		if !c.devicesFailing {
			log.Printf("devices: listing failed: %v", err)
		}
		c.devicesFailing = true
		listed = make([]Device, 0, len(c.admitted))
		for _, d := range c.admitted {
			d.Online = true // a listing outage is no evidence that a device left
			listed = append(listed, d)
		}
	} else if c.devicesFailing {
		log.Printf("devices: listing works again")
		c.devicesFailing = false
	}
	return listed
}

// eligibleDevices applies membership, Service-name, hidden and tag rules.
// rejected holds exclusion reasons; members holds every tailnet member name
// for the override warning.
func (c *Catalog) eligibleDevices(listed []Device, reserved map[string]bool, overrides map[string]Override) (eligible map[tailcfg.StableNodeID]deviceCheck, rejected map[tailcfg.StableNodeID]string, members map[string]bool) {
	eligible = map[tailcfg.StableNodeID]deviceCheck{}
	rejected = map[tailcfg.StableNodeID]string{}
	members = map[string]bool{}
	for _, d := range listed {
		name := c.deviceName(d.DNSName)
		if name == "" {
			rejected[d.ID] = "no longer a member of this tailnet"
			continue
		}
		members[name] = true
		if reserved[name] {
			rejected[d.ID] = "Service name collision"
			c.warnOnce("collision:"+name, "warning: device %s shares a Service name; the Service takes precedence", name)
			continue
		}
		if overrides[name].Hidden {
			rejected[d.ID] = "hidden"
			continue
		}
		if len(c.DeviceTags) != 0 && !matchesDeviceTags(d.Tags, c.DeviceTags) {
			rejected[d.ID] = "no longer matches device tags"
			continue
		}
		eligible[d.ID] = deviceCheck{Device: d, insecure: matchesDeviceTags(d.Tags, c.InsecureTLSTags)}
	}
	return eligible, rejected, members
}

// probeDevices checks admitted devices, then candidates until the scan budget
// ends, and returns the prefix to show, including offline admitted devices.
func (c *Catalog) probeDevices(ctx context.Context, checks []deviceCheck) (shown int) {
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(checks)) {
		workers.Go(func() {
			for i := range jobs {
				check := &checks[i]
				check.answered, check.status, check.reason, check.unverified = c.DeviceProber.probe(ctx, check.Device, check.insecure)
			}
		})
	}
	var budget <-chan time.Time
scan:
	for i, check := range checks {
		if !check.candidate {
			shown = i + 1
			if check.Online {
				jobs <- i
			}
			continue
		}
		if budget == nil {
			budget = time.After(cmp.Or(c.scanBudget, 10*time.Second))
		}
		select {
		case jobs <- i:
			c.cursor, shown = check.DNSName, i+1
		case <-ctx.Done():
			break scan
		case <-budget: // only candidate starts are bounded; in-flight probes finish
			break scan
		}
	}
	close(jobs)
	workers.Wait()
	return shown
}

func newDeviceEntry(name, dnsName string, unverified bool, o Override) Entry {
	description := ""
	if unverified {
		description = "Unverified HTTPS"
	}
	return Entry{
		Name:        name,
		Source:      "device",
		Label:       cmp.Or(o.Label, titleCase(name)),
		Description: cmp.Or(o.Description, description),
		Group:       cmp.Or(o.Group, "Apps"),
		Icon:        iconURL(cmp.Or(o.Icon, name)),
		Host:        dnsName,
		Ports:       []string{"tcp:443"},
		URL:         cmp.Or(o.URL, "https://"+dnsName+"/"),
		order:       o.Order,
	}
}

func matchesDeviceTags(tags, filter []string) bool {
	return slices.ContainsFunc(tags, func(tag string) bool { return slices.Contains(filter, tag) })
}

// deviceName returns the bare name of a device of this tailnet, or "" for one shared in from another.
func (c *Catalog) deviceName(dnsName string) string {
	name, ok := strings.CutSuffix(dnsName, "."+c.DNSSuffix)
	if !ok || name == "" || strings.Contains(name, ".") {
		return ""
	}
	return strings.ToLower(name)
}
