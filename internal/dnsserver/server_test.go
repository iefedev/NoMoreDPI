package dnsserver

import (
	"net/netip"
	"path/filepath"
	"testing"

	"github.com/miekg/dns"

	"nomoredpi/internal/config"
	"nomoredpi/internal/fakeip"
)

// Talks to the real upstreams: run with network access.
func TestServerAnswers(t *testing.T) {
	pool, err := fakeip.New(netip.MustParsePrefix("100.79.0.0/16"), filepath.Join(t.TempDir(), "f.json"))
	if err != nil {
		t.Fatal(err)
	}
	up, err := NewUpstream(config.Default().Upstreams)
	if err != nil {
		t.Fatal(err)
	}
	s := New(pool, up)
	s.SetMatcher(Matcher{"discord.com": "discord", "discord.gg": "discord"})
	if err := s.Listen([]string{"127.0.0.1:15353"}); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c := new(dns.Client)
	ask := func(name string, qt uint16) *dns.Msg {
		m := new(dns.Msg)
		m.SetQuestion(name, qt)
		r, _, err := c.Exchange(m, "127.0.0.1:15353")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return r
	}

	r := ask("gateway.discord.gg.", dns.TypeA)
	if len(r.Answer) != 1 {
		t.Fatalf("profile domain: want 1 fake answer, got %v", r)
	}
	fake, _ := netip.AddrFromSlice(r.Answer[0].(*dns.A).A.To4())
	if !pool.Contains(fake) {
		t.Fatalf("answer %s is not fake", fake)
	}
	if d, _ := pool.Lookup(fake); d != "gateway.discord.gg" {
		t.Fatalf("lookup %s = %q", fake, d)
	}
	if r := ask("discord.com.", dns.TypeAAAA); len(r.Answer) != 0 || r.Rcode != dns.RcodeSuccess {
		t.Fatalf("AAAA for profile domain must be empty NOERROR: %v", r)
	}
	if r := ask("discord.com.", dns.TypeHTTPS); len(r.Answer) != 0 {
		t.Fatalf("HTTPS for profile domain must be empty: %v", r)
	}
	r = ask("example.com.", dns.TypeA)
	if r.Rcode != dns.RcodeSuccess || len(r.Answer) == 0 {
		t.Fatalf("other domain should resolve normally: %v", r)
	}
	for _, rr := range r.Answer {
		if a, ok := rr.(*dns.A); ok {
			ip, _ := netip.AddrFromSlice(a.A.To4())
			if pool.Contains(ip) {
				t.Fatalf("other domain got a fake address")
			}
		}
	}
	if r := ask("dns.google.", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Fatalf("DoH host must be NXDOMAIN, got %v", dns.RcodeToString[r.Rcode])
	}
	if r := ask("use-application-dns.net.", dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Fatalf("Firefox canary must be NXDOMAIN")
	}
	// Cloudflare answers NODATA (not NXDOMAIN) for missing names; either way no fake address.
	if r := ask("doesnotexist-nomoredpi-test.discord.com.", dns.TypeA); len(r.Answer) != 0 {
		t.Fatalf("nonexistent profile name must not get a fake address: %v", r)
	}
	t.Logf("gateway.discord.gg -> %s; stats q=%d faked=%d", fake, s.Stats.Queries.Load(), s.Stats.Faked.Load())
}
