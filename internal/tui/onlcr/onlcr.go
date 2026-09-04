// Package onlcr supplies the newline translation a terminal driver normally
// performs, for hosts that don't.
//
// Bubble Tea decides whether to map newlines from whether it holds a tty input
// handle: with piped I/O it sets mapNl and emits a bare \n at the end of every
// rendered line, expecting the tty's ONLCR mode to add the carriage return.
// That is the path a js/wasm build takes, since it has no tty at all (see
// initInput in the vendored bubbletea's tty_other.go).
//
// Browser terminal emulators do not do ONLCR — xterm.js treats \n strictly as
// a line feed, moving down a row while keeping the column — so the renderer's
// output arrives as a staircase unless something restores the carriage
// returns. The test suite already compensates for exactly this when it replays
// renderer output through a VT emulator; see replayBottomRows in
// internal/tui/ui/wrapresurrect_test.go.
package onlcr

import "io"

// Writer wraps w, rewriting bare \n as \r\n. A \n already preceded by \r is
// passed through untouched, including when the pair is split across two
// writes.
type Writer struct {
	w       io.Writer
	afterCR bool
}

// NewWriter returns a Writer translating newlines on the way to w.
func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// Write implements io.Writer. It always reports len(p) written on success:
// callers care about their own bytes being accepted, not about the carriage
// returns added underneath.
func (t *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	// Fast path: nothing to insert.
	if !needsTranslation(p, t.afterCR) {
		n, err := t.w.Write(p)
		if n > 0 {
			t.afterCR = p[n-1] == '\r'
		}
		if err != nil {
			return n, err
		}
		return len(p), nil
	}

	out := make([]byte, 0, len(p)+8)
	prevCR := t.afterCR
	for _, b := range p {
		if b == '\n' && !prevCR {
			out = append(out, '\r')
		}
		out = append(out, b)
		prevCR = b == '\r'
	}
	if _, err := t.w.Write(out); err != nil {
		return 0, err
	}
	t.afterCR = prevCR
	return len(p), nil
}

// needsTranslation reports whether p contains a \n that is not already
// preceded by a \r.
func needsTranslation(p []byte, afterCR bool) bool {
	prevCR := afterCR
	for _, b := range p {
		if b == '\n' && !prevCR {
			return true
		}
		prevCR = b == '\r'
	}
	return false
}
