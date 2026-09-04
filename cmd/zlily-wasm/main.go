//go:build js && wasm

// Command zlily-wasm runs the zlily TUI inside a browser tab.
//
// It is the same Bubble Tea model the native client runs — see
// runTUI in cmd/zlily/main.go — with the terminal replaced by a host
// JavaScript terminal emulator (xterm.js). The host owns the screen and the
// keyboard; this program only exchanges bytes with it.
//
// Build:
//
//	GOOS=js GOARCH=wasm go build -o zlily.wasm ./cmd/zlily-wasm
//
// The page loads it with the toolchain's wasm_exec.js (found in
// $(go env GOROOT)/lib/wasm/), then drives it through the "zlily" global:
//
//	zlily.start({cols, rows, onOutput})  // begins the program
//	zlily.write(data)                    // keystrokes, from xterm.js onData
//	zlily.resize(cols, rows)             // geometry, from a fit addon
package main

import (
	"io"
	"log/slog"
	"sync"
	"syscall/js"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/ui"
)

func main() {
	prog := &bridge{}
	zlily := js.Global().Get("Object").New()
	zlily.Set("start", js.FuncOf(prog.start))
	zlily.Set("write", js.FuncOf(prog.write))
	zlily.Set("resize", js.FuncOf(prog.resize))
	js.Global().Set("zlily", zlily)

	// Signal readiness so the page can call start() without polling.
	if ready := js.Global().Get("zlilyOnReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}

	// wasm_exec treats main returning as program exit, which would tear down
	// the exported functions. Park forever instead.
	select {}
}

// bridge holds the pieces the JS side pokes at. start populates it; write and
// resize are no-ops until then.
//
// start does its work on a goroutine, so prog is written from there and read
// from the JS callbacks: the mutex is what keeps that honest.
type bridge struct {
	mu      sync.Mutex
	prog    *tea.Program
	in      *input
	started bool
}

// start boots the Bubble Tea program. Args: a single object with cols, rows,
// and onOutput (a function taking a Uint8Array of rendered bytes).
//
// It must not block. This runs on the goroutine servicing JS callbacks, and
// anything that waits here waits forever: resolving a fetch, firing a timer or
// delivering the next keystroke all require returning to the JS event loop
// first. So the callback only captures its arguments and returns, and the real
// work — which starts with an HTTP round trip to check a stored token — happens
// on a goroutine of its own.
func (b *bridge) start(_ js.Value, args []js.Value) any {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return nil
	}
	b.started = true

	cfg := args[0]
	cols, rows := cfg.Get("cols").Int(), cfg.Get("rows").Int()
	token := ""
	if t := cfg.Get("token"); t.Type() == js.TypeString {
		token = t.String()
	}

	// The input buffer exists before the program does, so keystrokes arriving
	// while the token is being checked are held rather than dropped.
	b.in = newInput()
	out := newOutput(cfg.Get("onOutput"))
	c := newClient(cfg)

	go b.run(c, out, cols, rows, token)
	return nil
}

// run resumes any prior session and drives the program. It is deliberately off
// the JS callback goroutine; see start.
func (b *bridge) run(c *client.Client, out io.Writer, cols, rows int, token string) {
	logChan, logger := ui.NewLogger()
	slog.SetDefault(logger)

	startup := []string{"Running in the browser — Ctrl-Z and shell-out features are unavailable."}

	// A token the host kept from a previous load. The proxy session outlives
	// the page, so re-attaching to it skips a login that would only have handed
	// back the same session. A token the proxy no longer knows is dropped here,
	// and the model falls back to its login dialog exactly as before.
	if token != "" {
		if user, err := c.ResumeSession(token); err != nil {
			slog.Debug("stored session token rejected", "err", err)
			if forget := js.Global().Get("zlilySaveToken"); forget.Type() == js.TypeFunction {
				forget.Invoke(js.Null())
			}
		} else {
			startup = append(startup, "Resumed your session as "+user+".")
		}
	}

	m := ui.New(c, logChan, startup...)

	// Everything the native client infers from a tty has to be stated here:
	// there is no fd to interrogate for color support, and no SIGWINCH, so
	// the initial geometry is passed in and later changes arrive via resize.
	prog := tea.NewProgram(m,
		tea.WithInput(b.in),
		tea.WithOutput(out),
		tea.WithColorProfile(colorprofile.TrueColor),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}),
		tea.WithWindowSize(cols, rows),
	)

	b.mu.Lock()
	b.prog = prog
	b.mu.Unlock()

	if _, err := prog.Run(); err != nil {
		slog.Error("tui exited", "err", err)
	}
	b.in.Close()
}

// newClient points the TUI at the proxy, defaulting to whatever served the
// page so its HTTP and WebSocket calls stay same-origin — and, critically,
// inheriting the page's scheme: a page served over https can open neither http
// nor ws connections, so the TUI has to speak https/wss back.
//
// An explicit "proxy" (and optional "secure") in the config overrides both,
// which is what the headless test harness uses since it has no document to
// have been served from.
func newClient(cfg js.Value) *client.Client {
	addr, secure := "", false

	if loc := js.Global().Get("location"); loc.Truthy() {
		addr = loc.Get("host").String()
		secure = loc.Get("protocol").String() == "https:"
	}
	if p := cfg.Get("proxy"); p.Type() == js.TypeString && p.String() != "" {
		addr = p.String()
		secure = cfg.Get("secure").Truthy()
	}

	if secure {
		return client.NewSecure(addr)
	}
	return client.New(addr)
}

// write feeds host keystrokes to the program's input. xterm.js onData already
// hands over exactly the bytes a real terminal would send — escape sequences,
// bracketed paste and mouse reports included — so this goes in unmodified and
// Bubble Tea's own parser does the rest.
func (b *bridge) write(_ js.Value, args []js.Value) any {
	b.mu.Lock()
	in := b.in
	b.mu.Unlock()
	if in != nil {
		in.Push([]byte(args[0].String()))
	}
	return nil
}

// resize reports new geometry. It stands in for the SIGWINCH that js/wasm has
// no way to receive (see signals_other.go in the vendored bubbletea).
func (b *bridge) resize(_ js.Value, args []js.Value) any {
	b.mu.Lock()
	prog := b.prog
	b.mu.Unlock()
	// A resize arriving before the program exists is dropped: the size it would
	// have carried is the one start already passed as the initial geometry.
	if prog != nil {
		prog.Send(tea.WindowSizeMsg{Width: args[0].Int(), Height: args[1].Int()})
	}
	return nil
}
