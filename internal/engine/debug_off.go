//go:build !debug

package engine

// debugHandle: release builds have no developer-only requests (see debug_on.go).
func debugHandle(string) (any, bool) { return nil, false }
