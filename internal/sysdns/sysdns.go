// Package sysdns points every network adapter's DNS at our resolver and puts the original
// settings back. The originals are written to disk first so a crash can be undone on the
// next start (or by `nomoredpi restore`).
package sysdns

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"nomoredpi/internal/logx"
)

var (
	ours4 = netip.MustParseAddr("127.0.0.1")
	ours6 = netip.MustParseAddr("::1")
)

type adapterState struct {
	GUID     string `json:"guid"`
	Name     string `json:"name"`
	Static4  string `json:"static4"` // registry NameServer value, "" = automatic (DHCP)
	Static6  string `json:"static6"`
	Fallback string `json:"fallback,omitempty"`
}

type Manager struct {
	statePath string
	skip      func(winipcfg.LUID) bool

	mu       sync.Mutex
	adapters map[string]*adapterState // by GUID
}

// New returns a manager; skip excludes adapters (our own tunnel).
func New(statePath string, skip func(winipcfg.LUID) bool) *Manager {
	return &Manager{statePath: statePath, skip: skip, adapters: map[string]*adapterState{}}
}

// RestoreLeftover undoes a takeover that a crashed run left behind.
func RestoreLeftover(statePath string) error {
	b, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var list []*adapterState
	if err := json.Unmarshal(b, &list); err != nil {
		os.Remove(statePath)
		return err
	}
	logx.Printf("sysdns: restoring DNS left by a previous run (%d adapters)", len(list))
	for _, a := range list {
		restoreOne(a)
	}
	return os.Remove(statePath)
}

// Apply takes over every adapter that is up and not yet ours. Safe to call repeatedly;
// it also catches adapters that appear or reset their DNS later.
func (m *Manager) Apply() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagDefault)
	if err != nil {
		return err
	}
	changed := false
	for _, aa := range list {
		if aa.IfType == winipcfg.IfTypeSoftwareLoopback || aa.OperStatus != winipcfg.IfOperStatusUp {
			continue
		}
		if m.skip != nil && m.skip(aa.LUID) {
			continue
		}
		guid, err := aa.LUID.GUID()
		if err != nil {
			continue
		}
		gs := guid.String()
		cur := currentDNS(aa)
		st, known := m.adapters[gs]
		if known && isOurs(cur) {
			continue
		}
		// Adapters without a resolver of their own (Hyper-V/WSL switches, the hotspot,
		// Teredo, overlay VPNs) are never asked for names, and changing them makes Windows
		// reconfigure ICS/HNS, which briefly disrupts the network. Leave them alone; if one
		// gets a resolver later (Wi-Fi connects), the next Apply takes it over.
		if !known && !hasResolver(cur) {
			continue
		}
		if !known {
			st = &adapterState{
				GUID:    gs,
				Name:    aa.FriendlyName(),
				Static4: readStatic(`SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\` + gs),
				Static6: readStatic(`SYSTEM\CurrentControlSet\Services\Tcpip6\Parameters\Interfaces\` + gs),
			}
			for _, a := range cur {
				if a.Is4() && !a.IsLoopback() {
					st.Fallback = a.String()
					break
				}
			}
			m.adapters[gs] = st
			changed = true
		}
		// Save before touching the system, so a crash mid-way can be undone.
		if changed {
			if err := m.saveLocked(); err != nil {
				return err
			}
			changed = false
		}
		servers := ours4.String()
		if st.Fallback != "" {
			// Fail-open: if the service dies, Windows falls back to the original resolver.
			// While we run, the DNS lock blocks port 53 so this is never used.
			servers += "," + st.Fallback
		}
		if err := setNameServers(*guid, false, servers); err != nil {
			logx.Printf("sysdns: %s v4: %v", st.Name, err)
			continue
		}
		_ = setNameServers(*guid, true, ours6.String())
		logx.Printf("sysdns: %s -> %s (was %q)", st.Name, servers, describe(st.Static4))
	}
	return nil
}

func describe(static string) string {
	if static == "" {
		return "automatic"
	}
	return static
}

// Restore puts every adapter back and removes the state file.
func (m *Manager) Restore() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, st := range m.adapters {
		restoreOne(st)
	}
	m.adapters = map[string]*adapterState{}
	os.Remove(m.statePath)
}

func restoreOne(st *adapterState) {
	guid, err := windows.GUIDFromString(st.GUID)
	if err != nil {
		return
	}
	if err := setNameServers(guid, false, st.Static4); err != nil {
		logx.Printf("sysdns: restore %s v4: %v", st.Name, err)
	}
	_ = setNameServers(guid, true, st.Static6)
	logx.Printf("sysdns: %s restored to %q", st.Name, describe(st.Static4))
}

func (m *Manager) saveLocked() error {
	list := make([]*adapterState, 0, len(m.adapters))
	for _, a := range m.adapters {
		list = append(list, a)
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := m.statePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.statePath)
}

// setNameServers changes only the server list, leaving the adapter's search list alone.
func setNameServers(guid windows.GUID, v6 bool, servers string) error {
	p, err := windows.UTF16PtrFromString(servers)
	if err != nil {
		return err
	}
	s := &winipcfg.DnsInterfaceSettings{
		Version:    winipcfg.DnsInterfaceSettingsVersion1,
		Flags:      winipcfg.DnsInterfaceSettingsFlagNameserver,
		NameServer: p,
	}
	if v6 {
		s.Flags |= winipcfg.DnsInterfaceSettingsFlagIPv6
	}
	if err := winipcfg.SetInterfaceDnsSettings(guid, s); err != nil {
		return fmt.Errorf("SetInterfaceDnsSettings: %w", err)
	}
	return nil
}

func readStatic(path string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("NameServer")
	return strings.TrimSpace(v)
}

func currentDNS(aa *winipcfg.IPAdapterAddresses) []netip.Addr {
	var out []netip.Addr
	for d := aa.FirstDNSServerAddress; d != nil; d = d.Next {
		sa, err := d.Address.Sockaddr.Sockaddr()
		if err != nil {
			continue
		}
		switch v := sa.(type) {
		case *syscall.SockaddrInet4:
			out = append(out, netip.AddrFrom4(v.Addr))
		case *syscall.SockaddrInet6:
			out = append(out, netip.AddrFrom16(v.Addr))
		}
	}
	return out
}

// hasResolver reports a real DNS server; fec0:0:0:ffff::1-3 are Windows' IPv6 placeholders.
func hasResolver(cur []netip.Addr) bool {
	placeholder := netip.MustParsePrefix("fec0:0:0:ffff::/64")
	for _, a := range cur {
		if !placeholder.Contains(a) {
			return true
		}
	}
	return false
}

func isOurs(cur []netip.Addr) bool {
	return len(cur) > 0 && cur[0] == ours4
}
