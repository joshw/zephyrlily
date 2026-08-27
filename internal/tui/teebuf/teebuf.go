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
	"time"
)

// DefaultTail is the retained tail size. Big enough for many full frames at
// typical terminal sizes; small enough to be an unremarkable memory cost.
const DefaultTail = 256 * 1024

// maxWriteRecords bounds the retained write pattern. Each record is a few
// bytes, and a session that exceeds this has long since wrapped the byte ring
// too.
const maxWriteRecords = 8192

// WriteRecord is one Write call: how far into the session it happened and how
// many bytes it carried.
//
// The boundaries matter as much as the bytes. The renderer emits one write per
// frame, and a replay that re-slices the same bytes at different offsets is a
// different stimulus to anything downstream that syncs per frame — mosh's
// state-sync protocol above all, which coalesces by write. A replay chunked at
// a fixed size delivers every byte faithfully and still fails to reproduce a
// frame-boundary-sensitive fault, which is exactly what happened before this
// was recorded.
type WriteRecord struct {
	At time.Duration // since the first write
	N  int           // bytes in this write
}

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

	mu     sync.Mutex
	ring   []byte // fixed capacity buffer
	pos    int    // next write position in ring
	full   bool   // ring has wrapped at least once
	writes uint64 // total Write calls forwarded
	bytes  uint64 // total bytes forwarded

	start   time.Time     // first write, the origin for WriteRecord.At
	records []WriteRecord // write pattern, oldest first, capped
	dropped int           // records discarded once the cap was reached
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
	w.writes++
	w.bytes += uint64(len(p))
	now := time.Now()
	if w.start.IsZero() {
		w.start = now
	}
	if len(w.records) >= maxWriteRecords {
		// Drop the oldest, matching the byte ring: what survives is the tail.
		copy(w.records, w.records[1:])
		w.records = w.records[:len(w.records)-1]
		w.dropped++
	}
	w.records = append(w.records, WriteRecord{At: now.Sub(w.start), N: len(p)})
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

// Written reports how many writes, and how many bytes in total, have been
// forwarded to the terminal. The TUI's responsiveness metrics sample it: a
// session whose per-frame output has grown is one whose keystrokes cost more
// to echo, which matters most over a slow link (see internal/tui/ui/perf.go).
func (w *Writer) Written() (writes, bytes uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writes, w.bytes
}

// Writes returns the retained write pattern, oldest first, and how many
// records were dropped to stay within the cap.
//
// A replay that honours these reproduces the renderer's own frame boundaries
// and pacing; one that does not is delivering the same bytes as a different
// stimulus.
func (w *Writer) Writes() ([]WriteRecord, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]WriteRecord(nil), w.records...), w.dropped
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
