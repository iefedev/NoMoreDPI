// Package tunnel owns the Wintun adapter that the fake address block is routed to, and
// forwards each flow arriving there to the real destination: TCP through the profile's
// ciadpi, UDP directly.
package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"golang.org/x/net/proxy"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
	"gvisor.dev/gvisor/pkg/tcpip/stack"

	"nomoredpi/internal/logx"
)

// Router tells the tunnel where a fake address really goes.
type Router interface {
	// Resolve returns the domain behind fake, its real IPv4 addresses, and the SOCKS
	// port of the profile serving it (0 = connect directly).
	Resolve(fake netip.Addr) (domain string, real []netip.Addr, socksPort int, profile string, err error)
}

type ProfileStats struct {
	Active atomic.Int64
	Total  atomic.Uint64
	Failed atomic.Uint64
	Bytes  atomic.Uint64
}

type Tunnel struct {
	dev     *device
	stack   *stack.Stack
	router  Router
	gateway netip.Addr
	prefix  netip.Prefix

	statsMu sync.Mutex
	stats   map[string]*ProfileStats
}

const AdapterName = "No More DPI"

// Start creates the adapter, gives it gateway/prefix (which also installs the on-link
// route for the whole fake block) and starts the userspace network stack.
func Start(gateway netip.Addr, prefix netip.Prefix, r Router) (*Tunnel, error) {
	dev, err := openDevice(AdapterName, 1500)
	if err != nil {
		return nil, err
	}
	t := &Tunnel{dev: dev, router: r, gateway: gateway, prefix: prefix, stats: map[string]*ProfileStats{}}
	if err := t.configure(gateway, prefix); err != nil {
		dev.Close()
		return nil, err
	}
	st, err := core.CreateStack(&core.Config{LinkEndpoint: dev, TransportHandler: t})
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("network stack: %w", err)
	}
	t.stack = st
	return t, nil
}

func (t *Tunnel) configure(gateway netip.Addr, prefix netip.Prefix) error {
	luid := t.dev.LUID()
	if err := luid.SetIPAddressesForFamily(winipcfg.AddressFamily(2), []netip.Prefix{netip.PrefixFrom(gateway, prefix.Bits())}); err != nil {
		return fmt.Errorf("adapter address: %w", err)
	}
	ipif, err := luid.IPInterface(winipcfg.AddressFamily(2))
	if err != nil {
		return err
	}
	ipif.UseAutomaticMetric = false
	ipif.Metric = 5
	ipif.NLMTU = 1500
	if err := ipif.Set(); err != nil {
		return fmt.Errorf("adapter metric: %w", err)
	}
	// No DNS on this adapter: queries must go to our resolver via the others.
	_ = luid.FlushDNS(winipcfg.AddressFamily(2))
	_ = luid.FlushDNS(winipcfg.AddressFamily(23))
	return nil
}

func (t *Tunnel) LUID() winipcfg.LUID { return t.dev.LUID() }

// EnsureConfigured puts the adapter address back if Windows dropped it. That happens when
// every adapter is rebound, for example after a network filter driver is installed or
// removed: the adapter comes back with a 169.254 address and the fake block no longer
// reaches us. Reports whether a repair was needed.
func (t *Tunnel) EnsureConfigured() (bool, error) {
	if _, err := t.dev.LUID().IPAddress(t.gateway); err == nil {
		return false, nil
	}
	return true, t.configure(t.gateway, t.prefix)
}

// Close removes the adapter first: stack.Wait waits for the packet reader, which only
// returns once the Wintun session is closed (the other order deadlocks).
func (t *Tunnel) Close() {
	t.dev.closeAdapter()
	if t.stack != nil {
		t.stack.Close()
		t.stack.Wait()
	}
	t.dev.Endpoint.Close()
}

// Stats returns a snapshot per profile id ("" = direct).
func (t *Tunnel) Stats() map[string]*ProfileStats {
	t.statsMu.Lock()
	defer t.statsMu.Unlock()
	out := make(map[string]*ProfileStats, len(t.stats))
	for k, v := range t.stats {
		out[k] = v
	}
	return out
}

func (t *Tunnel) statFor(profile string) *ProfileStats {
	t.statsMu.Lock()
	defer t.statsMu.Unlock()
	s := t.stats[profile]
	if s == nil {
		s = &ProfileStats{}
		t.stats[profile] = s
	}
	return s
}

func addrOf(id stack.TransportEndpointID) netip.Addr {
	a, _ := netip.AddrFromSlice(id.LocalAddress.AsSlice())
	return a.Unmap()
}

func (t *Tunnel) HandleTCP(c adapter.TCPConn) {
	go t.handleTCP(c)
}

func (t *Tunnel) handleTCP(c adapter.TCPConn) {
	defer c.Close()
	id := c.ID()
	fake := addrOf(id)
	port := strconv.Itoa(int(id.LocalPort))
	domain, reals, socksPort, profile, err := t.router.Resolve(fake)
	if err != nil {
		logx.Printf("tcp %s:%s: %v", fake, port, err)
		return
	}
	st := t.statFor(profile)
	st.Total.Add(1)
	st.Active.Add(1)
	defer st.Active.Add(-1)

	var up net.Conn
	for i, ip := range reals {
		if i == 2 {
			break
		}
		target := net.JoinHostPort(ip.String(), port)
		up, err = dialVia(socksPort, target)
		if err == nil {
			break
		}
	}
	if err != nil {
		st.Failed.Add(1)
		logx.Printf("tcp %s (%s:%s) via %s: %v", domain, reals[0], port, via(socksPort), err)
		return
	}
	defer up.Close()
	pipe(c, up, &st.Bytes)
}

func via(port int) string {
	if port == 0 {
		return "direct"
	}
	return "ciadpi:" + strconv.Itoa(port)
}

func dialVia(socksPort int, target string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	if socksPort == 0 {
		return d.Dial("tcp4", target)
	}
	s, err := proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(socksPort), nil, d)
	if err != nil {
		return nil, err
	}
	return s.Dial("tcp", target)
}

type closeWriter interface{ CloseWrite() error }

// countingWriter keeps the byte counter live while a long connection is open.
type countingWriter struct {
	w io.Writer
	n *atomic.Uint64
}

func (c countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n.Add(uint64(n))
	return n, err
}

func pipe(a, b net.Conn, bytes *atomic.Uint64) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(countingWriter{dst, bytes}, src)
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
		} else {
			_ = dst.Close()
		}
		// The other direction may wait forever on a peer that never closes.
		_ = src.SetReadDeadline(time.Now().Add(30 * time.Second))
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}

func (t *Tunnel) HandleUDP(c adapter.UDPConn) {
	go t.handleUDP(c)
}

const udpIdle = 2 * time.Minute

func (t *Tunnel) handleUDP(c adapter.UDPConn) {
	defer c.Close()
	id := c.ID()
	if id.LocalPort == 443 {
		// QUIC: ciadpi cannot desync it, so make browsers fall back to TCP/TLS.
		return
	}
	fake := addrOf(id)
	_, reals, _, profile, err := t.router.Resolve(fake)
	if err != nil {
		return
	}
	up, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(reals[0], id.LocalPort)))
	if err != nil {
		return
	}
	defer up.Close()
	st := t.statFor(profile)
	st.Total.Add(1)
	st.Active.Add(1)
	defer st.Active.Add(-1)

	done := make(chan struct{}, 2)
	relay := func(dst io.Writer, src net.Conn) {
		buf := make([]byte, 65535)
		for {
			_ = src.SetReadDeadline(time.Now().Add(udpIdle))
			n, err := src.Read(buf)
			if err != nil {
				break
			}
			if _, err := dst.Write(buf[:n]); err != nil && !errors.Is(err, net.ErrClosed) {
				break
			}
			st.Bytes.Add(uint64(n))
		}
		done <- struct{}{}
	}
	go relay(up, c)
	go relay(c, up)
	<-done
}
