// Package teebuf interposes on the terminal output stream, retaining a tail
// of everything written so a %debug snapshot can include the exact bytes the
// renderer sent to the terminal. Display bugs are divergences between what
// the app rendered and what the terminal did with it; this tail lets a bug
// report be replayed byte-for-byte through a terminal emulator (see
// TestReplaySnapshot in internal/tui/ui).
package teebuf

import (
	"os"
	"sync"
)

// DefaultTail is the retained tail size. Big enough for many full frames at
// typical terminal sizes; small enough to be an unremarkable memory cost.
const DefaultTail = 256 * 1024

// Writer wraps a terminal output file, forwarding writes unchanged while
// keeping the most recent Write bytes in a fixed-size ring.
//
// The embedded *os.File deliberately supplies Read, Close, and — critically —
// Fd: bubbletea only treats its output as a real terminal when the writer
// satisfies term.File (io.ReadWriteCloser + Fd) and the fd is a TTY. Losing
// that detection would silently flip the renderer into its non-tty mode
// (cooked-output newline mapping, no raw input), so the wrapper must stay a
// file in bubbletea's eyes.
type Writer struct {
	*os.File

	mu   sync.Mutex
	ring []byte // fixed capacity buffer
	pos  int    // next write position in ring
	full bool   // ring has wrapped at least once
}

// New wraps f (typically os.Stdout) retaining a DefaultTail-sized tail.
func New(f *os.File) *Writer {
	return &Writer{File: f, ring: make([]byte, DefaultTail)}
}

// Write forwards to the underlying file and records the written prefix in
// the ring. The return values are the file's, so short writes and errors
// propagate exactly as without the tee.
func (w *Writer) Write(p []byte) (int, error) {
	n, err := w.File.Write(p)
	if n > 0 {
		w.record(p[:n])
	}
	return n, err
}

func (w *Writer) record(p []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	// Only the last len(ring) bytes of p can survive anyway.
	if len(p) > len(w.ring) {
		p = p[len(p)-len(w.ring):]
		w.pos = 0
		w.full = true
	}
	n := copy(w.ring[w.pos:], p)
	if n < len(p) {
		copy(w.ring, p[n:])
		w.full = true
	}
	w.pos = (w.pos + len(p)) % len(w.ring)
	if w.pos == 0 && len(p) > 0 {
		w.full = true
	}
}

// Tail returns a copy of the retained output tail, oldest bytes first.
func (w *Writer) Tail() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.full {
		return append([]byte(nil), w.ring[:w.pos]...)
	}
	out := make([]byte, 0, len(w.ring))
	out = append(out, w.ring[w.pos:]...)
	out = append(out, w.ring[:w.pos]...)
	return out
}
