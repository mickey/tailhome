package main

import (
	"context"
	"net"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
	"tailscale.com/tailcfg/nodecap"
	"tailscale.com/tsnet"
	"tailscale.com/types/views"
)

// shareNode keeps all upstream traffic on the dedicated node's network stack.
type shareNode interface {
	Up(context.Context) (*ipnstate.Status, error)
	LocalClient() (*local.Client, error)
	ListenFunnel() (net.Listener, error)
	Dial(context.Context, string, string) (net.Conn, error)
	Close() error
}

type tsnetShareNode struct{ *tsnet.Server }

func (n *tsnetShareNode) ListenFunnel() (net.Listener, error) {
	return n.Server.ListenFunnel("tcp", ":443")
}

// nodeRuntime is single-use for publication. Logout can also be used on a fresh
// instance to clean up cached state, but never enrolls a new identity.
type nodeRuntime struct {
	binding                           ShareBinding
	hostname, dir, suffix, homeNodeID string
	mint                              func(context.Context) (string, error)
	mu                                sync.Mutex
	node                              shareNode
	closed                            bool
	newNode                           func(string) shareNode
	cleanupUpTimeout                  time.Duration // zero uses the production discovery bound
}

func newShareRuntime(binding ShareBinding, hostname, dir, tailnetSuffix, homeNodeID string, mint func(context.Context) (string, error)) shareRuntime {
	r := &nodeRuntime{binding: binding, hostname: hostname, dir: dir, suffix: strings.TrimSuffix(tailnetSuffix, "."), homeNodeID: homeNodeID, mint: mint}
	r.newNode = func(authKey string) shareNode {
		return &tsnetShareNode{&tsnet.Server{
			Dir: dir, Hostname: hostname, Ephemeral: true, AuthKey: authKey,
			AdvertiseTags: []string{shareTag},
			// Auth URLs and issuer errors must never leak into private logs.
			UserLogf: func(string, ...any) {}, Logf: func(string, ...any) {},
		}}
	}
	return r
}

func (r *nodeRuntime) start(ctx context.Context, cleanup bool) (shareNode, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, shareErr("setup_failed")
	}
	if r.node != nil {
		n := r.node
		r.mu.Unlock()
		return n, nil
	}
	r.mu.Unlock()
	cached, err := enrolled(r.dir)
	if err != nil {
		return nil, shareErr("setup_failed")
	}
	if cleanup && !cached {
		return nil, nil
	}
	// A nonempty invalid key prevents tsnet's ambient-key and interactive
	// fallback when restoring state. It can never enroll another node.
	authKey := "tailhome-cached-state-only"
	if !cleanup && !cached {
		if r.mint == nil {
			return nil, shareErr("oauth_missing")
		}
		authKey, err = r.mint(ctx)
		if err != nil {
			return nil, shareEnrollmentFailure(err)
		}
		if authKey == "" {
			return nil, shareErr("setup_failed")
		}
	}
	if err := os.MkdirAll(r.dir, 0700); err != nil {
		return nil, shareErr("setup_failed")
	}
	if err := os.Chmod(r.dir, 0700); err != nil {
		return nil, shareErr("setup_failed")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, shareErr("setup_failed")
	}
	if r.node == nil {
		r.node = r.newNode(authKey)
	}
	return r.node, nil
}

// shareNodeUp bounds enrollment independently of the rest of publication. Up waits for
// Running even when a cached ephemeral profile has been deleted by control.
// Inspect that profile with a fresh budget after Up times out.
func shareNodeUp(ctx context.Context, n shareNode, timeout time.Duration) (*ipnstate.Status, error) {
	upCtx, cancel := context.WithTimeout(ctx, timeout)
	status, err := n.Up(upCtx)
	cancel()
	if err == nil || ctx.Err() != nil {
		return status, err
	}
	lc, clientErr := n.LocalClient()
	if clientErr != nil {
		return status, err
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 2*time.Second)
	defer checkCancel()
	current, statusErr := lc.Status(checkCtx)
	if statusErr == nil && current != nil && current.BackendState == "NeedsLogin" {
		return current, nil
	}
	return status, err
}

func (r *nodeRuntime) Prepare(ctx context.Context, report func()) (host string, listener net.Listener, resolve func(context.Context) ([]netip.Addr, error), err error) {
	startCtx, startCancel := context.WithTimeout(ctx, 20*time.Second)
	n, err := r.start(startCtx, false)
	startCancel()
	if err != nil {
		return "", nil, nil, err
	}
	status, err := shareNodeUp(ctx, n, 60*time.Second)
	if err == nil && status != nil && status.BackendState == "NeedsLogin" && ctx.Err() == nil {
		// Reauthenticate the existing backend, rather than merely setting a new
		// tsnet AuthKey (which is ignored for cached profiles). This preserves
		// the share's state directory, hostname and certificate cache.
		cached, stateErr := enrolled(r.dir)
		if stateErr == nil && cached && r.mint != nil {
			retryCtx, retryCancel := context.WithTimeout(ctx, 20*time.Second)
			authKey, mintErr := r.mint(retryCtx)
			if mintErr != nil {
				retryCancel()
				return "", nil, nil, shareEnrollmentFailure(mintErr)
			} else if authKey == "" {
				err = shareErr("setup_failed")
			} else {
				lc, clientErr := n.LocalClient()
				err = clientErr
				if err == nil {
					err = lc.Start(retryCtx, ipn.Options{AuthKey: authKey})
				}
				if err == nil {
					err = lc.StartLoginInteractive(retryCtx)
				}
			}
			retryCancel()
			if err == nil {
				status, err = shareNodeUp(ctx, n, 60*time.Second)
			}
		}
	}
	if err != nil {
		return "", nil, nil, shareErr("setup_failed")
	}
	host, err = validateShareEnrollment(status, r.hostname, r.suffix)
	if err != nil {
		return "", nil, nil, err
	}
	if !status.Self.HasCap(nodecap.HTTPS) {
		return "", nil, nil, shareErr("dns_missing")
	}
	if ipn.CheckFunnelAccess(443, status.Self) != nil {
		return "", nil, nil, shareErr("funnel_denied")
	}
	lc, err := n.LocalClient()
	if err != nil {
		return "", nil, nil, shareErr("setup_failed")
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
	defer checkCancel()
	lock, err := lc.TailnetLockStatus(checkCtx)
	if err != nil {
		return "", nil, nil, shareErr("setup_failed")
	}
	if lock != nil && lock.Enabled {
		return "", nil, nil, shareErr("tailnet_lock")
	}
	resolve = func(ctx context.Context) ([]netip.Addr, error) { return r.resolve(ctx, lc) }
	if _, err := resolve(ctx); err != nil {
		return "", nil, nil, err
	}
	if ctx.Err() != nil {
		return "", nil, nil, shareErr("setup_failed")
	}
	listener, err = n.ListenFunnel()
	if err != nil {
		return "", nil, nil, shareErr("setup_failed")
	}
	// ListenFunnel uses an internal background context. A Stop or timeout can
	// race its completion, so never return a late listener to the manager.
	fail := func(code string) (string, net.Listener, func(context.Context) ([]netip.Addr, error), error) {
		// Closing a tsnet listener may wait on its internal background work;
		// it must not delay the manager's independent admission revocation.
		go listener.Close()
		return "", nil, nil, shareErr(code)
	}
	if ctx.Err() != nil {
		return fail("setup_failed")
	}
	if report != nil {
		report()
	}
	certCtx, certCancel := context.WithTimeout(ctx, 60*time.Second)
	defer certCancel()
	_, _, err = lc.CertPair(certCtx, host)
	if err != nil {
		if strings.Contains(err.Error(), "urn:ietf:params:acme:error:rateLimited") {
			return fail("certificate_rate_limited")
		}
		return fail("setup_failed")
	}
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed || ctx.Err() != nil {
		return fail("setup_failed")
	}
	return host, listener, resolve, nil
}

func validateShareEnrollment(st *ipnstate.Status, hostname, suffix string) (string, error) {
	if st == nil || st.Self == nil || st.Self.ID == "" || st.BackendState != "Running" {
		return "", shareErr("setup_failed")
	}
	if suffix == "" || st.CurrentTailnet == nil || st.CurrentTailnet.MagicDNSSuffix != suffix || !st.CurrentTailnet.MagicDNSEnabled {
		return "", shareErr("dns_missing")
	}
	if st.Self.Tags == nil || st.Self.Tags.Len() != 1 || st.Self.Tags.At(0) != shareTag {
		return "", shareErr("setup_failed")
	}
	host := hostname + "." + suffix
	if strings.TrimSuffix(st.Self.DNSName, ".") != host {
		return "", shareErr("setup_failed")
	}
	if !slices.Contains(st.CertDomains, host) {
		return "", shareErr("dns_missing")
	}
	return host, nil
}

func (r *nodeRuntime) resolve(ctx context.Context, lc *local.Client) ([]netip.Addr, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := lc.Status(ctx)
	if err != nil {
		return nil, shareErr("target_unavailable")
	}
	if _, err := validateShareEnrollment(st, r.hostname, r.suffix); err != nil {
		return nil, err
	}
	if r.binding.Kind == "service" {
		services, err := lc.GetServices(ctx)
		if err != nil {
			return nil, shareErr("target_unavailable")
		}
		return resolveShareService(r.binding, services, r.suffix)
	}
	return resolveShareDevice(r.binding, st, r.homeNodeID)
}

func resolveShareService(b ShareBinding, services map[tailcfg.ServiceName]tailcfg.ServiceDetails, suffix string) ([]netip.Addr, error) {
	name := tailcfg.AsServiceName(b.Service)
	if name == "" || b.Kind != "service" || strings.TrimPrefix(b.Service, "svc:")+"."+suffix != b.FQDN {
		return nil, shareErr("target_changed")
	}
	svc, ok := services[name]
	if !ok || (svc.Name != "" && svc.Name != name) {
		return nil, shareErr("target_unavailable")
	}
	advertised := false
	for _, p := range svc.Ports {
		if p.Proto == 6 && p.Ports.First == b.Port && p.Ports.Last == b.Port {
			advertised = true
		}
	}
	if !advertised || !validSharePort(b.Port) {
		return nil, shareErr("target_changed")
	}
	return validateShareAddresses(svc.Addrs)
}

func resolveShareDevice(b ShareBinding, st *ipnstate.Status, homeNodeID string) ([]netip.Addr, error) {
	if b.Kind != "device" || b.NodeID == "" || b.NodeID == homeNodeID || st == nil || st.Self == nil || b.NodeID == string(st.Self.ID) || !validSharePort(b.Port) {
		return nil, shareErr("target_changed")
	}
	for _, p := range st.Peer {
		if p == nil || string(p.ID) != b.NodeID {
			continue
		}
		if p.ShareeNode || strings.TrimSuffix(p.DNSName, ".") != b.FQDN {
			return nil, shareErr("target_changed")
		}
		if p.Tags != nil && views.SliceContains(*p.Tags, shareTag) {
			return nil, shareErr("target_changed")
		}
		for _, raw := range p.PeerAPIURL {
			u, err := url.Parse(raw)
			if err == nil && u.Port() == strconv.Itoa(int(b.Port)) {
				return nil, shareErr("target_changed")
			}
		}
		return validateShareAddresses(p.TailscaleIPs)
	}
	return nil, shareErr("target_unavailable")
}

func validSharePort(port uint16) bool {
	// LocalAPI and web-management ports are never application destinations.
	return port != 0 && port != 41112 && port != 5252
}

// Dial accepts only the literal destination pinned by the gateway's resolver.
// The gateway refreshes and validates target identity before each connection;
// repeating resolution here would race service movement and double LocalAPI work.
func (r *nodeRuntime) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" {
		return nil, shareErr("target_changed")
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil || ap.Port() != r.binding.Port || !validSharePort(ap.Port()) || !shareTailnetAddress(ap.Addr()) {
		return nil, shareErr("target_changed")
	}
	r.mu.Lock()
	n, closed := r.node, r.closed
	r.mu.Unlock()
	if n == nil || closed {
		return nil, shareErr("target_unavailable")
	}
	return n.Dial(ctx, network, address)
}

func (r *nodeRuntime) Logout(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	n, err := r.start(ctx, true)
	if err != nil || n == nil {
		return err
	}
	upTimeout := r.cleanupUpTimeout
	if upTimeout == 0 {
		upTimeout = 20 * time.Second
	}
	status, err := shareNodeUp(ctx, n, upTimeout)
	if err != nil {
		return shareErr("cleanup_pending")
	}
	if status != nil && status.BackendState == "NeedsLogin" {
		return nil // The ephemeral identity was already garbage-collected.
	}
	lc, err := n.LocalClient()
	if err != nil {
		return shareErr("cleanup_pending")
	}
	if err := lc.Logout(ctx); err != nil {
		return shareErr("cleanup_pending")
	}
	return nil
}

func (r *nodeRuntime) Close() error {
	r.mu.Lock()
	r.closed = true
	n := r.node
	r.mu.Unlock()
	if n == nil {
		return nil
	}
	if err := n.Close(); err != nil {
		return shareErr("cleanup_pending")
	}
	return nil
}
