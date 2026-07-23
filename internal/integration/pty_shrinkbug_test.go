package integration

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/ptytest"
)

// TestE2E_PTYShrinkBoundaryColumnShift drives the real zlily binary in a
// real PTY (plain script(1), no mosh in the loop) through the exact
// keystroke sequence that produced a display-corruption bug in production
// when relayed through mosh: type (not paste) one character more than fits
// on the input area's first line — one keystroke at a time, with realistic
// gaps, matching how a human actually types — then backspace twice, crossing
// back from 2 input lines to 1. The root cause turned out to be entirely
// mosh's (a false-positive scroll-detection heuristic in its
// Display::new_frame — see forceRedraw's doc comment in
// internal/tui/ui/ui.go, and https://github.com/mobile-shell/mosh/issues/1400),
// not anything in zlily/bubbletea/ultraviolet, so this test is expected to
// stay clean: it's a permanent check that zlily's own renderer output, with
// no mosh in the path, never exhibits this or an analogous defect. No
// %debug snapshot needed: script(1) already captures every byte zlily
// writes, so the corrupted frame (if any — there shouldn't be one) is found
// and checked directly from the typescript by the same forensic technique
// used on real production debug snapshots earlier in this investigation.
func TestE2E_PTYShrinkBoundaryColumnShift(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY end-to-end test in -short mode")
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not available")
	}

	fake := lilytest.Start(t, lilytest.DefaultWorld())
	proxyAddr := startProxy(t, fake)

	dir := t.TempDir()
	bin := buildZlilyBinary(t, dir)
	capture := filepath.Join(dir, "typescript")

	const width = 89

	// Type one 'a' per printf call with a real gap between each — not one
	// burst — so this matches actual human keystroke timing rather than a
	// single write() of the whole string. width+1 chars crosses from 1 to 2
	// input lines (no prompt, so firstWidth == width); two backspaces cross
	// back from 2 lines to 1, which is the transition under investigation.
	var typing strings.Builder
	for i := 0; i < width+1; i++ {
		typing.WriteString("printf 'a'; sleep 0.02; ")
	}

	// Octal escapes: dash's printf (Ubuntu /bin/sh) has no \xHH form. \177
	// is DEL, the byte most terminals send for Backspace.
	pipeline := `(sleep 1.5; printf 'alice\tpassword\r'; sleep 2; ` +
		typing.String() +
		`sleep 0.3; printf '\177'; sleep 0.2; printf '\177'; sleep 1; ` +
		`printf '\003\003'; sleep 1) | TERM=xterm-256color ` +
		ptytest.ScriptInvocation(capture, "stty rows 26 cols "+strconv.Itoa(width)+"; "+bin+" client --proxy "+proxyAddr)
	if out, err := ptytest.RunWithTimeout(t, 90*time.Second, pipeline); err != nil {
		t.Fatalf("pty run: %v\n%s", err, out)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	t.Logf("captured %d bytes", len(data))

	if dest := os.Getenv("ZLILY_TYPESCRIPT_KEEP"); dest != "" {
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			t.Logf("could not keep typescript at %s: %v", dest, err)
		} else {
			t.Logf("typescript kept at %s", dest)
		}
	}

	// script(1)'s shutdown sequence includes an "exit alternate screen"
	// escape that would otherwise blank the emulator's view of the final
	// frame; trim it.
	raw := data
	if i := strings.Index(string(raw), "\x1b[?1049l"); i >= 0 {
		raw = raw[:i]
	}
	const height = 26

	screenAt := func(n int) *vt.Emulator {
		em := vt.NewEmulator(width, height)
		go func() { _, _ = io.Copy(io.Discard, em) }()
		_, _ = em.Write(raw[:n])
		return em
	}
	// Look for a run of 5+ 'a' in the bottom 3 rows (input area) — long
	// enough to be the typed content, not a coincidental 'a' in prose or the
	// status bar.
	hasARun := func(n int) bool {
		em := screenAt(n)
		for y := height - 3; y < height; y++ {
			run := 0
			for x := 0; x < width; x++ {
				c := em.CellAt(x, y)
				ch := byte(' ')
				if c != nil && len(c.Content) == 1 {
					ch = c.Content[0]
				}
				if ch == 'a' {
					run++
					if run >= 5 {
						return true
					}
				} else {
					run = 0
				}
			}
		}
		return false
	}

	upper := len(raw)
	const step = 300
	n := upper
	for n > 0 && !hasARun(n) {
		n -= step
		if n < 0 {
			n = 0
		}
	}
	if !hasARun(n) {
		t.Fatal("no run of 5+ 'a' found near the bottom of the screen anywhere in the capture — repro sequence may not have landed")
	}
	lo, hi := n, n+step
	if hi > upper {
		hi = upper
	}
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if hasARun(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	t.Logf("last frame with a run of 'a' near the input row: byte %d of %d", lo, len(raw))

	em := screenAt(lo)
	shifted := false
	for y := 0; y < height; y++ {
		var sb strings.Builder
		for x := 0; x < width; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		trimmed := strings.TrimRight(sb.String(), " ")
		if trimmed == "" {
			continue
		}
		leading := strings.HasPrefix(trimmed, " ")
		t.Logf("%2d: %q (len=%d, leading-space=%v)", y, trimmed, len(trimmed), leading)
		if leading && strings.Trim(trimmed, " a") == "" {
			shifted = true
		}
	}
	if shifted {
		t.Errorf("REPRODUCED: input row shows a leading-space shift at the shrink boundary")
	} else {
		t.Log("clean: no column shift detected at the shrink boundary")
	}
}
