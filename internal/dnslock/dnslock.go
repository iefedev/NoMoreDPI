// Package dnslock uses the Windows Filtering Platform so nothing on the machine can
// resolve names except through our resolver: plain DNS and DNS-over-TLS to anything but
// loopback are blocked, and so are well-known DNS-over-HTTPS endpoints. The session is
// dynamic, so every filter disappears the moment our process exits (fail-open).
package dnslock

import (
	"fmt"
	"net/netip"

	"github.com/tailscale/wf"
	"golang.org/x/sys/windows"
)

// Well-known DoH resolver addresses. Browsers that ship their own DoH connect to these
// by IP; blocking them makes them fall back to the system resolver.
var dohAddrs = []string{
	"8.8.8.8/32", "8.8.4.4/32", "2001:4860:4860::8888/128", "2001:4860:4860::8844/128",
	"1.1.1.1/32", "1.0.0.1/32", "2606:4700:4700::1111/128", "2606:4700:4700::1001/128",
	"1.1.1.2/32", "1.0.0.2/32", "1.1.1.3/32", "1.0.0.3/32",
	"162.159.36.1/32", "162.159.46.1/32", "104.16.248.249/32", "104.16.249.249/32",
	"9.9.9.9/32", "149.112.112.112/32", "9.9.9.10/32", "149.112.112.10/32", "9.9.9.11/32",
	"2620:fe::fe/128", "2620:fe::9/128", "2620:fe::10/128",
	"208.67.222.222/32", "208.67.220.220/32", "146.112.41.2/32",
	"94.140.14.14/32", "94.140.15.15/32", "94.140.14.140/32", "94.140.14.141/32",
	"45.90.28.0/24", "45.90.30.0/24", "185.228.168.9/32", "185.228.169.9/32",
	"194.242.2.2/32", "194.242.2.3/32", "185.222.222.222/32", "45.11.45.11/32",
	"76.76.2.0/24", "76.76.10.0/24", "223.5.5.5/32", "223.6.6.6/32", "77.88.8.8/32",
}

type Lock struct {
	s *wf.Session
}

var sublayerID = wf.SublayerID(mustGUID("{6f2a6b9e-3c55-4a1f-9e0b-6a2f1d7c4b11}"))

// Enable installs the filters. selfExe is permitted everywhere (our upstream may be a
// DoH server in the block list).
func Enable(selfExe string) (*Lock, error) {
	s, err := wf.New(&wf.Options{Name: "No More DPI", Description: "DNS lock", Dynamic: true})
	if err != nil {
		return nil, fmt.Errorf("could not open WFP session: %w", err)
	}
	l := &Lock{s: s}
	if err := l.install(selfExe); err != nil {
		s.Close()
		return nil, err
	}
	return l, nil
}

func (l *Lock) install(selfExe string) error {
	if err := l.s.AddSublayer(&wf.Sublayer{ID: sublayerID, Name: "No More DPI DNS lock", Weight: 0xFFFF}); err != nil {
		return fmt.Errorf("sublayer: %w", err)
	}
	appID, err := wf.AppID(selfExe)
	if err != nil {
		return fmt.Errorf("app id: %w", err)
	}
	layers := []wf.LayerID{wf.LayerALEAuthConnectV4, wf.LayerALEAuthConnectV6}
	loop4 := netip.MustParsePrefix("127.0.0.0/8")
	loop6 := netip.MustParsePrefix("::1/128")

	add := func(name string, layer wf.LayerID, weight uint64, action wf.Action, conds ...*wf.Match) error {
		g, err := windows.GenerateGUID()
		if err != nil {
			return err
		}
		return l.s.AddRule(&wf.Rule{
			ID: wf.RuleID(g), Name: name, Layer: layer, Sublayer: sublayerID,
			Weight: weight, Action: action, Conditions: conds,
		})
	}
	// The ALE layers are evaluated once per flow, and the DNS client service keeps
	// flows that were authorized before the lock was enabled, so queries on them would
	// get through. The transport layers see every packet. They carry no app id, which is
	// fine: our own upstreams never use port 53/853.
	for _, layer := range []wf.LayerID{wf.LayerOutboundTransportV4, wf.LayerOutboundTransportV6} {
		loop := loop4
		if layer == wf.LayerOutboundTransportV6 {
			loop = loop6
		}
		if err := add("permit loopback (transport)", layer, 14, wf.ActionPermit,
			&wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: loop}); err != nil {
			return err
		}
		for _, port := range []uint16{53, 853} {
			if err := add(fmt.Sprintf("block port %d (transport)", port), layer, 10, wf.ActionBlock,
				&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: port}); err != nil {
				return err
			}
		}
	}
	for _, layer := range layers {
		loop := loop4
		if layer == wf.LayerALEAuthConnectV6 {
			loop = loop6
		}
		// Highest weight wins inside the sublayer: permits first.
		if err := add("permit self", layer, 15, wf.ActionPermit,
			&wf.Match{Field: wf.FieldALEAppID, Op: wf.MatchTypeEqual, Value: appID}); err != nil {
			return err
		}
		if err := add("permit loopback", layer, 14, wf.ActionPermit,
			&wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: loop}); err != nil {
			return err
		}
		for _, port := range []uint16{53, 853} {
			if err := add(fmt.Sprintf("block port %d", port), layer, 10, wf.ActionBlock,
				&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: port}); err != nil {
				return err
			}
		}
		for _, s := range dohAddrs {
			p := netip.MustParsePrefix(s)
			if p.Addr().Is4() != (layer == wf.LayerALEAuthConnectV4) {
				continue
			}
			if err := add("block doh "+s, layer, 10, wf.ActionBlock,
				&wf.Match{Field: wf.FieldIPRemoteAddress, Op: wf.MatchTypeEqual, Value: p},
				&wf.Match{Field: wf.FieldIPRemotePort, Op: wf.MatchTypeEqual, Value: uint16(443)}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Lock) Close() {
	if l != nil && l.s != nil {
		l.s.Close()
		l.s = nil
	}
}

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}
