package autofind

import (
	"os"
	"path/filepath"
	"testing"

	"nomoredpi/internal/config"
	"nomoredpi/internal/dnsserver"
)

// Real network test: needs dist\ciadpi.exe and a path where discord.com is DPI-blocked.
func TestFindsStrategyForDiscord(t *testing.T) {
	ciadpi, _ := filepath.Abs("../../dist/ciadpi.exe")
	if _, err := os.Stat(ciadpi); err != nil {
		t.Skip("build first: ", err)
	}
	up, _ := dnsserver.NewUpstream(config.Default().Upstreams)
	ips, err := up.LookupA("discord.com")
	if err != nil {
		t.Fatal(err)
	}
	found, attempts := Run(ciadpi, "discord.com", ips[0].String(), 19777, config.Strategies[:3], nil)
	for _, a := range attempts {
		t.Logf("%-40q ok=%v %4dms %s", a.Args, a.OK, a.Ms, a.Err)
	}
	if found == "" {
		t.Fatal("no strategy worked")
	}
}
