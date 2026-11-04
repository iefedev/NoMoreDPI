// Package ipc is the named-pipe channel between the service and the tray app: one JSON
// request per line, one JSON response per line.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const PipeName = `\\.\pipe\nomoredpi`

// SYSTEM and Administrators: full; the logged-on (interactive) user: read/write.
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

// Caller is the process on the other end of a connection.
type Caller struct {
	PID uint32
	// Elevated is true for an administrator running elevated, and for SYSTEM. A standard
	// user, or an administrator without UAC elevation, is not.
	Elevated bool
}

// callerOf identifies the client process when the connection is accepted, while the
// process that opened it is certainly still alive.
func callerOf(c net.Conn) Caller {
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return Caller{}
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(f.Fd()), &pid) != nil {
		return Caller{}
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Caller{PID: pid}
	}
	defer windows.CloseHandle(h)
	var tok windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok) != nil {
		return Caller{PID: pid}
	}
	defer tok.Close()
	return Caller{PID: pid, Elevated: tok.IsElevated()}
}

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

type Handler func(caller Caller, method string, params json.RawMessage) (any, error)

// Serve accepts connections until ctx is cancelled.
func Serve(ctx context.Context, h Handler) error {
	return serve(ctx, PipeName, h)
}

func serve(ctx context.Context, name string, h Handler) error {
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: pipeSDDL, MessageMode: false})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go serveConn(c, h)
	}
}

func serveConn(c net.Conn, h Handler) {
	defer c.Close()
	caller := callerOf(c)
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	enc := json.NewEncoder(c)
	for sc.Scan() {
		var req Request
		var resp Response
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			resp.Error = "bad request: " + err.Error()
		} else if res, err := h(caller, req.Method, req.Params); err != nil {
			resp.Error = err.Error()
		} else {
			resp.OK = true
			resp.Result, _ = json.Marshal(res)
		}
		if enc.Encode(&resp) != nil {
			return
		}
	}
}

// Call sends one request and decodes the result into out (may be nil).
func Call(method string, params any, out any, timeout time.Duration) error {
	return call(PipeName, method, params, out, timeout)
}

func call(name, method string, params any, out any, timeout time.Duration) error {
	c, err := winio.DialPipe(name, &timeout)
	if err != nil {
		return ErrNoService
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(timeout))
	req := Request{Method: method}
	if params != nil {
		req.Params, _ = json.Marshal(params)
	}
	if err := json.NewEncoder(c).Encode(&req); err != nil {
		return err
	}
	var resp Response
	if err := json.NewDecoder(bufio.NewReader(c)).Decode(&resp); err != nil {
		return err
	}
	if !resp.OK {
		return errors.New(resp.Error)
	}
	if out != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

var ErrNoService = errors.New("The No More DPI service is not running")
