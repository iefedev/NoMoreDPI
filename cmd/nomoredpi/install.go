package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"nomoredpi/internal/config"
	"nomoredpi/internal/logx"
)

const (
	svcExe = "nomoredpi-svc.exe"
	// uiExe is the tray icon and settings window (ui\, WPF).
	uiExe = "NoMoreDPI.exe"
)

var payload = []string{svcExe, uiExe, "ciadpi.exe", "wintun.dll"}

const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "No More DPI"
)

// serviceSDDL is the default service DACL plus start/stop/pause for interactive users, so
// the window can start and stop the service without UAC. Changing its configuration
// (startup type, binary) stays with administrators.
const serviceSDDL = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWRPWPDTLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"

// installDir is where a manual `nomoredpi-svc install` (without --setup) copies the files.
// Setup installs wherever the user chose instead.
func installDir() string {
	pf, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		pf = os.Getenv("ProgramFiles")
	}
	return filepath.Join(pf, "No More DPI")
}

// setupMode reports whether the installer is calling us (see main.go).
func setupMode() bool { return hasFlag("--setup") }

func hasFlag(f string) bool {
	for _, a := range os.Args[2:] {
		if a == f {
			return true
		}
	}
	return false
}

// launchedByParent reports whether elevated() started us through UAC.
func launchedByParent() bool { return hasFlag("--child") }

// quiet: someone else reports the result (setup, or the unelevated parent).
func quiet() bool { return setupMode() || launchedByParent() }

func install() error {
	_ = logx.Open(filepath.Join(os.TempDir(), "nomoredpi-install.log"), false)
	logx.Printf("install: args=%q", os.Args)
	err := doInstall()
	if err != nil {
		logx.Printf("install failed: %v", err)
	}
	return err
}

func doInstall() error {
	_, src := exePaths()
	dst := installDir()
	if setupMode() {
		dst = src // setup put the files where the user chose
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	// Upgrade: stop the running service and tray so files can be replaced.
	if s, err := m.OpenService(serviceName); err == nil {
		stopService(s)
		s.Close()
	}
	killUI()
	logx.Printf("install: service and tray stopped")

	if err := config.SecureDir(); err != nil {
		return fmt.Errorf("could not secure %s: %w", config.Dir(), err)
	}

	if !strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		for _, f := range payload {
			if err := copyFile(filepath.Join(src, f), filepath.Join(dst, f)); err != nil {
				return fmt.Errorf("could not copy %s: %w", f, err)
			}
		}
	}
	exe := filepath.Join(dst, svcExe)

	s, err := m.OpenService(serviceName)
	if err != nil {
		s, err = m.CreateService(serviceName, exe, mgr.Config{
			DisplayName:  "No More DPI",
			Description:  "System-wide DPI bypass (byedpi) for the domains you choose.",
			StartType:    mgr.StartAutomatic,
			ErrorControl: mgr.ErrorNormal,
		}, "service")
		if err != nil {
			return fmt.Errorf("could not create the service: %w", err)
		}
	} else {
		cfg, _ := s.Config()
		cfg.BinaryPathName = `"` + exe + `" service`
		// An upgrade keeps a startup type the user chose, except Disabled: installing
		// means they want it running.
		if cfg.StartType == mgr.StartDisabled {
			cfg.StartType = mgr.StartAutomatic
		}
		if err := s.UpdateConfig(cfg); err != nil {
			return err
		}
	}
	defer s.Close()
	if err := setServiceDACL(s); err != nil {
		logx.Printf("install: service permissions: %v", err)
	}
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	}, 86400)
	_ = s.SetRecoveryActionsOnNonCrashFailures(true)
	if err := s.Start(); err != nil {
		return fmt.Errorf("could not start the service: %w", err)
	}
	if err := waitState(s, svc.Running, 10*time.Second); err != nil {
		return err
	}
	logx.Printf("install: service running")
	if quiet() {
		return nil
	}
	// Started elevated directly: the tray runs elevated too, which works (the pipe
	// admits administrators); the user's own tray starts at next sign-in.
	ui := filepath.Join(dst, uiExe)
	_ = startAndWait(ui, "--autostart", "on")
	_ = startDetached(ui)
	info("No More DPI is installed and running.\nIts icon is in the notification area, bottom right.")
	return nil
}

func setServiceDACL(s *mgr.Service) error {
	sd, err := windows.SecurityDescriptorFromString(serviceSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(s.Handle, windows.SE_SERVICE, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

func uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	killUI()
	if s, err := m.OpenService(serviceName); err == nil {
		stopService(s) // the service restores system DNS on stop
		if err := s.Delete(); err != nil {
			s.Close()
			return err
		}
		s.Close()
	}
	// A service that had crashed (or was disabled while a crash left state behind) never
	// got to restore anything.
	restoreLeftovers()
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(runValue)
		k.Close()
	}

	if !setupMode() {
		// We may be running from the install dir: delete it after we exit.
		dir := installDir()
		sysDir, err := windows.GetSystemDirectory()
		if err != nil {
			return err
		}
		cmd := exec.Command(filepath.Join(sysDir, "cmd.exe"))
		// Raw command line: Go's argument quoting (\") is not what cmd.exe understands.
		cmd.SysProcAttr = &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: windows.CREATE_NO_WINDOW,
			CmdLine:       `cmd.exe /c ping -n 3 127.0.0.1 >nul & rmdir /s /q "` + dir + `"`,
		}
		_ = cmd.Start()
	}

	msg := "No More DPI was removed and your DNS settings were put back."
	switch {
	case setupMode():
	case launchedByParent():
		// The parent shows the message; a box here would keep the UAC child alive.
		_ = os.WriteFile(filepath.Join(os.TempDir(), "nomoredpi-uninstall.txt"), []byte(msg), 0o644)
	default:
		info(msg)
	}
	return nil
}

// prepareUpgrade frees the installed files for setup.
func prepareUpgrade() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(serviceName); err == nil {
		stopService(s)
		s.Close()
	}
	killUI()
	return nil
}

func setStartup(kind string) error {
	start := map[string]uint32{
		"automatic": mgr.StartAutomatic, "auto": mgr.StartAutomatic,
		"manual": mgr.StartManual, "disabled": mgr.StartDisabled,
	}
	t, ok := start[strings.ToLower(kind)]
	if !ok {
		return fmt.Errorf("unknown startup type %q (automatic, manual or disabled)", kind)
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("the No More DPI service is not installed")
	}
	defer s.Close()
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	cfg.StartType = t
	cfg.DelayedAutoStart = false
	return s.UpdateConfig(cfg)
}

func stopService(s *mgr.Service) {
	st, err := s.Query()
	if err != nil || st.State == svc.Stopped {
		return
	}
	_, _ = s.Control(svc.Stop)
	if waitState(s, svc.Stopped, 25*time.Second) == nil {
		return
	}
	// A wedged process would keep our files locked: end it.
	if st, err := s.Query(); err == nil && st.ProcessId != 0 {
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, st.ProcessId); err == nil {
			_ = windows.TerminateProcess(h, 1)
			windows.CloseHandle(h)
		}
		_ = waitState(s, svc.Stopped, 10*time.Second)
	}
}

func waitState(s *mgr.Service, want svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.State == want {
			return nil
		}
		if want == svc.Running && st.State == svc.Stopped {
			return errors.New("the service stopped while starting; details: " + filepath.Join(config.Dir(), "service.log"))
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the service did not reach state %v", want)
}

func killUI() { killProcesses(uiExe) }

// killProcesses ends user-session processes with these names, but never ourselves or our
// parent (the unelevated process that launched us through UAC and waits to start the tray).
func killProcesses(names ...string) {
	self := uint32(os.Getpid())
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafeSizeofPE)
	var procs []windows.ProcessEntry32
	parent := uint32(0)
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if pe.ProcessID == self {
			parent = pe.ParentProcessID
		}
		procs = append(procs, pe)
	}
	for _, pe := range procs {
		name := windows.UTF16ToString(pe.ExeFile[:])
		match := false
		for _, n := range names {
			match = match || strings.EqualFold(name, n)
		}
		if !match || pe.ProcessID == self || pe.ProcessID == parent || isServiceProcess(pe.ProcessID) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pe.ProcessID)
		if err != nil {
			continue
		}
		logx.Printf("ending %s pid %d", name, pe.ProcessID)
		_ = windows.TerminateProcess(h, 0)
		_, _ = windows.WaitForSingleObject(h, 3000)
		windows.CloseHandle(h)
	}
}

func isServiceProcess(pid uint32) bool {
	var sid uint32
	return windows.ProcessIdToSessionId(pid, &sid) == nil && sid == 0
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	// A process that just stopped can keep its image locked for a moment.
	for i := 0; ; i++ {
		err := os.Rename(tmp, dst)
		if err == nil || i == 50 {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
