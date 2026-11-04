package dnsserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/ameshkov/dnscrypt/v2"
	"github.com/miekg/dns"

	"nomoredpi/internal/logx"
)

// Upstream resolves through DNSCrypt stamps or DoH URLs, in order, with a small cache.
type Upstream struct {
	targets []*target
	cache   *cache
}

type target struct {
	spec string
	mu   sync.Mutex
	dc   *dnscrypt.Client
	ri   *dnscrypt.ResolverInfo
	at   time.Time // when ri was fetched
	doh  *http.Client
	// fails counts consecutive failures; failing targets are tried last.
	fails int
}

func NewUpstream(specs []string) (*Upstream, error) {
	u := &Upstream{cache: newCache(8192)}
	for _, s := range specs {
		s = strings.TrimSpace(s)
		switch {
		case strings.HasPrefix(s, "sdns://"):
			u.targets = append(u.targets, &target{spec: s, dc: &dnscrypt.Client{
				Net: "udp", Timeout: 3 * time.Second, UDPSize: 4096,
				Logger: slog.New(slog.DiscardHandler),
			}})
		case strings.HasPrefix(s, "https://"):
			u.targets = append(u.targets, &target{spec: s, doh: &http.Client{Timeout: 4 * time.Second}})
		case s == "":
		default:
			return nil, fmt.Errorf("unknown upstream %q (sdns:// or https:// expected)", s)
		}
	}
	if len(u.targets) == 0 {
		return nil, errors.New("no DNS upstream configured")
	}
	return u, nil
}

func (t *target) exchange(m *dns.Msg) (*dns.Msg, error) {
	if t.doh != nil {
		return t.exchangeDoH(m)
	}
	t.mu.Lock()
	if t.ri == nil || time.Since(t.at) > time.Hour {
		ri, err := t.dc.Dial(t.spec)
		if err != nil {
			t.mu.Unlock()
			return nil, fmt.Errorf("dnscrypt dial: %w", err)
		}
		t.ri, t.at = ri, time.Now()
	}
	ri := t.ri
	t.mu.Unlock()
	tc := &dnscrypt.Client{Net: "tcp", Timeout: 4 * time.Second, Logger: slog.New(slog.DiscardHandler)}
	r, err := t.dc.Exchange(m, ri)
	if err != nil {
		// UDP can fail on its own (Windows can refuse it for a few seconds while it
		// reconfigures adapters); the same resolver over TCP usually still answers.
		if r, tcpErr := tc.Exchange(m, ri); tcpErr == nil {
			return r, nil
		}
		t.mu.Lock()
		t.ri = nil // certificate may have rotated
		t.mu.Unlock()
		return nil, err
	}
	if r.Truncated {
		return tc.Exchange(m, ri)
	}
	return r, nil
}

func (t *target) exchangeDoH(m *dns.Msg) (*dns.Msg, error) {
	q := m.Copy()
	q.Id = 0
	wire, err := q.Pack()
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, t.spec, bytes.NewReader(wire))
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := t.doh.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("doh status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	if err != nil {
		return nil, err
	}
	r := new(dns.Msg)
	if err := r.Unpack(body); err != nil {
		return nil, err
	}
	r.Id = m.Id
	return r, nil
}

// Exchange answers m from cache or the first upstream that responds.
func (u *Upstream) Exchange(m *dns.Msg) (*dns.Msg, error) {
	if len(m.Question) != 1 {
		return nil, errors.New("exactly one question expected")
	}
	key := cacheKey(m.Question[0])
	if r := u.cache.get(key); r != nil {
		r.Id = m.Id
		return r, nil
	}
	var lastErr error
	for _, t := range u.ordered() {
		r, err := t.exchange(m)
		t.mu.Lock()
		if err != nil {
			lastErr = err
			t.fails++
			n := t.fails
			t.mu.Unlock()
			if n == 1 || n%20 == 0 {
				logx.Printf("dns upstream %s: %v", shortSpec(t.spec), err)
			}
			continue
		}
		t.fails = 0
		t.mu.Unlock()
		if r.Rcode == dns.RcodeSuccess || r.Rcode == dns.RcodeNameError {
			u.cache.put(key, r)
		}
		r.Id = m.Id
		return r, nil
	}
	return nil, fmt.Errorf("all upstreams failed: %w", lastErr)
}

func (u *Upstream) ordered() []*target {
	var good, bad []*target
	for _, t := range u.targets {
		t.mu.Lock()
		failing := t.fails >= 3
		t.mu.Unlock()
		if failing {
			bad = append(bad, t)
		} else {
			good = append(good, t)
		}
	}
	return append(good, bad...)
}

// LookupA returns the IPv4 addresses of name.
func (u *Upstream) LookupA(name string) ([]netip.Addr, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), dns.TypeA)
	m.RecursionDesired = true
	r, err := u.Exchange(m)
	if err != nil {
		return nil, err
	}
	if r.Rcode == dns.RcodeNameError {
		return nil, fmt.Errorf("%s: %w", name, ErrNXDomain)
	}
	var out []netip.Addr
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok {
			if ip, ok := netip.AddrFromSlice(a.A.To4()); ok {
				out = append(out, ip)
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: %w", name, ErrNoA)
	}
	return out, nil
}

var (
	ErrNXDomain = errors.New("NXDOMAIN")
	ErrNoA      = errors.New("no A record")
)

func shortSpec(s string) string {
	if len(s) > 32 {
		return s[:32] + "…"
	}
	return s
}

type cacheEntry struct {
	msg     *dns.Msg
	expires time.Time
}

type cache struct {
	mu  sync.Mutex
	max int
	m   map[string]cacheEntry
}

func newCache(max int) *cache { return &cache{max: max, m: map[string]cacheEntry{}} }

func cacheKey(q dns.Question) string {
	return fmt.Sprintf("%s|%d|%d", strings.ToLower(q.Name), q.Qtype, q.Qclass)
}

func (c *cache) get(k string) *dns.Msg {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok {
		return nil
	}
	left := time.Until(e.expires)
	if left <= 0 {
		delete(c.m, k)
		return nil
	}
	r := e.msg.Copy()
	ttl := uint32(left.Seconds())
	for _, s := range [][]dns.RR{r.Answer, r.Ns, r.Extra} {
		for _, rr := range s {
			if rr.Header().Rrtype != dns.TypeOPT {
				rr.Header().Ttl = min(rr.Header().Ttl, ttl)
			}
		}
	}
	return r
}

func (c *cache) put(k string, r *dns.Msg) {
	ttl := uint32(3600)
	for _, s := range [][]dns.RR{r.Answer, r.Ns} {
		for _, rr := range s {
			ttl = min(ttl, rr.Header().Ttl)
		}
	}
	if len(r.Answer) == 0 && len(r.Ns) == 0 {
		ttl = 30
	}
	ttl = max(ttl, 10)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		now := time.Now()
		for key, e := range c.m {
			if now.After(e.expires) || len(c.m) >= c.max {
				delete(c.m, key)
			}
		}
	}
	c.m[k] = cacheEntry{msg: r.Copy(), expires: time.Now().Add(time.Duration(ttl) * time.Second)}
}
