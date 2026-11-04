// Package fakeip hands out stable addresses from a private block for profile domains.
// The tunnel maps them back to the domain, so only profile traffic is ever routed to us.
package fakeip

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"time"
)

type Pool struct {
	mu       sync.Mutex
	prefix   netip.Prefix
	gateway  netip.Addr // the TUN's own address, never handed out
	byDomain map[string]netip.Addr
	byAddr   map[netip.Addr]string
	next     netip.Addr
	path     string
	dirty    bool
}

func New(prefix netip.Prefix, path string) (*Pool, error) {
	prefix = prefix.Masked()
	if !prefix.Addr().Is4() || prefix.Bits() > 24 {
		return nil, fmt.Errorf("fake range must be IPv4 /24 or larger: %s", prefix)
	}
	gw := prefix.Addr().Next()
	p := &Pool{
		prefix:   prefix,
		gateway:  gw,
		byDomain: map[string]netip.Addr{},
		byAddr:   map[netip.Addr]string{},
		next:     gw.Next(),
		path:     path,
	}
	p.load()
	return p, nil
}

// Gateway is the address assigned to the TUN adapter itself.
func (p *Pool) Gateway() netip.Addr  { return p.gateway }
func (p *Pool) Prefix() netip.Prefix { return p.prefix }

// Get returns the fake address for domain, allocating one if needed.
func (p *Pool) Get(domain string) netip.Addr {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a, ok := p.byDomain[domain]; ok {
		return a
	}
	a := p.alloc()
	if old, ok := p.byAddr[a]; ok {
		delete(p.byDomain, old)
	}
	p.byDomain[domain] = a
	p.byAddr[a] = domain
	p.dirty = true
	return a
}

// Lookup maps a fake address back to its domain.
func (p *Pool) Lookup(a netip.Addr) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.byAddr[a]
	return d, ok
}

func (p *Pool) Contains(a netip.Addr) bool { return p.prefix.Contains(a) }

func (p *Pool) alloc() netip.Addr {
	for range 2 {
		for a := p.next; p.prefix.Contains(a); a = a.Next() {
			if _, used := p.byAddr[a]; !used && !isBroadcast(p.prefix, a) {
				p.next = a.Next()
				return a
			}
		}
		p.next = p.gateway.Next()
	}
	// Full: wrap around and reuse the oldest-numbered slot.
	a := p.next
	p.next = a.Next()
	if !p.prefix.Contains(p.next) || isBroadcast(p.prefix, p.next) {
		p.next = p.gateway.Next()
	}
	return a
}

func isBroadcast(pfx netip.Prefix, a netip.Addr) bool {
	b := a.As4()
	host := 32 - pfx.Bits()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return v&(1<<host-1) == 1<<host-1
}

type persisted struct {
	Prefix  string            `json:"prefix"`
	Domains map[string]string `json:"domains"`
}

func (p *Pool) load() {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var s persisted
	if json.Unmarshal(b, &s) != nil || s.Prefix != p.prefix.String() {
		return
	}
	for d, as := range s.Domains {
		a, err := netip.ParseAddr(as)
		if err != nil || !p.prefix.Contains(a) || a == p.gateway {
			continue
		}
		p.byDomain[d] = a
		p.byAddr[a] = d
		if p.next.Less(a.Next()) {
			p.next = a.Next()
		}
	}
}

// Flush writes the mapping to disk if it changed, so fake addresses that apps have
// cached keep working after a restart.
func (p *Pool) Flush() error {
	p.mu.Lock()
	if !p.dirty {
		p.mu.Unlock()
		return nil
	}
	s := persisted{Prefix: p.prefix.String(), Domains: make(map[string]string, len(p.byDomain))}
	for d, a := range p.byDomain {
		s.Domains[d] = a.String()
	}
	p.dirty = false
	p.mu.Unlock()
	b, _ := json.Marshal(s)
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

// FlushLoop persists periodically until stop closes.
func (p *Pool) FlushLoop(stop <-chan struct{}) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			_ = p.Flush()
			return
		case <-t.C:
			_ = p.Flush()
		}
	}
}
