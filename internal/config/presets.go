package config

// DefaultArgs is TLS record splitting: over some relay/tethering paths it is the only
// trick that survives, while segment-level tricks do not.
const DefaultArgs = "--tlsrec 1+s"

func Default() *Config {
	return &Config{
		Version:   Version,
		Active:    true,
		FakeRange: "100.79.0.0/16",
		Upstreams: []string{
			// quad9-dnscrypt-ip4-filter-pri, then AdGuard, then DoH.
			"sdns://AQMAAAAAAAAADDkuOS45Ljk6ODQ0MyBnyEe4yHWM0SAkVUO-dWdG3zTfHYTAC4xHA2jfgh2GPhkyLmRuc2NyeXB0LWNlcnQucXVhZDkubmV0",
			"sdns://AQMAAAAAAAAAEjE0OS4xMTIuMTEyLjk6ODQ0MyBnyEe4yHWM0SAkVUO-dWdG3zTfHYTAC4xHA2jfgh2GPhkyLmRuc2NyeXB0LWNlcnQucXVhZDkubmV0",
			"sdns://AQcAAAAAAAAAEjk0LjE0MC4xNC4xNDA6NTQ0MyC16ETWuDo-PhJo62gfvqcN48X6aNvWiBQdvy7AZrLa-iUyLmRuc2NyeXB0LnVuZmlsdGVyZWQubnMxLmFkZ3VhcmQuY29t",
			"https://1.1.1.1/dns-query",
		},
		LockDNS: true,
		// No sites are included: the user adds a profile for each site they need.
		Profiles: []Profile{},
	}
}

// Strategies are tried in order by "auto find".
var Strategies = []string{
	"--tlsrec 1+s",
	"--split 1 --tlsrec 1+s --disorder 1",
	"--tlsrec 3+s",
	"--tlsrec 1+s --tlsrec 5+s",
	"--split 1+s --tlsrec 1+s",
	"--disorder 1",
	"--split 1 --disorder 3+s --mod-http=h,d --tlsrec 1+s",
	"--fake -1 --ttl 8",
	"--oob 1",
	"--split 2 --disorder 1 --tlsrec 2+s",
}
