//go:build debug

package main

// debugCommands are forwarded to the service as-is; they exist only in -tags debug builds.
var debugCommands = map[string]bool{"stacks": true}
