// Package engine wires the resolver, tunnel, ciadpi processes, system DNS takeover and
// DNS lock together, and answers the tray app's requests.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"nomoredpi/internal/autofind"
	"nomoredpi/internal/browserdns"
	"nomoredpi/internal/byedpi"
	"nomoredpi/internal/config"
	"nomoredpi/internal/dnslock"
	"nomoredpi/internal/dnsserver"
	"nomoredpi/internal/fakeip"
	"nomoredpi/internal/ipc"
	"nomoredpi/internal/logx"
	"nomoredpi/internal/sysdns"
	"nomoredpi/internal/tunnel"
)

// AppVersion is set at build time: -ldflags "-X nomoredpi/internal/engine.AppVersion=1.0.0".
var AppVersion = "1.0.0"

var listenAddrs = []string{"127.0.0.1:53", "[::1]:53"}

type Engine struct {
	ciadpi string
	self   string
	dir    string

	mu      sync.Mutex
	cfg     *config.Config
	sup     *byedpi.Supervisor
	pool    *fakeip.Pool
	dns     *dnsserver.Server
	tun     *tunnel.Tunnel
	sysdns  *sysdns.Manager
	lock    *dnslock.Lock
	up      bool
	lastErr string
	since   time.Time

	findMu sync.Mutex // one auto-find at a time

	// live is what tunnel flows read; it never needs e.mu, so a flow can't block a stop.
	live atomic.Pointer[liveState]
}

type liveState struct {
	pool *fakeip.Pool
	dns  *dnsserver.Server
}

// New prepares the engine; exeDir must contain ciadpi.exe and wintun.dll.
func New(exeDir, selfExe string) (*Engine, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	sup, err := byedpi.NewSupervisor(filepath.Join(exeDir, "ciadpi.exe"))
	if err != nil {
		return nil, err
	}
	return &Engine{ciadpi: filepath.Join(exeDir, "ciadpi.exe"), self: selfExe, dir: config.Dir(), cfg: cfg, sup: sup}, nil
}

func (e *Engine) statePath() string        { return filepath.Join(e.dir, "dns-state.json") }
func (e *Engine) browserStatePath() string { return filepath.Join(e.dir, "browser-state.json") }

// Run activates (if configured) and keeps things healthy until ctx ends.
func (e *Engine) Run(ctx context.Context) {
	if err := sysdns.RestoreLeftover(e.statePath()); err != nil {
		logx.Printf("restore leftover DNS: %v", err)
	}
	e.mu.Lock()
	if e.cfg.Active {
		e.activateLocked()
	} else {
		browserdns.Restore(e.browserStatePath())
	}
	e.mu.Unlock()

	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			e.deactivateLocked()
			e.mu.Unlock()
			e.sup.Close()
			return
		case <-t.C:
			e.mu.Lock()
			if e.up {
				if repaired, err := e.tun.EnsureConfigured(); repaired {
					logx.Printf("tunnel adapter lost its address; reconfigured (err=%v)", err)
					flushResolverCache()
				}
				// New adapters, or apps that rewrite their adapter's DNS settings.
				if err := e.sysdns.Apply(); err != nil {
					logx.Printf("sysdns: %v", err)
				}
				_ = e.pool.Flush()
			} else if e.cfg.Active && e.lastErr != "" {
				logx.Printf("retrying activation after error: %s", e.lastErr)
				e.activateLocked()
			}
			e.mu.Unlock()
		}
	}
}

func (e *Engine) activateLocked() {
	if e.up {
		return
	}
	if err := e.startLocked(); err != nil {
		e.lastErr = err.Error()
		logx.Printf("activate failed: %v", err)
		e.deactivateLocked()
		e.lastErr = err.Error()
		return
	}
	e.up, e.lastErr, e.since = true, "", time.Now()
	logx.Printf("active")
}

func (e *Engine) startLocked() error {
	prefix, err := netip.ParsePrefix(e.cfg.FakeRange)
	if err != nil {
		return fmt.Errorf("fakeRange: %w", err)
	}
	if e.pool == nil || e.pool.Prefix() != prefix.Masked() {
		if e.pool, err = fakeip.New(prefix, filepath.Join(e.dir, "fakeip.json")); err != nil {
			return err
		}
	}
	up, err := dnsserver.NewUpstream(e.cfg.Upstreams)
	if err != nil {
		return err
	}
	e.applyProfilesLocked()
	e.dns = dnsserver.New(e.pool, up)
	e.dns.SetMatcher(matcherOf(e.cfg))
	e.live.Store(&liveState{pool: e.pool, dns: e.dns})

	if e.tun, err = tunnel.Start(e.pool.Gateway(), e.pool.Prefix(), e); err != nil {
		return err
	}
	if err := e.dns.Listen(listenAddrs); err != nil {
		return err
	}
	tunLUID := e.tun.LUID()
	e.sysdns = sysdns.New(e.statePath(), func(l winipcfg.LUID) bool { return l == tunLUID })
	if err := e.sysdns.Apply(); err != nil {
		return fmt.Errorf("could not set system DNS: %w", err)
	}
	if e.cfg.LockDNS {
		if e.lock, err = dnslock.Enable(e.self); err != nil {
			return err
		}
		logx.Printf("dns lock enabled")
		// Without this a browser in strict DoH mode would lose all DNS under the lock.
		if err := browserdns.Apply(e.browserStatePath()); err != nil {
			logx.Printf("browserdns: %v", err)
		}
	}
	flushResolverCache()
	return nil
}

func (e *Engine) deactivateLocked() {
	e.live.Store(nil)
	if e.lock != nil {
		browserdns.Restore(e.browserStatePath())
	}
	e.lock.Close()
	e.lock = nil
	if e.sysdns != nil {
		e.sysdns.Restore()
		e.sysdns = nil
	}
	if e.dns != nil {
		e.dns.Close()
	}
	if e.tun != nil {
		e.tun.Close()
		e.tun = nil
	}
	e.sup.StopAll()
	if e.up {
		logx.Printf("tunnel, resolver and ciadpi stopped")
	}
	if e.pool != nil {
		_ = e.pool.Flush()
	}
	if e.up {
		flushResolverCache()
		logx.Printf("inactive")
	}
	e.up, e.lastErr = false, ""
}

func (e *Engine) applyProfilesLocked() {
	var want []byedpi.Want
	for _, p := range e.cfg.Profiles {
		if p.Enabled && len(p.Domains) > 0 {
			want = append(want, byedpi.Want{ID: p.ID, Port: p.Port, Args: p.Args})
		}
	}
	e.sup.Apply(want)
}

func matcherOf(c *config.Config) dnsserver.Matcher {
	m := dnsserver.Matcher{}
	for _, p := range c.Profiles {
		if !p.Enabled {
			continue
		}
		for _, d := range p.Domains {
			if _, taken := m[d]; !taken {
				m[d] = p.ID
			}
		}
	}
	return m
}

// Resolve implements tunnel.Router.
func (e *Engine) Resolve(fake netip.Addr) (string, []netip.Addr, int, string, error) {
	live := e.live.Load()
	if live == nil {
		return "", nil, 0, "", errors.New("inactive")
	}
	pool, dns := live.pool, live.dns
	domain, ok := pool.Lookup(fake)
	if !ok {
		return "", nil, 0, "", fmt.Errorf("unknown fake address %s", fake)
	}
	reals, err := dns.Upstream().LookupA(domain)
	if err != nil {
		return domain, nil, 0, "", err
	}
	id, matched := dns.Match(domain)
	if !matched {
		// The domain left every profile after an app cached its fake address: connect
		// directly rather than break the app.
		return domain, reals, 0, "", nil
	}
	port := e.sup.Port(id)
	// Right after start (or a ciadpi restart) the process may need a few seconds;
	// hold the new connection instead of refusing it.
	for deadline := time.Now().Add(8 * time.Second); port == 0 && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		port = e.sup.Port(id)
	}
	if port == 0 {
		return domain, nil, 0, id, fmt.Errorf("profile %s is not running", id)
	}
	return domain, reals, port, id, nil
}

// ---- requests from the tray app ----

type ProfileStatus struct {
	config.Profile
	Running  bool   `json:"running"`
	Restarts int    `json:"restarts"`
	LastErr  string `json:"lastErr,omitempty"`
	Active   int64  `json:"activeConns"`
	Total    uint64 `json:"totalConns"`
	Failed   uint64 `json:"failedConns"`
	Bytes    uint64 `json:"bytes"`
}

type Status struct {
	Version  string            `json:"version"`
	Active   bool              `json:"active"`
	Up       bool              `json:"up"`
	Since    string            `json:"since,omitempty"`
	Error    string            `json:"error,omitempty"`
	Locked   bool              `json:"locked"`
	DNS      map[string]uint64 `json:"dns"`
	Profiles []ProfileStatus   `json:"profiles"`
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Status{Version: AppVersion, Active: e.cfg.Active, Up: e.up, Error: e.lastErr, Locked: e.lock != nil, DNS: map[string]uint64{}}
	if e.up {
		s.Since = e.since.Format(time.RFC3339)
	}
	if e.dns != nil {
		st := &e.dns.Stats
		s.DNS = map[string]uint64{"queries": st.Queries.Load(), "faked": st.Faked.Load(),
			"upstream": st.Upstream.Load(), "failures": st.Failures.Load(), "blockedDoh": st.BlockedDoH.Load()}
	}
	sups := map[string]byedpi.Status{}
	for _, st := range e.sup.Statuses() {
		sups[st.ID] = st
	}
	var tstats map[string]*tunnel.ProfileStats
	if e.tun != nil {
		tstats = e.tun.Stats()
	}
	for _, p := range e.cfg.Profiles {
		ps := ProfileStatus{Profile: p}
		if st, ok := sups[p.ID]; ok {
			ps.Running, ps.Restarts, ps.LastErr = st.Running, st.Restarts, st.LastErr
		}
		if ts := tstats[p.ID]; ts != nil {
			ps.Active, ps.Total, ps.Failed, ps.Bytes = ts.Active.Load(), ts.Total.Load(), ts.Failed.Load(), ts.Bytes.Load()
		}
		s.Profiles = append(s.Profiles, ps)
	}
	return s
}

func (e *Engine) SetActive(active bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.Active = active
	if err := config.Save(e.cfg); err != nil {
		return err
	}
	if active {
		e.activateLocked()
		if !e.up {
			return errors.New(e.lastErr)
		}
	} else {
		e.deactivateLocked()
	}
	return nil
}

// ErrNeedsAdmin is returned when a caller without an elevated token tries to change a
// machine-wide DNS setting.
var ErrNeedsAdmin = errors.New("changing the DNS servers, the DNS lock or the fake IP range needs administrator rights")

// SystemSettings are the parts of the config that affect every user's DNS. Only an
// elevated caller may change them (setSystem); setConfig must leave them as they are
// unless the caller is elevated.
type SystemSettings struct {
	Upstreams []string `json:"upstreams"`
	LockDNS   bool     `json:"lockDns"`
	FakeRange string   `json:"fakeRange"`
}

func systemOf(c *config.Config) SystemSettings {
	return SystemSettings{Upstreams: c.Upstreams, LockDNS: c.LockDNS, FakeRange: c.FakeRange}
}

func (s SystemSettings) equal(o SystemSettings) bool {
	return s.LockDNS == o.LockDNS && s.FakeRange == o.FakeRange && slices.Equal(s.Upstreams, o.Upstreams)
}

// SetSystem changes the machine-wide DNS settings and keeps everything else.
func (e *Engine) SetSystem(s SystemSettings) error {
	n := e.Config()
	n.Upstreams, n.LockDNS, n.FakeRange = s.Upstreams, s.LockDNS, strings.TrimSpace(s.FakeRange)
	return e.SetConfig(n, true)
}

// SetConfig replaces the config. elevated says whether the caller may change the
// machine-wide DNS settings (SystemSettings).
func (e *Engine) SetConfig(n *config.Config, elevated bool) error {
	n.Normalize()
	if err := n.Validate(); err != nil {
		return err
	}
	if _, err := dnsserver.NewUpstream(n.Upstreams); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.cfg
	if !elevated && !systemOf(n).equal(systemOf(old)) {
		return ErrNeedsAdmin
	}
	n.Active = old.Active
	if err := config.Save(n); err != nil {
		return err
	}
	e.cfg = n
	restart := !systemOf(old).equal(systemOf(n))
	if e.up && restart {
		e.deactivateLocked()
		e.activateLocked()
		return nil
	}
	if e.up {
		e.applyProfilesLocked()
		e.dns.SetMatcher(matcherOf(n))
		flushResolverCache()
	}
	return nil
}

func (e *Engine) Config() *config.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.Clone()
}

type FindResult struct {
	Profile  string             `json:"profile"`
	Found    string             `json:"found"`
	Applied  bool               `json:"applied"`
	Attempts []autofind.Attempt `json:"attempts"`
}

// AutoFind tests strategies for a profile and saves the first that works.
func (e *Engine) AutoFind(id string) (*FindResult, error) {
	if !e.findMu.TryLock() {
		return nil, errors.New("another auto find is already running")
	}
	defer e.findMu.Unlock()
	e.mu.Lock()
	var prof *config.Profile
	for i := range e.cfg.Profiles {
		if e.cfg.Profiles[i].ID == id {
			p := e.cfg.Profiles[i]
			prof = &p
		}
	}
	upSpecs := e.cfg.Upstreams
	e.mu.Unlock()
	if prof == nil {
		return nil, fmt.Errorf("no such profile: %s", id)
	}
	host := prof.Probe
	if host == "" && len(prof.Domains) > 0 {
		host = prof.Domains[0]
	}
	if host == "" {
		return nil, errors.New("the profile has no domains")
	}
	up, err := dnsserver.NewUpstream(upSpecs)
	if err != nil {
		return nil, err
	}
	ips, err := up.LookupA(host)
	if err != nil {
		return nil, fmt.Errorf("could not resolve %s: %w", host, err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	logx.Printf("autofind[%s]: probing %s (%s)", id, host, ips[0])
	strategies := append([]string{}, config.Strategies...)
	if prof.Args != "" && !slices.Contains(strategies, prof.Args) {
		strategies = append([]string{prof.Args}, strategies...)
	}
	found, attempts := autofind.Run(e.ciadpi, host, ips[0].String(), port, strategies, func(a autofind.Attempt) {
		logx.Printf("autofind[%s]: %-45q ok=%v %dms %s", id, a.Args, a.OK, a.Ms, a.Err)
	})
	res := &FindResult{Profile: id, Found: found, Attempts: attempts}
	if found == "" {
		return res, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range e.cfg.Profiles {
		if e.cfg.Profiles[i].ID == id {
			e.cfg.Profiles[i].Args = found
		}
	}
	if err := config.Save(e.cfg); err != nil {
		return res, err
	}
	if e.up {
		e.applyProfilesLocked()
	}
	res.Applied = true
	return res, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Handle dispatches an IPC request.
func (e *Engine) Handle(caller ipc.Caller, method string, params json.RawMessage) (any, error) {
	switch method {
	case "status":
		return e.Status(), nil
	case "getConfig":
		return e.Config(), nil
	case "setConfig":
		var c config.Config
		if err := json.Unmarshal(params, &c); err != nil {
			return nil, err
		}
		return nil, e.SetConfig(&c, caller.Elevated)
	case "setSystem":
		if !caller.Elevated {
			return nil, ErrNeedsAdmin
		}
		var s SystemSettings
		if err := json.Unmarshal(params, &s); err != nil {
			return nil, err
		}
		return nil, e.SetSystem(s)
	case "setActive":
		var on bool
		if err := json.Unmarshal(params, &on); err != nil {
			return nil, err
		}
		return nil, e.SetActive(on)
	case "autoFind":
		var id string
		if err := json.Unmarshal(params, &id); err != nil {
			return nil, err
		}
		return e.AutoFind(id)
	case "logs":
		return logx.Tail(200), nil
	}
	if res, ok := debugHandle(method); ok {
		return res, nil
	}
	return nil, fmt.Errorf("unknown method %q", strings.TrimSpace(method))
}

var procFlush = windows.NewLazySystemDLL("dnsapi.dll").NewProc("DnsFlushResolverCache")

func flushResolverCache() { _, _, _ = procFlush.Call() }
