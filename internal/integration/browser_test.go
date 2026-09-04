package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/stretchr/testify/require"
)

// End-to-end tests for the browser client (cmd/zlily-wasm), run against a real
// proxy and a fake Lily.
//
// These exist because the browser build has failure modes no other test can
// reach. It shares every package with the native client, so unit tests pass
// while the page is broken, and three separate bugs got that far: a resumed
// session that restored its token but not its WebSocket, a blocking call on the
// goroutine that services JS callbacks (which deadlocks the whole runtime), and
// a reconnect with no password to reconnect with. Each looked fine natively.
//
// Node stands in for the browser. That is not free — see spoofNodeDetection in
// testdata/browser_harness.cjs for the one place Go behaves differently there —
// but it exercises the real wasm binary, the real bridge, and the real HTTP and
// WebSocket paths, and it replays the actual renderer output through a terminal
// emulator to assert what a person would see.

const (
	browserCols = 100
	browserRows = 30
)

var (
	wasmOnce sync.Once
	wasmPath string
	wasmErr  error
)

// buildBrowserWASM compiles cmd/zlily-wasm once per test binary.
func buildBrowserWASM(t *testing.T) string {
	t.Helper()
	wasmOnce.Do(func() {
		root, err := filepath.Abs("../..")
		if err != nil {
			wasmErr = err
			return
		}
		// Not t.TempDir(): that is removed when the first test finishes, and
		// every later test would find the binary gone.
		dir, err := os.MkdirTemp("", "zlily-wasm-test")
		if err != nil {
			wasmErr = err
			return
		}
		out := filepath.Join(dir, "zlily.wasm")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, "go", "build", "-o", out, "./cmd/zlily-wasm")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
		if b, err := cmd.CombinedOutput(); err != nil {
			wasmErr = fmt.Errorf("build zlily-wasm: %w\n%s", err, b)
			return
		}
		wasmPath = out
	})
	require.NoError(t, wasmErr)
	return wasmPath
}

// browserStep is one action the harness performs.
type browserStep struct {
	Wait   int    `json:"wait,omitempty"`   // milliseconds
	Write  string `json:"write,omitempty"`  // fed a rune at a time, as typing
	Resize []int  `json:"resize,omitempty"` // [cols, rows]
}

// browserResult is what the page's callbacks observed.
type browserResult struct {
	Saved              []string `json:"saved"`              // tokens handed to zlilySaveToken
	Cleared            int      `json:"cleared"`            // times it was told to forget one
	Error              string   `json:"error"`              // a Go panic or harness failure
	AnsweredBackground bool     `json:"answeredBackground"` // the OSC 11 query was replied to

	screen []string // the rendered display, one entry per row
	raw    []byte   // the renderer's byte stream, before replay
}

// row returns the screen line at y, or "" past the bottom.
func (r browserResult) row(y int) string {
	if y < 0 || y >= len(r.screen) {
		return ""
	}
	return r.screen[y]
}

// contains reports whether any screen row contains s.
func (r browserResult) contains(s string) bool {
	for _, line := range r.screen {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

func (r browserResult) String() string {
	return "screen:\n" + strings.Join(r.screen, "\n")
}

// runBrowser boots the wasm client against proxyAddr with an optionally stored
// token, performs steps, and returns what the page saw.
func runBrowser(t *testing.T, proxyAddr, token string, steps ...browserStep) browserResult {
	return runBrowserWithBackground(t, proxyAddr, token, "", steps...)
}

// runBrowserWithBackground is runBrowser with the terminal reporting a
// background colour when asked, as a real terminal emulator does. Empty means
// the query goes unanswered.
func runBrowserWithBackground(t *testing.T, proxyAddr, token, background string, steps ...browserStep) browserResult {
	t.Helper()

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping browser client test")
	}
	wasm := buildBrowserWASM(t)

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	require.NoError(t, err)
	wasmExec := filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js")
	require.FileExists(t, wasmExec, "the toolchain's wasm_exec.js is what the page loads")

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "script.json")
	outPath := filepath.Join(dir, "out.bin")
	resultPath := filepath.Join(dir, "result.json")

	script := map[string]any{
		"proxy": proxyAddr, "token": token,
		"cols": browserCols, "rows": browserRows,
		"steps": steps, "background": background,
	}
	b, err := json.Marshal(script)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(scriptPath, b, 0o600))

	harness, err := filepath.Abs(filepath.Join("testdata", "browser_harness.cjs"))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, harness, wasm, scriptPath, outPath, resultPath)
	cmd.Env = append(os.Environ(), "WASM_EXEC="+wasmExec)
	stderr, runErr := cmd.CombinedOutput()

	raw, readErr := os.ReadFile(outPath)
	if readErr != nil {
		t.Fatalf("harness produced no output (%v)\nexit: %v\n%s", readErr, runErr, stderr)
	}

	var res browserResult
	rb, err := os.ReadFile(resultPath)
	require.NoError(t, err, "harness produced no result file\n%s", stderr)
	require.NoError(t, json.Unmarshal(rb, &res))

	// A Go panic in the client still leaves a plausible-looking screen behind,
	// so fail on it explicitly rather than letting the assertions decide.
	require.Empty(t, res.Error, "the browser client crashed:\n%s", res.Error)

	res.screen = replayScreen(t, raw, browserCols, browserRows)
	res.raw = raw
	return res
}

// replayScreen feeds captured renderer output through a terminal emulator and
// returns the resulting rows, which is what a person would actually see.
func replayScreen(t *testing.T, stream []byte, w, h int) []string {
	t.Helper()
	em := vt.NewEmulator(w, h)
	// The emulator answers terminal queries on its own reader; leaving that
	// undrained deadlocks the write below.
	go func() { _, _ = io.Copy(io.Discard, em) }()

	// No \n → \r\n fixup here on purpose: the client is responsible for that
	// (internal/tui/onlcr), and if it stops doing it the screen must staircase
	// here too rather than being quietly repaired by the test.
	if _, err := em.Write(stream); err != nil {
		t.Fatalf("replay: %v", err)
	}

	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var sb strings.Builder
		for x := 0; x < w; x++ {
			if c := em.CellAt(x, y); c != nil && c.Content != "" {
				sb.WriteString(c.Content)
			} else {
				sb.WriteByte(' ')
			}
		}
		rows[y] = strings.TrimRight(sb.String(), " ")
	}
	return rows
}

// loginDialogShowing reports whether the credential dialog is on screen.
func (r browserResult) loginDialogShowing() bool {
	return r.contains("Username:") && r.contains("Password:")
}

// columnOf returns the column where s starts, or -1 if no row contains it.
func (r browserResult) columnOf(s string) int {
	for _, line := range r.screen {
		if i := strings.Index(line, s); i >= 0 {
			return len([]rune(line[:i]))
		}
	}
	return -1
}

// requireLeftAligned asserts that each marker begins at column 0.
//
// This is what catches a staircased screen. Scrollback lines are appended with
// a bare \n on this path, and Bubble Tea relies on the terminal driver's ONLCR
// to supply the carriage return — which no browser terminal does, so the client
// adds it (see internal/tui/onlcr). Lose that and each line starts where the
// previous one ended: "Users here:" at column 4, then 15, 22, 27. Substring
// assertions never notice, because every character is still present.
//
// The static login dialog is not a useful place to check this: it is painted
// with absolute cursor moves and stays aligned either way. Only appended output
// shows the drift.
func (r browserResult) requireLeftAligned(t *testing.T, markers ...string) {
	t.Helper()
	for _, m := range markers {
		got := r.columnOf(m)
		require.NotEqual(t, -1, got, "%q is not on screen\n%s", m, r)
		require.Equal(t, 0, got,
			"%q starts at column %d, not 0 — output is staircased, so the newline translation is gone\n%s",
			m, got, r)
	}
}

// requireAligned asserts that every marker begins in the same column.
//
// This is what catches a staircased screen. Bubble Tea emits a bare \n on this
// path and relies on the client to add the carriage return (see
// internal/tui/onlcr); lose that and each line starts where the last one ended,
// drifting right. Substring assertions do not notice — the text is all still
// present, just in the wrong places — so the geometry has to be checked
// directly. The replay deliberately performs no newline repair of its own for
// the same reason.
func (r browserResult) requireAligned(t *testing.T, markers ...string) {
	t.Helper()
	want := -1
	for _, m := range markers {
		got := r.columnOf(m)
		require.NotEqual(t, -1, got, "%q is not on screen\n%s", m, r)
		if want == -1 {
			want = got
			continue
		}
		require.Equal(t, want, got,
			"%q starts at column %d but earlier markers start at %d — the screen is staircased\n%s",
			m, got, want, r)
	}
}
