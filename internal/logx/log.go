// Package logx is a small size-capped file logger that also keeps the last lines
// in memory for the settings window.
package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

const (
	maxSize  = 4 << 20
	ringSize = 300
)

var (
	mu   sync.Mutex
	file *os.File
	path string
	size int64
	ring []string
	echo io.Writer
)

// Open starts logging to p. If console is true lines are echoed to stderr too.
func Open(p string, console bool) error {
	mu.Lock()
	defer mu.Unlock()
	path = p
	if console {
		echo = os.Stderr
	}
	return reopen()
}

func reopen() error {
	if file != nil {
		file.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	st, _ := f.Stat()
	file, size = f, st.Size()
	return nil
}

func Printf(format string, args ...any) {
	line := time.Now().Format("2006-01-02 15:04:05.000 ") + fmt.Sprintf(format, args...)
	mu.Lock()
	defer mu.Unlock()
	ring = append(ring, line)
	if len(ring) > ringSize {
		ring = ring[len(ring)-ringSize:]
	}
	if echo != nil {
		fmt.Fprintln(echo, line)
	}
	if file == nil {
		return
	}
	n, _ := fmt.Fprintln(file, line)
	size += int64(n)
	if size > maxSize {
		file.Close()
		file = nil
		_ = os.Rename(path, path+".1")
		_ = reopen()
	}
}

// Tail returns the most recent lines, oldest first.
func Tail(n int) []string {
	mu.Lock()
	defer mu.Unlock()
	if n > len(ring) {
		n = len(ring)
	}
	return append([]string(nil), ring[len(ring)-n:]...)
}

// Writer adapts logx to an io.Writer with a prefix, for child process output.
type Writer struct {
	Prefix string
	buf    []byte
}

func (w *Writer) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := indexNL(w.buf)
		if i < 0 {
			break
		}
		if line := trimCR(w.buf[:i]); len(line) > 0 {
			Printf("%s%s", w.Prefix, line)
		}
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > 4096 {
		Printf("%s%s", w.Prefix, w.buf)
		w.buf = nil
	}
	return len(p), nil
}

func indexNL(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

func trimCR(b []byte) []byte {
	if n := len(b); n > 0 && b[n-1] == '\r' {
		return b[:n-1]
	}
	return b
}
