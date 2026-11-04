// Package config holds the persisted settings of No More DPI.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const Version = 1

// Profile is one group of domains served by its own ciadpi process.
type Profile struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Enabled bool     `json:"enabled"`
	Domains []string `json:"domains"`
	// Args are ciadpi desync arguments (listen address/port are added by the service).
	Args string `json:"args"`
	// Port is the local SOCKS5 port of this profile's ciadpi.
	Port int `json:"port"`
	// Probe is the host "auto find" tests against; defaults to the first domain.
	Probe string `json:"probe,omitempty"`
}

type Config struct {
	Version int `json:"version"`
	// Active is the global on/off switch (tray "Pause").
	Active bool `json:"active"`
	// FakeRange is the address block handed out for profile domains.
	FakeRange string `json:"fakeRange"`
	// Upstreams are DNSCrypt stamps (sdns://) or DoH URLs, tried in order.
	Upstreams []string  `json:"upstreams"`
	LockDNS   bool      `json:"lockDns"`
	Profiles  []Profile `json:"profiles"`
}

func Dir() string {
	if pd := os.Getenv("ProgramData"); pd != "" {
		return filepath.Join(pd, "No More DPI")
	}
	return `C:\ProgramData\No More DPI`
}

func Path() string { return filepath.Join(Dir(), "config.json") }

var mu sync.Mutex

// Load reads the config, creating it from defaults on first run.
func Load() (*Config, error) {
	mu.Lock()
	defer mu.Unlock()
	b, err := os.ReadFile(Path())
	if errors.Is(err, os.ErrNotExist) {
		c := Default()
		return c, save(c)
	}
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("config.json is corrupt: %w", err)
	}
	c.fill()
	return c, c.Validate()
}

func Save(c *Config) error {
	mu.Lock()
	defer mu.Unlock()
	return save(c)
}

func save(c *Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path())
}

func (c *Config) fill() {
	d := Default()
	if c.Version == 0 {
		c.Version = Version
	}
	if c.FakeRange == "" {
		c.FakeRange = d.FakeRange
	}
	if len(c.Upstreams) == 0 {
		c.Upstreams = d.Upstreams
	}
	c.Normalize()
}

// Normalize cleans domain lists and assigns missing ids and ports.
func (c *Config) Normalize() {
	used := map[int]bool{}
	for _, p := range c.Profiles {
		if p.Port != 0 {
			used[p.Port] = true
		}
	}
	for i := range c.Profiles {
		p := &c.Profiles[i]
		p.ID = strings.TrimSpace(p.ID)
		if p.ID == "" {
			p.ID = fmt.Sprintf("p%d", i+1)
		}
		p.Domains = cleanDomains(p.Domains)
		p.Args = strings.Join(strings.Fields(p.Args), " ")
		if p.Port == 0 {
			for port := 19090; ; port++ {
				if !used[port] {
					p.Port, used[port] = port, true
					break
				}
			}
		}
	}
}

func (c *Config) Validate() error {
	if err := CheckFakeRange(c.FakeRange); err != nil {
		return err
	}
	ids := map[string]bool{}
	ports := map[int]bool{}
	for _, p := range c.Profiles {
		if ids[p.ID] {
			return fmt.Errorf("duplicate profile id: %s", p.ID)
		}
		if ports[p.Port] {
			return fmt.Errorf("duplicate port: %d", p.Port)
		}
		if p.Port < 1024 || p.Port > 65535 {
			return fmt.Errorf("%s: invalid port %d", p.Name, p.Port)
		}
		if err := CheckArgs(p.Args); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
		ids[p.ID], ports[p.Port] = true, true
	}
	return nil
}

type argKind int

const (
	flagArg   argKind = iota // takes no value
	valueArg                 // takes a value
	inlineArg                // takes a value that ciadpi reads as a file path unless it starts with ':'
)

// allowedArgs are the ciadpi desync options a profile may use, by exact long name.
// Everything else is rejected: options that move the listener (the service owns it), read
// or write files, or detach the process. ciadpi runs as SYSTEM and any signed-in user can
// edit profiles over the pipe. The match must be exact because ciadpi's getopt_long also
// accepts any unambiguous prefix (--fake-d for --fake-data, --cache-d for --cache-dump).
var allowedArgs = map[string]argKind{
	"split": valueArg, "disorder": valueArg, "oob": valueArg, "disoob": valueArg,
	"fake": valueArg, "fake-sni": valueArg, "ttl": valueArg, "fake-offset": valueArg,
	"fake-tls-mod": valueArg, "oob-data": valueArg, "mod-http": valueArg, "tlsrec": valueArg,
	"tlsminor": valueArg, "udp-fake": valueArg, "def-ttl": valueArg, "auto": valueArg,
	"auto-mode": valueArg, "cache-ttl": valueArg, "timeout": valueArg, "proto": valueArg,
	"pf": valueArg, "round": valueArg, "copy": valueArg, "buf-size": valueArg,
	"max-conn": valueArg, "await-int": valueArg,
	"no-domain": flagArg, "no-udp": flagArg, "no-ipv6": flagArg, "wait-send": flagArg,
	"tfo": flagArg, "drop-sack": flagArg,
	"fake-data": inlineArg, "hosts": inlineArg, "ipset": inlineArg,
}

// CheckArgs accepts only long options from allowedArgs (short ones can be bundled, e.g.
// -UD, which hides what they do), and only inline data (":text") for options that would
// otherwise read a file.
func CheckArgs(args string) error {
	fields := strings.Fields(args)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "--" {
			return errors.New(`a bare "--" is not allowed in a profile`)
		}
		if !strings.HasPrefix(f, "-") || len(f) < 2 || f[1] >= '0' && f[1] <= '9' {
			continue // a value, including negative numbers like "--fake -1"
		}
		if !strings.HasPrefix(f, "--") {
			return fmt.Errorf("use long option names (for example --split, not -s): %s", f)
		}
		name, value, inline := strings.Cut(f[2:], "=")
		kind, ok := allowedArgs[name]
		if !ok {
			return fmt.Errorf("option --%s is not allowed in a profile", name)
		}
		if kind == flagArg {
			if inline {
				return fmt.Errorf("--%s takes no value", name)
			}
			continue
		}
		if !inline {
			// getopt takes the next field as the value even if it starts with "-".
			if i+1 >= len(fields) {
				return fmt.Errorf("--%s needs a value", name)
			}
			i++
			value = fields[i]
		}
		if kind == inlineArg && !strings.HasPrefix(value, ":") {
			return fmt.Errorf("--%s must be inline data (:text), not a file", name)
		}
	}
	return nil
}

// fakeRangeParents are the blocks a fake range may come from: the carrier-grade NAT block
// and the benchmarking block are not used by ordinary destinations, so routing a part of
// them into the tunnel cannot capture unrelated traffic.
var fakeRangeParents = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("198.18.0.0/15"),
}

// CheckFakeRange accepts an IPv4 block between /16 and /24 inside fakeRangeParents.
func CheckFakeRange(s string) error {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return fmt.Errorf("fake IP range: %w", err)
	}
	if !p.Addr().Is4() || p.Bits() < 16 || p.Bits() > 24 {
		return fmt.Errorf("fake IP range must be an IPv4 block between /16 and /24: %s", s)
	}
	for _, parent := range fakeRangeParents {
		if parent.Bits() <= p.Bits() && parent.Contains(p.Addr()) {
			return nil
		}
	}
	return fmt.Errorf("fake IP range must be inside 100.64.0.0/10 or 198.18.0.0/15: %s", s)
}

func cleanDomains(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimPrefix(d, "https://")
		d = strings.TrimPrefix(d, "http://")
		if i := strings.IndexAny(d, "/:"); i >= 0 {
			d = d[:i]
		}
		d = strings.Trim(d, ".")
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// Clone returns a deep copy.
func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	n := &Config{}
	_ = json.Unmarshal(b, n)
	return n
}
