//go:build debug

package engine

import "runtime"

// debugHandle answers developer-only requests. It exists only in builds made with
// `-tags debug` (build.ps1 -Dev).
func debugHandle(method string) (any, bool) {
	switch method {
	case "stacks":
		buf := make([]byte, 1<<20)
		return string(buf[:runtime.Stack(buf, true)]), true
	}
	return nil, false
}
