package engine

import (
	"encoding/json"
	"errors"
	"testing"

	"nomoredpi/internal/config"
	"nomoredpi/internal/ipc"
)

// A caller without an elevated token must not change machine-wide DNS settings. Only the
// rejecting paths are tested: an accepted change would write config.json in ProgramData.
func TestSystemSettingsNeedElevation(t *testing.T) {
	e := &Engine{cfg: config.Default()}
	e.cfg.Normalize()

	for name, change := range map[string]func(c *config.Config){
		"lock":      func(c *config.Config) { c.LockDNS = !c.LockDNS },
		"range":     func(c *config.Config) { c.FakeRange = "198.18.0.0/16" },
		"upstreams": func(c *config.Config) { c.Upstreams = []string{"https://1.1.1.1/dns-query"} },
	} {
		n := e.Config()
		change(n)
		if err := e.SetConfig(n, false); !errors.Is(err, ErrNeedsAdmin) {
			t.Errorf("%s: SetConfig as standard user = %v, want ErrNeedsAdmin", name, err)
		}
	}

	params, _ := json.Marshal(SystemSettings{Upstreams: []string{"https://1.1.1.1/dns-query"}, FakeRange: "100.79.0.0/16"})
	if _, err := e.Handle(ipc.Caller{Elevated: false}, "setSystem", params); !errors.Is(err, ErrNeedsAdmin) {
		t.Errorf("setSystem as standard user = %v, want ErrNeedsAdmin", err)
	}
	if _, debug := debugHandle("stacks"); !debug {
		if _, err := e.Handle(ipc.Caller{}, "stacks", nil); err == nil {
			t.Error("stacks answered in a build without -tags debug")
		}
	}
	if !e.Config().LockDNS {
		t.Error("config changed")
	}
}
