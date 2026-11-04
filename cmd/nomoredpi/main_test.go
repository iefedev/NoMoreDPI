package main

import (
	"slices"
	"testing"
)

func TestParseSetDNS(t *testing.T) {
	s, err := parseSetDNS([]string{"--lock", "off", "--fake-range", "198.18.0.0/16", "--upstream", "sdns://x", "--upstream", "https://1.1.1.1/dns-query", "--child"})
	if err != nil {
		t.Fatal(err)
	}
	if s.LockDNS || s.FakeRange != "198.18.0.0/16" || !slices.Equal(s.Upstreams, []string{"sdns://x", "https://1.1.1.1/dns-query"}) {
		t.Errorf("parsed %+v", s)
	}
	for _, bad := range [][]string{
		nil,
		{"--lock", "on"},
		{"--lock", "on", "--fake-range", "100.79.0.0/16"},
		{"--lock", "maybe", "--fake-range", "100.79.0.0/16", "--upstream", "sdns://x"},
		{"--lock", "on", "--fake-range", "100.79.0.0/16", "--upstream"},
		{"--lock", "on", "--fake-range", "100.79.0.0/16", "--upstream", "sdns://x", "--port", "1"},
	} {
		if _, err := parseSetDNS(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
