//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !aix && !zos
// +build !windows,!darwin,!dragonfly,!freebsd,!linux,!netbsd,!openbsd,!solaris,!aix,!zos

package tea

// This file covers platforms with no tty of their own — js/wasm above all,
// where the program is driven by a host (a browser terminal emulator, say)
// that hands it bytes rather than a file descriptor.
//
// initInput deliberately leaves p.ttyInput and p.ttyOutput nil. That is the
// documented "piped I/O" path the rest of the package already handles: the
// same one teatest drives. Note it also selects mapNl, so the renderer emits
// bare \n and expects the host to supply the carriage return, exactly as a
// tty's ONLCR would.

func (p *Program) initInput() error { return nil }

const suspendSupported = false

// suspendProcess is a no-op: there is no job control to suspend into.
func suspendProcess() {}
