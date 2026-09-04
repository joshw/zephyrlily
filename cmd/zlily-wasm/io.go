//go:build js && wasm

package main

import (
	"io"
	"sync"
	"syscall/js"

	"github.com/joshw/zephyrlily/internal/tui/onlcr"
)

// input is the program's stdin: a buffer JS pushes keystrokes into and the
// Bubble Tea input reader pulls from.
//
// It cannot be an io.Pipe. A pipe write blocks until a reader takes the bytes,
// and Push runs inside a js.Func callback — blocking there stalls the browser's
// event loop, including the very goroutine that would drain it. Buffering makes
// Push non-blocking; only Read waits, on a Go goroutine, which the wasm
// scheduler is happy to park.
type input struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newInput() *input {
	in := &input{}
	in.cond = sync.NewCond(&in.mu)
	return in
}

// Push appends host bytes and wakes any waiting reader. It never blocks.
func (in *input) Push(b []byte) {
	if len(b) == 0 {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed {
		return
	}
	in.buf = append(in.buf, b...)
	in.cond.Signal()
}

// Close unblocks the reader with io.EOF.
func (in *input) Close() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.closed = true
	in.cond.Broadcast()
}

// Read implements io.Reader, blocking until bytes arrive or the input closes.
func (in *input) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	for len(in.buf) == 0 {
		if in.closed {
			return 0, io.EOF
		}
		in.cond.Wait()
	}
	n := copy(p, in.buf)
	in.buf = in.buf[n:]
	// Reclaim the backing array once drained so a long session doesn't hold a
	// buffer sized to its largest paste.
	if len(in.buf) == 0 {
		in.buf = nil
	}
	return n, nil
}

// output forwards rendered bytes to the host terminal, with the newline
// translation a tty driver would normally do — see package onlcr for why that
// is this side's job.
type output struct{ fn js.Value }

// Write hands the host a Uint8Array rather than a string. Converting to a Go
// string here would decode the bytes as UTF-8 one write at a time, and the
// renderer is free to split a multi-byte rune across two writes — that would
// silently corrupt it into replacement characters. The logo art is dense
// box-drawing glyphs, so this is not hypothetical. xterm.js accepts bytes and
// carries the decoder state across chunks itself.
func (o *output) Write(p []byte) (int, error) {
	buf := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(buf, p)
	o.fn.Invoke(buf)
	return len(p), nil
}

// newOutput wraps a JS callback as the program's stdout.
func newOutput(fn js.Value) io.Writer { return onlcr.NewWriter(&output{fn: fn}) }
