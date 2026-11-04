package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32         = windows.NewLazySystemDLL("user32.dll")
	procMessageBox = user32.NewProc("MessageBoxW")
)

const (
	mbOK       = 0x0
	mbIconInfo = 0x40
	mbIconErr  = 0x10
	mbYesNo    = 0x4
	mbIconQ    = 0x20
	idYes      = 6
)

func messageBox(text string, flags uintptr) int {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("No More DPI")
	r, _, _ := procMessageBox.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags)
	return int(r)
}

// hasConsole reports whether we were started from a terminal (console build).
func hasConsole() bool {
	_, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		return false
	}
	var mode uint32
	h, _ := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	return windows.GetConsoleMode(h, &mode) == nil
}

// childErrPath is where a UAC child leaves its error for the waiting parent.
func childErrPath() string { return filepath.Join(os.TempDir(), "nomoredpi-elevated-error.txt") }

func fail(err error) {
	if launchedByParent() {
		_ = os.WriteFile(childErrPath(), []byte(err.Error()), 0o644)
		os.Exit(1)
	}
	if setupMode() {
		// Setup shows it (or logs it when silent); a box here would block setup.
		_ = os.WriteFile(filepath.Join(os.TempDir(), "nomoredpi-setup-error.txt"), []byte(err.Error()), 0o644)
		os.Exit(1)
	}
	if hasConsole() {
		fmt.Fprintln(os.Stderr, "error:", err)
	} else {
		messageBox(err.Error(), mbOK|mbIconErr)
	}
	os.Exit(1)
}

func info(text string) {
	if hasConsole() {
		fmt.Println(text)
	} else {
		messageBox(text, mbOK|mbIconInfo)
	}
}

func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// elevated runs fn directly when we are admin, otherwise relaunches this command
// through UAC and waits for it.
func elevated(cmd string, fn func() error) error {
	if isAdmin() {
		return fn()
	}
	exe, _ := exePaths()
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	var quoted []string
	for _, a := range append(append([]string{cmd}, os.Args[2:]...), "--child") {
		quoted = append(quoted, syscall.EscapeArg(a))
	}
	args, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	info := &shellExecuteInfo{
		cbSize: uint32(unsafe.Sizeof(shellExecuteInfo{})),
		fMask:  0x40, // SEE_MASK_NOCLOSEPROCESS
		lpVerb: verb, lpFile: file, lpParameters: args, nShow: windows.SW_HIDE,
	}
	os.Remove(childErrPath())
	if r, _, err := procShellExecuteEx.Call(uintptr(unsafe.Pointer(info))); r == 0 {
		return fmt.Errorf("administrator permission was not granted: %v", err)
	}
	defer windows.CloseHandle(info.hProcess)
	_, _ = windows.WaitForSingleObject(info.hProcess, windows.INFINITE)
	var code uint32
	_ = windows.GetExitCodeProcess(info.hProcess, &code)
	if code != 0 {
		if b, err := os.ReadFile(childErrPath()); err == nil {
			return errors.New(string(b))
		}
		return fmt.Errorf("%s failed (exit code %d)", cmd, code)
	}
	return nil
}

var procShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

var unsafeSizeofPE = unsafe.Sizeof(windows.ProcessEntry32{})

type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	lpVerb         *uint16
	lpFile         *uint16
	lpParameters   *uint16
	lpDirectory    *uint16
	nShow          int32
	hInstApp       windows.Handle
	lpIDList       uintptr
	lpClass        *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

// startDetached starts exe without waiting for it. (os.StartProcess without standard
// handles fails with "invalid argument" on Windows; exec.Cmd supplies them.)
func startDetached(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// startAndWait runs exe to completion (e.g. `NoMoreDPI.exe --autostart on`).
func startAndWait(exe string, args ...string) error {
	cmd := exec.Command(exe, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(15 * time.Second):
		return fmt.Errorf("%s did not finish", filepath.Base(exe))
	}
}
