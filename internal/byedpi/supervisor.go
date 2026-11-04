// Package byedpi runs one ciadpi process per profile and restarts it if it dies.
package byedpi

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"nomoredpi/internal/logx"
)

// Instance is a supervised ciadpi.
type Instance struct {
	ID   string
	Port int
	Args string

	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	running  bool
	restarts int
	lastErr  string
	pid      int
}

type Status struct {
	ID       string `json:"id"`
	Port     int    `json:"port"`
	Running  bool   `json:"running"`
	Restarts int    `json:"restarts"`
	PID      int    `json:"pid"`
	LastErr  string `json:"lastErr,omitempty"`
}

func (in *Instance) Status() Status {
	in.mu.Lock()
	defer in.mu.Unlock()
	return Status{ID: in.ID, Port: in.Port, Running: in.running, Restarts: in.restarts, PID: in.pid, LastErr: in.lastErr}
}

// Supervisor keeps ciadpi processes in a kill-on-close job, so they never outlive us.
type Supervisor struct {
	exe string
	job windows.Handle

	mu   sync.Mutex
	inst map[string]*Instance
}

func NewSupervisor(exe string) (*Supervisor, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	return &Supervisor{exe: exe, job: job, inst: map[string]*Instance{}}, nil
}

// Want is the desired set of profiles.
type Want struct {
	ID   string
	Port int
	Args string
}

// Apply starts, restarts or stops instances so that exactly want is running.
func (s *Supervisor) Apply(want []Want) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]bool{}
	for _, w := range want {
		keep[w.ID] = true
		if in, ok := s.inst[w.ID]; ok {
			if in.Port == w.Port && in.Args == w.Args {
				continue
			}
			s.stop(in)
		}
		in := &Instance{ID: w.ID, Port: w.Port, Args: w.Args}
		s.inst[w.ID] = in
		s.start(in)
	}
	for id, in := range s.inst {
		if !keep[id] {
			s.stop(in)
			delete(s.inst, id)
		}
	}
}

func (s *Supervisor) StopAll() { s.Apply(nil) }

func (s *Supervisor) Statuses() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.inst))
	for _, in := range s.inst {
		out = append(out, in.Status())
	}
	return out
}

// Port returns the port of a running profile, 0 if it is not up.
func (s *Supervisor) Port(id string) int {
	s.mu.Lock()
	in := s.inst[id]
	s.mu.Unlock()
	if in == nil {
		return 0
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.running {
		return 0
	}
	return in.Port
}

func (s *Supervisor) stop(in *Instance) {
	in.cancel()
	<-in.done
}

func (s *Supervisor) start(in *Instance) {
	ctx, cancel := context.WithCancel(context.Background())
	in.cancel, in.done = cancel, make(chan struct{})
	go func() {
		defer close(in.done)
		backoff := 500 * time.Millisecond
		for {
			began := time.Now()
			err := s.runOnce(ctx, in)
			if ctx.Err() != nil {
				return
			}
			in.mu.Lock()
			in.running, in.pid = false, 0
			in.restarts++
			if err != nil {
				in.lastErr = err.Error()
			}
			in.mu.Unlock()
			logx.Printf("ciadpi[%s] exited: %v; restarting", in.ID, err)
			if time.Since(began) > 30*time.Second {
				backoff = 500 * time.Millisecond
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 10*time.Second)
		}
	}()
}

// Args builds the ciadpi command line for a profile.
func Args(port int, desync string) []string {
	return append([]string{"-i", "127.0.0.1", "-p", strconv.Itoa(port), "-I", "0.0.0.0"},
		strings.Fields(desync)...)
}

func (s *Supervisor) runOnce(ctx context.Context, in *Instance) error {
	cmd := exec.Command(s.exe, Args(in.Port, in.Args)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	cmd.Stdout = &logx.Writer{Prefix: "ciadpi[" + in.ID + "] "}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	if h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
		_ = windows.AssignProcessToJobObject(s.job, h)
		windows.CloseHandle(h)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	if err := waitPort(ctx, in.Port, exited); err != nil {
		_ = cmd.Process.Kill()
		<-exited
		return err
	}
	in.mu.Lock()
	in.running, in.pid, in.lastErr = true, cmd.Process.Pid, ""
	in.mu.Unlock()
	logx.Printf("ciadpi[%s] up on 127.0.0.1:%d (%s)", in.ID, in.Port, in.Args)

	select {
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-exited
		in.mu.Lock()
		in.running, in.pid = false, 0
		in.mu.Unlock()
		return nil
	case err := <-exited:
		if err == nil {
			err = fmt.Errorf("exited")
		}
		return err
	}
}

func waitPort(ctx context.Context, port int, exited chan error) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			exited <- err
			return fmt.Errorf("ciadpi did not start (the arguments may be invalid): %v", err)
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("port %d did not open", port)
}

// Close stops everything and closes the job (killing any stragglers).
func (s *Supervisor) Close() {
	s.StopAll()
	windows.CloseHandle(s.job)
}
