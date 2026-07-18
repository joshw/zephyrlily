package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/joshw/zephyrlily/internal/lilytest"
	"github.com/joshw/zephyrlily/internal/ptytest"
)

// buildZlilyBinary compiles the real zlily binary into dir for PTY tests.
func buildZlilyBinary(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "zlily")
	build := exec.Command("go", "build", "-o", bin, "github.com/joshw/zephyrlily/cmd/zlily")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build zlily: %v\n%s", err, out)
	}
	return bin
}

// bceReliantFill matches an erase operation issued while a colored background
// is set: SGR containing bg blue (44, the status bar) immediately followed by
// EL (CSI K / CSI 0K) or ECH (CSI n X). Such fills only paint the background
// on terminals with back-color-erase. GNU screen 4.00.03 — the terminal zlily
// lives in daily — has BCE off, so the fill renders as default background and
// the status bar becomes invisible.
var bceReliantFill = regexp.MustCompile(`\x1b\[[0-9;]*\b44\b[0-9;]*m(\x1b\[[0-9]*X|\x1b\[0?K)`)

// TestE2E_PTYScreenStatusBarNotBCEReliant runs the real zlily binary in a PTY
// under TERM=screen against a fake Lily stack, logs in, and asserts the
// renderer never paints the status bar's background via BCE-dependent erases.
// Regression for: status bar invisible under GNU screen after the bubbletea
// v2 migration (ultraviolet's canClearWith assumes BCE unconditionally).
func TestE2E_PTYScreenStatusBarNotBCEReliant(t *testing.T) {
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
	// Drive the auth dialog: username, Tab, password, Enter; wait for the
	// normal UI (status bar) to paint; then double C-c to quit.
	pipeline := `(sleep 1.5; printf 'alice\tpassword\r'; sleep 3; printf '\x03\x03'; sleep 1) | TERM=screen ` +
		ptytest.ScriptInvocation(capture, "stty rows 24 cols 80; "+bin+" client --proxy "+proxyAddr)
	cmd := exec.Command("sh", "-c", pipeline)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pty run: %v\n%s", err, out)
	}

	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	if len(data) < 1000 {
		t.Fatalf("suspiciously small capture (%d bytes) — did the TUI run?", len(data))
	}
	// Sanity: the login actually completed and the normal UI rendered.
	if !regexp.MustCompile(`Connected to TestServer`).Match(data) {
		t.Fatalf("login did not complete in the PTY session (no banner in %d bytes)", len(data))
	}

	if loc := bceReliantFill.FindIndex(data); loc != nil {
		lo := max(0, loc[0]-60)
		hi := min(len(data), loc[1]+20)
		t.Errorf("BCE-reliant background fill emitted under TERM=screen at byte %d: %q", loc[0], data[lo:hi])
	}
}
