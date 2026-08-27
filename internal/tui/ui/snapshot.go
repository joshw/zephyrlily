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
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
	"github.com/joshw/zephyrlily/internal/cmdarg"
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

// screenVersion reports the installed GNU screen version when the session
// looks like it's running inside screen (STY set, or TERM prefixed
// "screen"). Screen's escape-handling quirks vary by build — an ancient
// MAXSTR-limited version behaves very differently from a modern one — so
// diagnosing display corruption needs the actual version on this machine,
// not an assumption carried over from a past incident on different hardware.
func screenVersion() (string, bool) {
	if os.Getenv("STY") == "" && !strings.HasPrefix(os.Getenv("TERM"), "screen") {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// screen -v reliably prints its version banner and exits 1 (it treats -v
	// as an unrecognized flag on at least some builds) — so a parseable first
	// line is success regardless of the exit code; only its absence is failure.
	out, err := exec.CommandContext(ctx, "screen", "-v").CombinedOutput()
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	if line == "" {
		return fmt.Sprintf("<detection failed: %v>", err), true
	}
	return line, true
}

// ttySize reports the terminal size as the kernel has it, which is not
// necessarily the size the model believes.
//
// The model's width/height come from the resize events bubbletea delivers. If
// one is ever missed — a SIGWINCH lost between screen, ssh and this process —
// the app keeps laying out for a size the terminal no longer has, and every
// row it addresses is off by the difference. That failure is invisible from
// inside the model, which is exactly why the snapshot has to ask the kernel
// separately rather than print its own belief twice.
func ttySize() (w, h int, err error) {
	return term.GetSize(os.Stdout.Fd())
}

// errNotInScreen reports that there is no screen session to ask.
var errNotInScreen = errors.New("not running inside GNU screen (STY unset)")

// screenHardcopy asks GNU screen to dump what is actually on its display.
//
// This is the one piece of evidence every previous display-bug investigation
// lacked. A snapshot records what this app *sent*; replaying those bytes only
// ever shows what a correct terminal *would* do with them. Neither says what
// the user was looking at, so every round ended at "the bytes look fine" with
// the actual divergence unmeasured. screen keeps its own model of the display
// and will write it to a file on request, which closes that gap on exactly the
// setup where these bugs keep happening.
//
// It must run BEFORE the snapshot's repaint. The repaint is what fixes the
// corruption; capturing after it would faithfully record a healthy screen
// every time. See the ordering note on the %debug snapshot command.
func screenHardcopy() (string, error) {
	sty := os.Getenv("STY")
	if sty == "" {
		return "", errNotInScreen
	}

	// A unique name, reserved and then removed: screen creates the file
	// itself, so polling for its existence is what tells us the command was
	// carried out rather than silently dropped.
	f, err := os.CreateTemp("", "zlily-hardcopy-*.txt")
	if err != nil {
		return "", fmt.Errorf("temp file: %w", err)
	}
	path := f.Name()
	_ = f.Close()
	_ = os.Remove(path)
	defer func() { _ = os.Remove(path) }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	args := []string{"-S", sty}
	// screen exports the window number; naming it explicitly means the dump is
	// of this app's own window rather than whichever one happens to be current.
	if win := os.Getenv("WINDOW"); win != "" {
		args = append(args, "-p", win)
	}
	args = append(args, "-X", "hardcopy", path)

	out, err := exec.CommandContext(ctx, "screen", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("screen -X hardcopy: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	// -X returns as soon as the command is queued, so the file appears a moment
	// later, if at all.
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil {
			return string(b), nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return "", fmt.Errorf("screen accepted the command but wrote no file within 1.5s")
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
	if v, ok := screenVersion(); ok {
		fmt.Fprintf(&b, "screen=%s\n", v)
	}

	section("geometry")
	fmt.Fprintf(&b, "width=%d\nheight=%d\n", m.width, m.height)
	// The kernel's size next to the model's. These must agree; if they do not,
	// the app has been laying out for a terminal that is not there, and every
	// row it addressed was off by the difference — which is the first thing to
	// check on any "it drew in the wrong place" report.
	if tw, th, err := ttySize(); err != nil {
		fmt.Fprintf(&b, "tty size=<unavailable: %v>\n", err)
	} else if tw != m.width || th != m.height {
		fmt.Fprintf(&b, "tty size=%dx%d  *** MISMATCH: model believes %dx%d ***\n",
			tw, th, m.width, m.height)
	} else {
		fmt.Fprintf(&b, "tty size=%dx%d (agrees with the model)\n", tw, th)
	}
	fmt.Fprintf(&b, "viewport width=%d height=%d yoffset=%d totallines=%d atbottom=%v\n",
		m.viewport.Width(), m.viewport.Height(), m.viewport.YOffset(),
		m.viewport.TotalLineCount(), m.viewport.AtBottom())
	if m.debugMode {
		fmt.Fprintf(&b, "debugviewport width=%d height=%d yoffset=%d\n",
			m.debugViewport.Width(), m.debugViewport.Height(), m.debugViewport.YOffset())
	}
	fmt.Fprintf(&b, "inputheight=%d firstlinewidth=%d prompt=%q debugmode=%v\n",
		m.calculateInputHeight(), m.inputFirstLineWidth(), m.inputPromptText(), m.debugMode)
	// Where the terminal itself says the cursor is, asked before the
	// pre-snapshot repaint. The renderer tracks the cursor by dead reckoning
	// from the sequences it emits; if the terminal disagrees, every subsequent
	// relative movement lands somewhere other than intended, which is how a
	// display gets stuck drawing over one row.
	switch {
	case !m.cursorReport.ok:
		b.WriteString("cursor report=<no answer from terminal>\n")
	default:
		fmt.Fprintf(&b, "cursor report=row %d col %d (1-based; measured %s before this snapshot)\n",
			m.cursorReport.y+1, m.cursorReport.x+1,
			time.Since(m.cursorReport.at).Round(time.Millisecond))
	}

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

	section("responsiveness")
	for _, line := range m.perf.report() {
		b.WriteString(line + "\n")
	}

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
	fmt.Fprintf(&b, "items=%d renderepoch=%d lastseenid=%d autopageanchor=%d pager=%v mouse=%v scrollanchor=%d\n",
		len(m.output), m.renderEpoch, m.lastSeenID, m.autoPageAnchor,
		m.pagerEnabled, m.mouseEnabled, m.scrollAnchor)
	start := len(m.output) - 30
	if start < 0 {
		start = 0
	}
	for i := start; i < len(m.output); i++ {
		it := m.output[i]
		fmt.Fprintf(&b, "item[%d] type=%s id=%d cachedlines=%d cacheepoch=%d\n",
			i, it.Type, it.ID, len(it.cache), it.cacheEpoch)
	}

	// What the terminal had on screen, captured before this snapshot's repaint.
	// It sits immediately above the rendered frame on purpose: the two are the
	// same screen as the terminal has it and as this app believes it, and any
	// display bug is a difference between them.
	section("screen hardcopy (what GNU screen has on its display, pre-repaint)")
	switch {
	case m.snapshotHardcopy != "":
		b.WriteString(m.snapshotHardcopy)
		if !strings.HasSuffix(m.snapshotHardcopy, "\n") {
			b.WriteString("\n")
		}
	case m.snapshotHardcopyErr != "":
		fmt.Fprintf(&b, "(unavailable: %s)\n", m.snapshotHardcopyErr)
	default:
		b.WriteString("(not captured)\n")
	}

	section("hardcopy vs what zlily drew")
	switch {
	case m.snapshotHardcopy == "":
		b.WriteString("(no hardcopy to compare)\n")
	case m.snapshotProbeFrame == "":
		b.WriteString("(no frame captured alongside the hardcopy)\n")
	default:
		for _, line := range compareHardcopy(m.snapshotHardcopy, m.snapshotProbeFrame, m.viewport.Height()) {
			b.WriteString(line + "\n")
		}
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
	"Usage: %debug perf",
	"  Prints how responsive this session has been over its lifetime.",
}

// handleDebugCommand implements %debug and its subcommands. The snapshot is
// captured in two steps: first a full-screen repaint (ClearScreen), so the
// renderer tail ends with a complete frame the replay tooling can
// reconstruct the whole screen from; then, after a short tick that lets the
// repaint reach the terminal (and the tee), a snapshotCaptureMsg triggers
// the actual capture in Update (see the case in ui.go).
func (m Model) handleDebugCommand(fields []string) (Model, []string, tea.Cmd) {
	if len(fields) < 2 {
		return m, debugUsage, nil
	}

	// %debug perf prints the responsiveness metrics into the scrollback. The
	// same table goes into every snapshot; having it available live is what
	// lets a slowdown be watched as it develops, rather than only autopsied
	// after the fact.
	if cmdarg.Is(fields[1], "perf") {
		m.recordEvent("perf report requested")
		return m, m.perf.report(), nil
	}

	if !cmdarg.Is(fields[1], "snapshot") {
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

	// Order matters here, and it is the opposite of what looks natural.
	//
	// Everything that measures the TERMINAL runs first, while the screen is
	// still in whatever state prompted the snapshot: the cursor-position query
	// and screen's hardcopy of the live display. Only then comes the repaint,
	// and only after that is the model's own frame captured.
	//
	// Repainting first would be tidier and would destroy the evidence. A full
	// repaint is precisely the thing that clears this class of corruption — it
	// is how the user recovers from it — so a hardcopy taken afterwards would
	// faithfully record a healthy screen on every single report, and the one
	// measurement that distinguishes "the terminal disagrees with us" from "our
	// bytes were wrong" would always come back clean.
	//
	// The cursor query is asynchronous: it goes out now and its answer arrives
	// as a tea.CursorPositionMsg while the hardcopy is still running, which is
	// what gives it time to land before anything is written.
	m.cursorReport = cursorReport{}
	m.snapshotHardcopy, m.snapshotHardcopyErr, m.snapshotProbeFrame = "", "", ""
	return m, nil, tea.Sequence(
		func() tea.Msg { return tea.RequestCursorPosition() },
		// A short pause before measuring, so that the frame this very command
		// caused — the echoed "%debug snapshot" line, the cleared input — has
		// reached the terminal. Without it the hardcopy shows the screen as it
		// was one frame ago while the model has already moved on, and the two
		// halves of the comparison describe different instants.
		//
		// This is an ordinary frame, not a repaint: it does not clear the
		// corruption a snapshot exists to capture. Only the ClearScreen further
		// down does that, which is why it comes after all the measuring.
		tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
			return snapshotProbeMsg{path: path}
		}),
	)
}

// snapshotProbeMsg fires once the terminal has caught up, to measure it
// alongside the frame the model believes is showing.
type snapshotProbeMsg struct{ path string }

// hardcopyCmd captures the live display off the UI goroutine — it shells out to
// screen and waits for a file, neither of which belongs in Update.
func hardcopyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		text, err := screenHardcopy()
		return snapshotHardcopyMsg{path: path, text: text, err: err}
	}
}

// hardcopyRows normalises a screen dump or a rendered frame into comparable
// rows: no styling, and no NBSP padding (the status bar pads with U+00A0,
// which screen writes back as a plain space).
//
// Genuinely blank rows are kept. Trimming them away would be convenient and
// would blind the comparison to the exact thing it is looking for: a terminal
// showing text on a row the app believes it left empty is stale content that
// was never erased, which is the signature of the bug this instrumentation
// exists to catch. Only the empty string left over from a trailing newline is
// dropped, since that is a split artifact rather than a row.
func hardcopyRows(s string) []string {
	lines := strings.Split(ansi.Strip(s), "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.TrimRight(strings.ReplaceAll(line, "\u00a0", " "), " \t"))
	}
	return out
}

// padRows lengthens a to n rows with blanks, so that a row present on one side
// and absent on the other is compared rather than quietly skipped.
func padRows(a []string, n int) []string {
	for len(a) < n {
		a = append(a, "")
	}
	return a
}

// compareHardcopy reports whether the terminal's display agrees with the frame
// the app believed was showing, and how it differs when it does not.
//
// The verdict is the point of the whole exercise, so the snapshot states it
// rather than leaving a reader to diff 25 rows by eye.
//
// The screen is two regions and they fail differently, so they are judged
// separately. The top scrollRows rows scroll, and a display a line or two
// behind the model there is ordinary frame timing — a message that landed
// between the two measurements shifts every row uniformly and means nothing is
// wrong. The rows below (status bar, input area) are drawn in place and never
// scroll, so any difference there is real. Judging the whole screen under one
// shift, as a first cut of this did, finds no shift that fits and cries
// mismatch on a perfectly healthy capture.
func compareHardcopy(hardcopy, frame string, scrollRows int) []string {
	hc, want := hardcopyRows(hardcopy), hardcopyRows(frame)
	if len(hc) == 0 || len(want) == 0 {
		return []string{"(nothing to compare)"}
	}
	// Equal length, so every row position is judged on both sides.
	n := max(len(hc), len(want))
	hc, want = padRows(hc, n), padRows(want, n)
	if scrollRows <= 0 || scrollRows > len(hc) || scrollRows > len(want) {
		// Geometry we cannot trust: fall back to a straight in-place diff.
		scrollRows = min(len(hc), len(want))
	}

	fixedDiffs := rowDiffs(hc[scrollRows:], want[scrollRows:], 0, scrollRows)

	// Scrolling region: in place first, then a shift of a line or two.
	shift, scrollDiffs := 0, rowDiffs(hc[:scrollRows], want[:scrollRows], 0, 0)
	if len(scrollDiffs) > 0 {
		for _, try := range []int{1, -1, 2, -2} {
			if d := rowDiffs(hc[:scrollRows], want[:scrollRows], try, 0); len(d) == 0 {
				shift, scrollDiffs = try, nil
				break
			}
		}
	}

	if len(scrollDiffs) == 0 && len(fixedDiffs) == 0 {
		if shift == 0 {
			return []string{"MATCH - the terminal's display agrees with the frame zlily believed it had drawn."}
		}
		return []string{fmt.Sprintf(
			"MATCH - the scrollback is %+d row(s) out, which is ordinary frame timing rather than a"+
				" display fault; the status bar and input line agree exactly.", shift)}
	}

	out := []string{"*** MISMATCH - the terminal's display does not agree with what zlily drew. ***", ""}
	if len(scrollDiffs) > 0 {
		out = append(out, "scrolling region (no row offset fits, so this is not timing):")
		out = append(out, scrollDiffs...)
	}
	if len(fixedDiffs) > 0 {
		// One benign case lands here: the status bar clock ticking over between
		// the two measurements. It is deliberately not special-cased, because a
		// status bar showing the wrong time is also exactly what this bug looks
		// like when it strikes — the corrupted capture that prompted all this
		// had a status bar stuck a minute behind. Reporting it and letting a
		// reader judge beats a filter that hides the real thing.
		out = append(out, "status bar / input area (drawn in place, so any difference here is real):")
		out = append(out, fixedDiffs...)
	}
	return out
}

// rowDiffs compares two row sets with want offset by shift, describing each row
// that differs. base is added to reported row numbers so a slice of the screen
// still reports absolute rows. Rows with no counterpart under the shift are
// skipped rather than counted as differences.
func rowDiffs(hc, want []string, shift, base int) []string {
	var out []string
	for i := range hc {
		j := i + shift
		if j < 0 || j >= len(want) {
			continue
		}
		if hc[i] != want[j] {
			out = append(out,
				fmt.Sprintf("  row %d:", base+i),
				fmt.Sprintf("    terminal: %q", hc[i]),
				fmt.Sprintf("    zlily   : %q", want[j]))
		}
	}
	return out
}

// snapshotHardcopyMsg carries the pre-repaint display dump back into Update,
// which then repaints and captures the model's own state.
type snapshotHardcopyMsg struct {
	path string
	text string
	err  error
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

// cursorReport is the terminal's answer to a cursor-position query, with when
// it was measured — the age matters, because a report taken after a repaint
// says nothing about the state that prompted the snapshot.
type cursorReport struct {
	x, y int // 0-based, as bubbletea reports them
	at   time.Time
	ok   bool
}
