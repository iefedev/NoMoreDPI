package config

import (
	"os"
	"strings"
	"testing"
)

func TestCheckArgs(t *testing.T) {
	good := []string{
		"", "--fake -1 --ttl 8", "--fake-data :GET / HTTP/1.1", "--fake-data=:x", "--mod-http=h,d",
		"--hosts :discord.com", "--ipset=:1.2.3.0/24", "--no-udp --split 1", "--split -1",
	}
	for _, ok := range append(good, Strategies...) {
		if err := CheckArgs(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	bad := []string{
		"-s1", "-UD", "--port 1", "--ip=0.0.0.0", "--pidfile x", "--hosts C:\\x", "--fake-data C:\\secret",
		"--fake-data=C:\\x", "--debug 2", "--cache-dump x", "--conn-ip 1.2.3.4", "--connect-to 1.2.3.4",
		// getopt_long accepts unambiguous prefixes, so these reach the denied options.
		"--fake-d C:\\x", "--fake-d=C:\\x", "--cache-d x", "--hos x", "--ips x", "--conn 1.2.3.4",
		"--connect-t x", "--pidf x", "--vers", "--he",
		// A value-taking option swallows the next field, so a file path can hide there.
		"--fake-data --split", "--hosts",
		"--", "--split 1 -- --hosts x", "--no-udp=1",
	}
	for _, b := range bad {
		if CheckArgs(b) == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

func TestCheckFakeRange(t *testing.T) {
	for _, ok := range []string{"100.79.0.0/16", "100.64.0.0/24", "198.18.0.0/16", "198.19.5.0/24"} {
		if err := CheckFakeRange(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "0.0.0.0/1", "10.0.0.0/8", "10.0.0.0/16", "100.64.0.0/10", "100.128.0.0/16",
		"192.168.1.0/24", "100.79.0.0/25", "fd00::/64", "198.20.0.0/16", "x"} {
		if CheckFakeRange(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDefaultValidates(t *testing.T) {
	c := Default()
	c.Normalize()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A first run writes no profiles, as an empty list (not null) that the UI can show as empty.
func TestFirstRunHasNoProfiles(t *testing.T) {
	t.Setenv("ProgramData", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Profiles) != 0 {
		t.Fatalf("first run created %d profiles", len(c.Profiles))
	}
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"profiles": []`) {
		t.Fatalf("config.json has no empty profile list:\n%s", b)
	}
}
