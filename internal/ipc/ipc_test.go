package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The service decides what a caller may change from Caller, so it must name the real client.
func TestCallerIsTheClientProcess(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\nomoredpi-test-%d`, os.Getpid())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, name, func(c Caller, method string, _ json.RawMessage) (any, error) {
			return c, nil
		})
	}()
	var got Caller
	var err error
	for i := 0; i < 50; i++ {
		if err = call(name, "whoami", nil, &got, time.Second); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got.PID != uint32(os.Getpid()) {
		t.Errorf("PID = %d, want %d", got.PID, os.Getpid())
	}
	if want := windows.GetCurrentProcessToken().IsElevated(); got.Elevated != want {
		t.Errorf("Elevated = %v, want %v", got.Elevated, want)
	}
	cancel()
	<-done
}
