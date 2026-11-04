// nomoredpi-svc is the service and its admin tool (the tray and window are NoMoreDPI.exe, ui\):
//
//	nomoredpi-svc                    open NoMoreDPI.exe next to it
//	nomoredpi-svc service            run by the Windows service manager
//	nomoredpi-svc run                run the engine in the foreground (admin, for debugging)
//	nomoredpi-svc install [--setup]  install/upgrade the service (asks for admin)
//	nomoredpi-svc uninstall [--setup] remove the service and put DNS back (asks for admin)
//	nomoredpi-svc prepare-upgrade    stop the service and tray so setup can replace files
//	nomoredpi-svc set-startup automatic|manual|disabled
//	nomoredpi-svc set-dns --lock on|off --fake-range <cidr> --upstream <url>...   (asks for admin)
//	nomoredpi-svc restore            put system DNS back if a crash left it pointing at us
//	nomoredpi-svc status|logs|pause|resume|autofind <id>   talk to the running service
//
// --setup is how the installer calls us: no message boxes, no tray launch, no file copy or
// delete (setup owns the files). Builds made with -tags debug also have `stacks`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"

	"nomoredpi/internal/browserdns"
	"nomoredpi/internal/config"
	"nomoredpi/internal/engine"
	"nomoredpi/internal/ipc"
	"nomoredpi/internal/logx"
	"nomoredpi/internal/sysdns"
)

const serviceName = "NoMoreDPI"

const usage = `usage: nomoredpi-svc <command>

  install | uninstall | prepare-upgrade | set-startup automatic|manual|disabled
  set-dns --lock on|off --fake-range <cidr> --upstream <sdns://... or https://...> [--upstream ...]
  status | logs | pause | resume | autofind <profile-id> | restore | run`

func main() {
	// A panic in a GUI-subsystem process is invisible; report it like any other error.
	defer func() {
		if r := recover(); r != nil {
			fail(fmt.Errorf("internal error: %v", r))
		}
	}()
	cmd := ""
	if len(os.Args) > 1 {
		cmd = strings.TrimLeft(strings.ToLower(os.Args[1]), "-/")
	}
	var err error
	switch cmd {
	case "":
		if _, dir := exePaths(); fileExists(filepath.Join(dir, uiExe)) {
			err = startDetached(filepath.Join(dir, uiExe))
			break
		}
		info(usage)
	case "help", "h", "?":
		info(usage)
	case "service":
		err = runService()
	case "run":
		err = runForeground()
	case "install":
		wasAdmin := isAdmin()
		err = elevated(cmd, install)
		if err == nil && !wasAdmin {
			// We are the unelevated parent of the UAC child: start the tray as the user,
			// with the window open so the result is visible.
			ui := filepath.Join(installDir(), uiExe)
			_ = startAndWait(ui, "--autostart", "on")
			err = startDetached(ui)
		}
	case "uninstall":
		wasAdmin := isAdmin()
		err = elevated(cmd, uninstall)
		if err == nil && !wasAdmin {
			msg := "No More DPI was removed."
			// The elevated child runs as the same user, so it shares our temp dir.
			if b, rerr := os.ReadFile(filepath.Join(os.TempDir(), "nomoredpi-uninstall.txt")); rerr == nil {
				msg = string(b)
			}
			info(msg)
		}
	case "prepare-upgrade":
		err = elevated(cmd, prepareUpgrade)
	case "set-startup":
		if len(os.Args) < 3 {
			err = fmt.Errorf("usage: nomoredpi-svc set-startup automatic|manual|disabled")
			break
		}
		err = elevated(cmd, func() error { return setStartup(os.Args[2]) })
	case "set-dns":
		var s engine.SystemSettings
		if s, err = parseSetDNS(os.Args[2:]); err != nil {
			break
		}
		// The service accepts this only from an elevated caller.
		err = elevated(cmd, func() error { return ipc.Call("setSystem", s, nil, 60*time.Second) })
	case "autofind":
		if len(os.Args) < 3 {
			err = fmt.Errorf("usage: nomoredpi-svc autofind <profile-id>")
			break
		}
		var res engine.FindResult
		if err = ipc.Call("autoFind", os.Args[2], &res, 5*time.Minute); err == nil {
			b, _ := json.MarshalIndent(res, "", "  ")
			fmt.Println(string(b))
		}
	case "pause", "resume":
		err = ipc.Call("setActive", cmd == "resume", nil, 60*time.Second)
	case "status", "logs":
		err = printCall(cmd)
	case "restore":
		err = elevated(cmd, func() error {
			restoreLeftovers()
			return nil
		})
	default:
		if debugCommands[cmd] {
			err = printCall(cmd)
			break
		}
		err = fmt.Errorf("unknown command: %s\n\n%s", cmd, usage)
	}
	if err != nil {
		fail(err)
	}
}

// printCall sends a request without parameters and prints the answer.
func printCall(method string) error {
	var out any
	if err := ipc.Call(method, nil, &out, 10*time.Second); err != nil {
		return err
	}
	if s, ok := out.(string); ok {
		fmt.Println(s)
	} else {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
	}
	return nil
}

// parseSetDNS reads `set-dns --lock on|off --fake-range <cidr> --upstream <url>...`.
// All three settings are required so that a partial command cannot reset the others.
func parseSetDNS(args []string) (engine.SystemSettings, error) {
	var s engine.SystemSettings
	var haveLock bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--child" || a == "--setup" {
			continue
		}
		if i+1 >= len(args) {
			return s, fmt.Errorf("set-dns: %s needs a value", a)
		}
		v := args[i+1]
		i++
		switch a {
		case "--lock":
			switch strings.ToLower(v) {
			case "on":
				s.LockDNS = true
			case "off":
				s.LockDNS = false
			default:
				return s, fmt.Errorf("set-dns: --lock must be on or off, not %q", v)
			}
			haveLock = true
		case "--fake-range":
			s.FakeRange = v
		case "--upstream":
			s.Upstreams = append(s.Upstreams, v)
		default:
			return s, fmt.Errorf("set-dns: unknown option %s", a)
		}
	}
	if !haveLock || s.FakeRange == "" || len(s.Upstreams) == 0 {
		return s, fmt.Errorf("usage: nomoredpi-svc set-dns --lock on|off --fake-range <cidr> --upstream <url> [--upstream <url> ...]")
	}
	return s, nil
}

// restoreLeftovers puts back browser policies and adapter DNS that a crashed run left set.
func restoreLeftovers() {
	browserdns.Restore(filepath.Join(config.Dir(), "browser-state.json"))
	if err := sysdns.RestoreLeftover(filepath.Join(config.Dir(), "dns-state.json")); err != nil {
		logx.Printf("restore: %v", err)
	}
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func exePaths() (exe, dir string) {
	exe, _ = os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	return exe, filepath.Dir(exe)
}

// serve runs the engine and the IPC endpoint until ctx ends.
func serve(ctx context.Context, console bool) error {
	// Reset the data folder's permissions on every start: this also covers installs made
	// before install locked it down.
	secureErr := config.SecureDir()
	if err := logx.Open(filepath.Join(config.Dir(), "service.log"), console); err != nil {
		return err
	}
	if secureErr != nil {
		logx.Printf("securing %s: %v", config.Dir(), secureErr)
	}
	exe, dir := exePaths()
	logx.Printf("No More DPI %s starting (%s)", engine.AppVersion, exe)
	e, err := engine.New(dir, exe)
	if err != nil {
		logx.Printf("start: %v", err)
		return err
	}
	ipcErr := make(chan error, 1)
	go func() { ipcErr <- ipc.Serve(ctx, e.Handle) }()
	done := make(chan struct{})
	go func() {
		e.Run(ctx)
		close(done)
	}()
	select {
	case err := <-ipcErr:
		if err != nil {
			logx.Printf("ipc: %v", err)
		}
		<-done
	case <-done:
	}
	logx.Printf("stopped")
	return nil
}

func runForeground() error {
	if !isAdmin() {
		return fmt.Errorf("`nomoredpi-svc run` must be run as administrator")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return serve(ctx, true)
}

type service struct{}

func (service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = serve(ctx, false)
		close(done)
	}()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 20000}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
					// System DNS is restored first thing; the adapter and WFP filters
					// go away with the process, so exiting now is safe.
					logx.Printf("stop timed out; exiting anyway")
				}
				return false, 0
			}
		case <-done:
			// The engine stopped on its own (startup error): exit non-zero so the
			// service manager's recovery actions restart us.
			cancel()
			return false, 1
		}
	}
}

func runService() error {
	return svc.Run(serviceName, service{})
}
