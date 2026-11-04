// Package dnsserver is the system resolver while No More DPI is active. Profile domains
// get fake addresses (routed into the tunnel); everything else is answered from the
// encrypted upstream unchanged.
package dnsserver

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/miekg/dns"

	"nomoredpi/internal/fakeip"
	"nomoredpi/internal/logx"
)

// Matcher maps a domain (or any of its parents) to a profile id.
type Matcher map[string]string

func (m Matcher) Match(name string) (string, bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	for {
		if id, ok := m[name]; ok {
			return id, true
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return "", false
		}
		name = name[i+1:]
	}
}

// dohHosts are answered NXDOMAIN so browsers cannot switch to their own encrypted DNS
// and skip us. Firefox also checks the canary domain before enabling DoH by itself.
var dohHosts = Matcher{
	"use-application-dns.net": "", "dns.google": "", "dns.google.com": "",
	"cloudflare-dns.com": "", "dns.cloudflare.com": "", "one.one.one.one": "",
	"dns.quad9.net": "", "dns9.quad9.net": "", "dns10.quad9.net": "", "dns11.quad9.net": "",
	"doh.opendns.com": "", "doh.familyshield.opendns.com": "", "dns.adguard.com": "",
	"dns.adguard-dns.com": "", "dns.nextdns.io": "", "doh.cleanbrowsing.org": "",
	"dns.mullvad.net": "", "doh.mullvad.net": "", "doh.dns.sb": "", "dns.controld.com": "",
	"freedns.controld.com": "", "doh.xfinity.com": "", "chrome.cloudflare-dns.com": "",
	"mozilla.cloudflare-dns.com": "", "dns.alidns.com": "", "doh.pub": "", "dns.yandex.net": "",
}

type Stats struct {
	Queries    atomic.Uint64
	Faked      atomic.Uint64
	Upstream   atomic.Uint64
	Failures   atomic.Uint64
	BlockedDoH atomic.Uint64
}

type Server struct {
	pool    *fakeip.Pool
	up      atomic.Pointer[Upstream]
	matcher atomic.Pointer[Matcher]
	Stats   Stats

	mu      sync.Mutex
	servers []*dns.Server
	seenDoH sync.Map
}

func New(pool *fakeip.Pool, up *Upstream) *Server {
	s := &Server{pool: pool}
	s.up.Store(up)
	empty := Matcher{}
	s.matcher.Store(&empty)
	return s
}

func (s *Server) SetMatcher(m Matcher)             { s.matcher.Store(&m) }
func (s *Server) SetUpstream(u *Upstream)          { s.up.Store(u) }
func (s *Server) Upstream() *Upstream              { return s.up.Load() }
func (s *Server) Match(name string) (string, bool) { return s.matcher.Load().Match(name) }

// Listen binds UDP and TCP on the given addresses. It fails if any address is taken.
func (s *Server) Listen(addrs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var started []*dns.Server
	fail := func(err error) error {
		for _, srv := range started {
			_ = srv.Shutdown()
		}
		return err
	}
	for _, a := range addrs {
		pc, err := net.ListenPacket("udp", a)
		if err != nil {
			return fail(fmt.Errorf("could not open DNS port (%s): %w", a, err))
		}
		ln, err := net.Listen("tcp", a)
		if err != nil {
			pc.Close()
			return fail(fmt.Errorf("could not open DNS port (%s/tcp): %w", a, err))
		}
		for _, srv := range []*dns.Server{
			{PacketConn: pc, Handler: s, UDPSize: 4096},
			{Listener: ln, Handler: s},
		} {
			started = append(started, srv)
			go func(srv *dns.Server) {
				if err := srv.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
					logx.Printf("dns server: %v", err)
				}
			}(srv)
		}
	}
	s.servers = started
	return nil
}

func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, srv := range s.servers {
		_ = srv.Shutdown()
	}
	s.servers = nil
}

func (s *Server) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	s.Stats.Queries.Add(1)
	resp := s.answer(r)
	if resp == nil {
		resp = new(dns.Msg)
		resp.SetRcode(r, dns.RcodeServerFailure)
		s.Stats.Failures.Add(1)
	}
	if _, isUDP := w.RemoteAddr().(*net.UDPAddr); isUDP {
		size := 512
		if o := r.IsEdns0(); o != nil {
			size = int(o.UDPSize())
		}
		resp.Truncate(size)
	}
	_ = w.WriteMsg(resp)
}

func (s *Server) answer(r *dns.Msg) *dns.Msg {
	if r.Opcode != dns.OpcodeQuery || len(r.Question) != 1 {
		m := new(dns.Msg)
		return m.SetRcode(r, dns.RcodeNotImplemented)
	}
	q := r.Question[0]
	name := strings.TrimSuffix(strings.ToLower(q.Name), ".")

	if _, ok := dohHosts.Match(name); ok {
		s.Stats.BlockedDoH.Add(1)
		if _, seen := s.seenDoH.LoadOrStore(name, true); !seen {
			logx.Printf("dns: refused DoH bootstrap %s (type %s)", name, dns.TypeToString[q.Qtype])
		}
		m := new(dns.Msg)
		return m.SetRcode(r, dns.RcodeNameError)
	}

	if _, ok := s.Match(name); ok && q.Qclass == dns.ClassINET {
		switch q.Qtype {
		case dns.TypeA:
			return s.fakeA(r, name)
		case dns.TypeAAAA, dns.TypeHTTPS, dns.TypeSVCB:
			// Answering these would hand out real addresses (or hints) that bypass the
			// tunnel. An empty NOERROR makes clients use the fake IPv4 address.
			m := new(dns.Msg)
			m.SetReply(r)
			m.RecursionAvailable = true
			return m
		}
	}

	s.Stats.Upstream.Add(1)
	resp, err := s.up.Load().Exchange(r)
	if err != nil {
		return nil
	}
	resp.Compress = true
	return resp
}

func (s *Server) fakeA(r *dns.Msg, name string) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.RecursionAvailable = true
	// Make sure the name exists and warm the cache the tunnel uses for the real address.
	if _, err := s.up.Load().LookupA(name); err != nil {
		switch {
		case errors.Is(err, ErrNXDomain):
			return m.SetRcode(r, dns.RcodeNameError)
		case errors.Is(err, ErrNoA):
			return m
		}
		return nil
	}
	s.Stats.Faked.Add(1)
	a := s.pool.Get(name)
	m.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 300},
		A:   a.AsSlice(),
	}}
	return m
}
