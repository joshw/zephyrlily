package ui

// Debug snapshots: %debug snapshot [path] writes the TUI's internal state to
// a file for attaching to bug reports on hard-to-reproduce issues. The
// snapshot captures all three layers a display bug can hide between: the
// model's state, the frame the app rendered (viewContent), and the raw bytes
// the renderer actually sent to the terminal (via the teebuf tap wired in
// cmd/zlily). A snapshot can be replayed and diffed mechanically:
//
//	ZLILY_SNAPSHOT=/path/to/file go test ./internal/tui/ui -run TestReplaySnapshot -v

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// RendererTap provides the tail of raw bytes written to the terminal. It is
// implemented by teebuf.Writer; a nil tap simply omits the byte-tail section.
type RendererTap interface {
	Tail() []byte
}

// WithRendererTap returns the model with the renderer output tap attached.
func (m Model) WithRendererTap(tap RendererTap) Model {
	m.rendererTap = tap
	return m
}

// ── event rings ───────────────────────────────────────────────────────────────

const (
	inputEventRingCap = 200
	msgMetaRingCap    = 100
)

// ringEntry is one timestamped line in a diagnostic ring.
type ringEntry struct {
	when time.Time
	desc string
}

// ring is a fixed-capacity FIFO of the most recent entries.
type ring struct {
	buf  []ringEntry
	pos  int
	full bool
}

func newRing(capacity int) *ring {
	return &ring{buf: make([]ringEntry, capacity)}
}

func (r *ring) add(desc string) {
	if r == nil {
		return
	}
	r.buf[r.pos] = ringEntry{when: time.Now(), desc: desc}
	r.pos = (r.pos + 1) % len(r.buf)
	if r.pos == 0 {
		r.full = true
	}
}

// entries returns the ring's contents, oldest first.
func (r *ring) entries() []ringEntry {
	if r == nil {
		return nil
	}
	if !r.full {
		return r.buf[:r.pos]
	}
	out := make([]ringEntry, 0, len(r.buf))
	out = append(out, r.buf[r.pos:]...)
	out = append(out, r.buf[:r.pos]...)
	return out
}

// modeName names the input mode that will handle the next key event.
func (m Model) modeName() string {
	switch {
	case m.authMode:
		return "auth"
	case m.editMode:
		return "edit"
	case m.reconnectPrompt:
		return "reconnect"
	case m.searchMode:
		return "search"
	case m.pasteMode:
		return "paste"
	default:
		return "normal"
	}
}

// recordKeyEvent notes a key press in the input-event ring.
func (m *Model) recordKeyEvent(msg tea.KeyPressMsg) {
	m.inputEvents.add(fmt.Sprintf("key %-14s code=%q text=%q mod=%d mode=%s",
		msg.String(), msg.Code, msg.Text, msg.Mod, m.modeName()))
}

// recordEvent notes a non-key input event (paste, wheel, resize, …).
func (m *Model) recordEvent(format string, args ...any) {
	m.inputEvents.add(fmt.Sprintf(format, args...))
}

// recordMsgMeta notes proxy traffic metadata (never message content).
func (m *Model) recordMsgMeta(format string, args ...any) {
	m.msgMeta.add(fmt.Sprintf(format, args...))
}

// ── snapshot assembly ─────────────────────────────────────────────────────────

// buildSnapshot renders the whole diagnostic snapshot as one text document.
// Pure function of the model plus the renderer tail so it is unit-testable;
// it runs inside Update, where reading model state needs no locking.
func buildSnapshot(m Model, rendererTail []byte) string {
	var b strings.Builder
	section := func(name string) { fmt.Fprintf(&b, "\n== %s ==\n", name) }

	fmt.Fprintf(&b, "== zlily debug snapshot v1 ==\n")
	fmt.Fprintf(&b, "generated=%s\n", time.Now().Format(time.RFC3339))
	b.WriteString("PRIVACY: this file contains recent typed input, message metadata,\n")
	b.WriteString("and current screen content. Review before sharing.\n")

	section("build")
	fmt.Fprintf(&b, "go=%s os=%s arch=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if bi, ok := debug.ReadBuildInfo(); ok {
		fmt.Fprintf(&b, "main=%s %s\n", bi.Main.Path, bi.Main.Version)
		for _, dep := range bi.Deps {
			switch {
			case strings.HasPrefix(dep.Path, "charm.land/"),
				strings.Contains(dep.Path, "charmbracelet/ultraviolet"),
				strings.Contains(dep.Path, "charmbracelet/x/ansi"):
				fmt.Fprintf(&b, "dep=%s %s\n", dep.Path, dep.Version)
			}
		}
	}

	section("environment")
	for _, k := range []string{"TERM", "LANG", "LC_ALL", "LC_CTYPE", "COLORTERM", "TMUX", "STY"} {
		fmt.Fprintf(&b, "%s=%s\n", k, os.Getenv(k))
	}
	_, sshSession := os.LookupEnv("SSH_TTY")
	fmt.Fprintf(&b, "ssh=%v\n", sshSession)

	section("geometry")
	fmt.Fprintf(&b, "width=%d\nheight=%d\n", m.width, m.height)
	fmt.Fprintf(&b, "viewport width=%d height=%d yoffset=%d totallines=%d atbottom=%v\n",
		m.viewport.Width(), m.viewport.Height(), m.viewport.YOffset(),
		m.viewport.TotalLineCount(), m.viewport.AtBottom())
	if m.debugMode {
		fmt.Fprintf(&b, "debugviewport width=%d height=%d yoffset=%d\n",
			m.debugViewport.Width(), m.debugViewport.Height(), m.debugViewport.YOffset())
	}
	fmt.Fprintf(&b, "inputheight=%d firstlinewidth=%d prompt=%q debugmode=%v\n",
		m.calculateInputHeight(), m.inputFirstLineWidth(), m.inputPromptText(), m.debugMode)

	section("input state")
	fmt.Fprintf(&b, "inputvalue=%q\n", m.inputValue)
	fmt.Fprintf(&b, "inputcursor=%d len=%d\n", m.inputCursor, len(m.inputValue))
	fmt.Fprintf(&b, "pastemode=%v pasteeatflag=%v pasteeatbuf=%v metaprefix=%v quitpending=%v\n",
		m.pasteMode, m.pasteEatFlag, m.pasteEatBuf, m.metaPrefix, m.quitPending)
	fmt.Fprintf(&b, "search mode=%v back=%v buf=%q save=%q idx=%d pos=%d\n",
		m.searchMode, m.searchBack, m.searchBuf, m.searchSave, m.searchIdx, m.searchPos)
	fmt.Fprintf(&b, "history pos=%d save=%q entries=%d\n", m.historyPos, m.historySave, len(m.history))
	fmt.Fprintf(&b, "completion active=%v token=%q fore=%q\n",
		m.completionActive, m.completionToken, m.completionFore)
	fmt.Fprintf(&b, "killring len=%d lastkill=%v\n", len(m.killRing), m.lastKill)

	section("recent input events (oldest first)")
	for _, e := range m.inputEvents.entries() {
		fmt.Fprintf(&b, "%s %s\n", e.when.Format("15:04:05.000"), e.desc)
	}

	section("recent proxy traffic (metadata only, oldest first)")
	for _, e := range m.msgMeta.entries() {
		fmt.Fprintf(&b, "%s %s\n", e.when.Format("15:04:05.000"), e.desc)
	}
	if len(m.debugMsgs) > 0 {
		fmt.Fprintf(&b, "debug pane transcript (%d lines, tail):\n", len(m.debugMsgs))
		tail := m.debugMsgs
		if len(tail) > 100 {
			tail = tail[len(tail)-100:]
		}
		for _, line := range tail {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	section("scrollback metadata")
	fmt.Fprintf(&b, "items=%d renderepoch=%d lastseenid=%d autopageanchor=%d pager=%v wheel=%v scrollanchor=%d\n",
		len(m.output), m.renderEpoch, m.lastSeenID, m.autoPageAnchor,
		m.pagerEnabled, m.mouseWheel, m.scrollAnchor)
	start := len(m.output) - 30
	if start < 0 {
		start = 0
	}
	for i := start; i < len(m.output); i++ {
		it := m.output[i]
		fmt.Fprintf(&b, "item[%d] type=%s id=%d cachedlines=%d cacheepoch=%d\n",
			i, it.Type, it.ID, len(it.cache), it.cacheEpoch)
	}

	section("rendered frame (quoted lines)")
	for _, line := range strings.Split(m.viewContent(), "\n") {
		fmt.Fprintf(&b, "%s\n", strconv.Quote(line))
	}

	section("renderer output tail (base64)")
	if len(rendererTail) == 0 {
		b.WriteString("(no renderer tap attached)\n")
	} else {
		enc := base64.StdEncoding.EncodeToString(rendererTail)
		for len(enc) > 76 {
			b.WriteString(enc[:76] + "\n")
			enc = enc[76:]
		}
		b.WriteString(enc + "\n")
	}

	section("goroutines")
	stack := make([]byte, 1<<20)
	stack = stack[:runtime.Stack(stack, true)]
	b.Write(stack)

	section("end of snapshot")
	return b.String()
}

// ── the %debug command family ─────────────────────────────────────────────────

var debugUsage = []string{
	"Usage: %debug snapshot [path]",
	"  Writes a diagnostic snapshot of the TUI's internal state for bug",
	"  reports. Default path: ~/zlily-debug-<timestamp>.txt",
	"  The file includes recent typed input and screen content - review",
	"  before sharing.",
}

// handleDebugCommand implements %debug and its subcommands. The snapshot is
// captured in two steps: first a full-screen repaint (ClearScreen), so the
// renderer tail ends with a complete frame the replay tooling can
// reconstruct the whole screen from; then, after a short tick that lets the
// repaint reach the terminal (and the tee), a snapshotCaptureMsg triggers
// the actual capture in Update (see the case in ui.go).
func (m Model) handleDebugCommand(fields []string) (Model, []string, tea.Cmd) {
	if len(fields) < 2 || !strings.EqualFold(fields[1], "snapshot") {
		return m, debugUsage, nil
	}

	path := ""
	if len(fields) >= 3 {
		path = fields[2]
	} else {
		base := "zlily-debug-" + time.Now().Format("20060102-150405") + ".txt"
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, base)
		} else {
			path = base
		}
	}

	m.recordEvent("snapshot requested path=%s", path)
	return m, nil, tea.Sequence(
		func() tea.Msg { return tea.ClearScreen() },
		tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
			return snapshotCaptureMsg{path: path}
		}),
	)
}

// captureSnapshot assembles the snapshot from the current model (called from
// Update on snapshotCaptureMsg, where model state is safely readable) and
// returns the command performing the blocking file write.
func (m Model) captureSnapshot(path string) tea.Cmd {
	var tail []byte
	if m.rendererTap != nil {
		tail = m.rendererTap.Tail()
	}
	content := buildSnapshot(m, tail)
	return func() tea.Msg {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return snapshotResultMsg{err: err, path: path}
		}
		return snapshotResultMsg{path: path}
	}
}

// snapshotCaptureMsg fires after the pre-snapshot repaint to capture state.
type snapshotCaptureMsg struct{ path string }

// snapshotResultMsg reports the outcome of a %debug snapshot file write.
type snapshotResultMsg struct {
	path string
	err  error
}
