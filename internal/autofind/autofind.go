// Package autofind tries ciadpi strategies against a profile's probe host over the
// current network path and reports the first one that gets a real HTTPS response.
package autofind

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/sys/windows"

	"nomoredpi/internal/byedpi"
)

type Attempt struct {
	Args string `json:"args"`
	OK   bool   `json:"ok"`
	Ms   int64  `json:"ms"`
	Err  string `json:"err,omitempty"`
}

// Run tests strategies in order against host (resolved to ip). It stops at the first
// strategy that passes twice in a row. port is a free local port for the test ciadpi.
func Run(ciadpi, host, ip string, port int, strategies []string, progress func(Attempt)) (string, []Attempt) {
	var all []Attempt
	// Baseline: is the host blocked at all on this path?
	base := probe(0, host, ip)
	base.Args = "(direct, no byedpi)"
	all = append(all, base)
	if progress != nil {
		progress(base)
	}
	for _, s := range strategies {
		a := tryStrategy(ciadpi, host, ip, port, s)
		all = append(all, a)
		if progress != nil {
			progress(a)
		}
		if a.OK {
			return s, all
		}
	}
	return "", all
}

func tryStrategy(ciadpi, host, ip string, port int, args string) Attempt {
	cmd := exec.Command(ciadpi, byedpi.Args(port, args)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		return Attempt{Args: args, Err: err.Error()}
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	for i := 0; i < 30; i++ {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 100*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	a := probe(port, host, ip)
	if a.OK {
		// One lucky pass is not enough: require a second one.
		b := probe(port, host, ip)
		if !b.OK {
			a.OK, a.Err = false, "failed on second try: "+b.Err
		}
	}
	a.Args = args
	return a
}

// probe does a TLS handshake with SNI=host and one HTTP request, through ciadpi on
// socksPort (0 = direct).
func probe(socksPort int, host, ip string) Attempt {
	start := time.Now()
	d := &net.Dialer{Timeout: 6 * time.Second}
	var conn net.Conn
	var err error
	target := net.JoinHostPort(ip, "443")
	if socksPort == 0 {
		conn, err = d.Dial("tcp4", target)
	} else {
		var s proxy.Dialer
		s, err = proxy.SOCKS5("tcp", "127.0.0.1:"+strconv.Itoa(socksPort), nil, d)
		if err == nil {
			conn, err = s.Dial("tcp", target)
		}
	}
	if err != nil {
		return Attempt{Err: "connect: " + err.Error()}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	tc := tls.Client(conn, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}})
	if err := tc.Handshake(); err != nil {
		return Attempt{Err: "TLS: " + err.Error(), Ms: time.Since(start).Milliseconds()}
	}
	fmt.Fprintf(tc, "HEAD / HTTP/1.1\r\nHost: %s\r\nUser-Agent: Mozilla/5.0\r\nConnection: close\r\n\r\n", host)
	resp, err := http.ReadResponse(bufio.NewReader(tc), nil)
	if err != nil {
		return Attempt{Err: "HTTP: " + err.Error(), Ms: time.Since(start).Milliseconds()}
	}
	resp.Body.Close()
	return Attempt{OK: true, Ms: time.Since(start).Milliseconds(), Err: ""}
}
