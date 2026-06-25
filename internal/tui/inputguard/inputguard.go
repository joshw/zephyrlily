// Package inputguard is a self-contained workaround for a Bubble Tea input-parser
// bug. It wraps a terminal input file so Bubble Tea never receives a read buffer
// that ends in the middle of an ANSI escape sequence.
//
// # The bug
//
// Bubble Tea (through at least v1.3.10) reads stdin into a fixed 256-byte buffer
// and parses it a message at a time in readAnsiInputs/detectOneMsg (key.go). The
// parser knows how to wait for more bytes when it sees a partial plain-text rune
// at the end of the buffer (it returns w==0 so the read loop extends the buffer),
// but its mouse/escape branch has no such guard. So when a fast burst of input
// (e.g. trackpad wheel scrolling, which emits a stream of \x1b[<…M SGR reports)
// overruns the 256-byte buffer and splits a sequence, the leftover bytes are
// emitted as KeyRunes and leak into the UI as stray keystrokes. The same split
// affects multi-byte cursor/alt keys.
//
// # The workaround
//
// Reader interposes between the OS and Bubble Tea: it holds back an incomplete
// trailing escape sequence and prepends it to the next read, so Bubble Tea only
// ever sees whole sequences. It only does this on a full read (when the OS likely
// has more bytes queued); a short read is treated as an event boundary, matching
// Bubble Tea's own assumption and preserving a lone Escape keypress.
//
// The package depends only on the standard library and has no compile-time
// coupling to Bubble Tea — the only knowledge it encodes is the structure of
// ANSI escape sequences. It is wired in at exactly one place, via tea.WithInput
// in cmd/zlily (runTUI).
//
// # Removing this package
//
// This is a temporary shim. If a future Bubble Tea release makes its input
// parser wait for the rest of a split escape sequence (i.e. the mouse/escape
// branch of detectOneMsg returns w==0 at the end of a full buffer, the way the
// plain-rune branch already does), this package becomes dead weight and can be
// deleted wholesale: remove the directory and drop the tea.WithInput(...) line in
// cmd/zlily/main.go's runTUI. Nothing else imports it.
package inputguard

import (
	"bytes"
	"os"
)

// Reader wraps an *os.File terminal input. It embeds the file so it still
// satisfies the term.File / cancelreader.File interfaces Bubble Tea relies on
// (Fd, Name, Write, Close all come from the embedded file), while overriding
// Read to enforce escape-sequence boundaries.
type Reader struct {
	*os.File
	hold    []byte // incomplete trailing escape sequence carried to the next Read
	scratch []byte // reused read buffer
	err     error  // error deferred until held bytes have been flushed
}

// New wraps f, which is typically os.Stdin.
func New(f *os.File) *Reader {
	return &Reader{File: f}
}

// Read fills p with input, never returning a buffer that ends mid-sequence on a
// full read.
func (r *Reader) Read(p []byte) (int, error) {
	return r.read(p, r.File.Read)
}

// read is the testable core of Read, parameterised over the underlying read
// function.
func (r *Reader) read(p []byte, readFn func([]byte) (int, error)) (int, error) {
	if r.err != nil && len(r.hold) == 0 {
		err := r.err
		r.err = nil
		return 0, err
	}

	room := len(p) - len(r.hold)
	if room <= 0 {
		// The held partial is as large as the caller's buffer (pathological, e.g.
		// a single sequence longer than 256 bytes). Flush what we can rather than
		// stall; sequence-boundary correctness is best-effort in this case.
		n := copy(p, r.hold)
		r.hold = r.hold[n:]
		return n, nil
	}

	if cap(r.scratch) < room {
		r.scratch = make([]byte, room)
	}
	n, err := readFn(r.scratch[:room])

	// Combine any held partial with the fresh bytes. The three-index slice forces
	// a fresh backing array so the append can't clobber hold or scratch.
	buf := append(r.hold[:len(r.hold):len(r.hold)], r.scratch[:n]...)
	r.hold = nil

	// A full read means we asked for room bytes and got them, so the OS probably
	// has more data queued and this read may have split a trailing sequence. On a
	// short read we're at an event boundary, so flush everything (this is also
	// what preserves a lone Escape keypress, which arrives as a short read).
	if err == nil && n == room {
		if cut := incompleteTailStart(buf); cut >= 0 {
			r.hold = append([]byte(nil), buf[cut:]...)
			buf = buf[:cut]
		}
	}

	// Bubble Tea's reader discards bytes when Read returns an error, so don't
	// surface the error until the buffered bytes have been delivered.
	if err != nil && len(buf) > 0 {
		r.err = err
		err = nil
	}

	return copy(p, buf), err
}

// incompleteTailStart reports the index at which an unterminated escape sequence
// begins at the tail of buf, or -1 if buf ends on a sequence boundary. It
// recognises a lone trailing ESC, CSI (ESC [ … final), and SS3 (ESC O final).
// Other ESC-introduced forms (alt+key, OSC/DCS string sequences) are treated as
// already complete, so they are never held back and can't stall input.
func incompleteTailStart(buf []byte) int {
	e := bytes.LastIndexByte(buf, 0x1b)
	if e < 0 {
		return -1
	}
	rest := buf[e:]
	if len(rest) == 1 {
		return e // lone trailing ESC; a continuation is expected on a full read
	}

	switch rest[1] {
	case '[': // CSI: ESC [ <parameter/intermediate bytes>* <final 0x40-0x7e>
		for i := 2; i < len(rest); i++ {
			switch c := rest[i]; {
			case c >= 0x20 && c <= 0x3f:
				// parameter (0x30-0x3f) or intermediate (0x20-0x2f) byte; keep going
			case c >= 0x40 && c <= 0x7e:
				return -1 // final byte present: the sequence is complete
			default:
				return -1 // malformed; don't hold it indefinitely
			}
		}
		return e // ran off the end still inside the CSI: incomplete
	case 'O': // SS3: ESC O <final>
		if len(rest) < 3 {
			return e
		}
		return -1
	default:
		return -1 // alt+key, string sequence, etc.: treat as complete
	}
}
