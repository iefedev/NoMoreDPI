package tunnel

import (
	"fmt"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/core/device/iobased"
	wgtun "golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// device is a Wintun adapter exposed as a gVisor link endpoint. It mirrors
// tun2socks' own TUN device but keeps the adapter LUID, which we need for addressing.
type device struct {
	*iobased.Endpoint
	nt     *wgtun.NativeTun
	name   string
	rSizes []int
	rBuffs [][]byte
	wBuffs [][]byte
	rMu    sync.Mutex
	wMu    sync.Mutex

	closeOnce sync.Once
}

func openDevice(name string, mtu int) (_ *device, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("wintun: %v", r)
		}
	}()
	wgtun.WintunTunnelType = "No More DPI"
	t, err := wgtun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("could not create the Wintun adapter: %w", err)
	}
	d := &device{
		nt:     t.(*wgtun.NativeTun),
		name:   name,
		rSizes: make([]int, 1),
		rBuffs: make([][]byte, 1),
		wBuffs: make([][]byte, 1),
	}
	realMTU, err := d.nt.MTU()
	if err != nil {
		d.nt.Close()
		return nil, err
	}
	ep, err := iobased.New(d, uint32(realMTU), 0)
	if err != nil {
		d.nt.Close()
		return nil, err
	}
	d.Endpoint = ep
	return d, nil
}

func (d *device) LUID() winipcfg.LUID { return winipcfg.LUID(d.nt.LUID()) }
func (d *device) Name() string        { return d.name }
func (d *device) Type() string        { return "wintun" }

func (d *device) Read(p []byte) (int, error) {
	d.rMu.Lock()
	defer d.rMu.Unlock()
	d.rBuffs[0] = p
	_, err := d.nt.Read(d.rBuffs, d.rSizes, 0)
	return d.rSizes[0], err
}

func (d *device) Write(p []byte) (int, error) {
	d.wMu.Lock()
	defer d.wMu.Unlock()
	d.wBuffs[0] = p
	return d.nt.Write(d.wBuffs, 0)
}

func (d *device) Close() {
	defer d.Endpoint.Close()
	d.closeAdapter()
}

func (d *device) closeAdapter() {
	d.closeOnce.Do(func() { _ = d.nt.Close() })
}
