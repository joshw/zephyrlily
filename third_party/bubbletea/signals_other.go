//go:build !windows && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !aix && !zos
// +build !windows,!darwin,!dragonfly,!freebsd,!linux,!netbsd,!openbsd,!solaris,!aix,!zos

package tea

// listenForResize does nothing on platforms with no SIGWINCH. A host driving
// the program over piped I/O cannot be polled for its size either, so it is
// the host's job to report geometry by sending a WindowSizeMsg through
// Program.Send whenever it changes.
func (p *Program) listenForResize(done chan struct{}) {
	defer close(done)
	<-p.ctx.Done()
}
