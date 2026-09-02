package ui

import (
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Detecting mosh, in two parts.
//
// There is no reliable way to know that THIS session is running over mosh.
// Mosh leaves no marker in its child's environment (verified by running
// 'mosh-server new -- env': no MOSH_* variable survives, and TERM is forced to
// a value plenty of other things use), and even if it did, attaching to a
// screen or tmux session started under a different transport would carry the
// stale answer. Walking our own parent chain does not work either: the usual
// setup runs zlily inside screen, whose daemon is reparented to init, so
// mosh-server is nowhere above us.
//
// So we ask two questions and say only as much as the answers support:
//
//  1. Secondary Device Attributes. Mosh emulates the terminal itself and
//     answers this query with a hardcoded signature of its own — see
//     CSI_SDA in mosh's src/terminal/terminalfunctions.cc. When that
//     signature comes back, mosh is certain. It only comes back when nothing
//     is between us and mosh: screen answers the query itself (it replies
//     83 = 'S'), so under a multiplexer this tells us nothing about mosh.
//
//  2. Is this user running a mosh-server on this machine. Blunt, and true of
//     sessions that are not ours, but it is what survives a multiplexer. It
//     supports "might be", nothing stronger.
//
// A wrong answer costs one suggestion the user can ignore; no behaviour
// changes on it.

// moshSDA is mosh's hardcoded Secondary Device Attributes reply, "\033[>1;10;0c"
// (a claim to be a plain VT220, firmware 10). Real terminals answer with their
// own identity: iTerm2 with 0;95;0, Terminal.app 1;95;0, xterm 41;<patch>;0,
// screen 83;<version>;0, tmux 84;0;0.
var moshSDA = []int{1, 10, 0}

// When to say it. The hint is a note about the display, so it can wait for a
// gap; what it must not do is land in the middle of something the user is being
// asked to answer. Logging in ends with Lily's own prompts ("enter a blurb",
// "review now?") and then the review itself, and an unsolicited three lines
// arriving between the question and the answer is what this pacing avoids.
//
// So: nothing before the initial state fetch returns, which is gated on the
// login sync and so happens only once those prompts are answered; then nothing
// while any prompt is outstanding; then wait for the output to go quiet, which
// is what puts it after the review rather than through the middle of it.
//
// The quiet wait is capped, because a busy channel may never fall silent and
// the alternative to an imperfect moment is never saying it at all. The prompt
// check has a much longer cap of its own and no override: printing over a
// question is the one outcome worth giving up on the hint entirely to avoid.
const (
	moshSettleTick = 1500 * time.Millisecond
	moshQuietCap   = 45 * time.Second
	moshPromptCap  = 5 * time.Minute
)

// moshPSMsg carries the result of the process-table check.
type moshPSMsg struct{ found bool }

// moshSettleMsg is one tick of the wait for a good moment to speak up.
type moshSettleMsg struct{}

// detectMoshCmds starts both probes. Deciding what to say with the answers is
// paced separately; see moshSettleMsg.
func detectMoshCmds() []tea.Cmd {
	return []tea.Cmd{
		func() tea.Msg { return moshPSMsg{found: moshServerRunning()} },
		tea.Raw(ansi.RequestSecondaryDeviceAttributes),
	}
}

// moshSettleCmd schedules the next look for a gap.
func moshSettleCmd() tea.Cmd {
	return tea.Tick(moshSettleTick, func(time.Time) tea.Msg { return moshSettleMsg{} })
}

// isMoshSDA reports whether a Secondary Device Attributes reply is mosh's.
func isMoshSDA(attrs []int) bool { return slices.Equal(attrs, moshSDA) }

// moshServerRunning reports whether this user owns a mosh-server process.
func moshServerRunning() bool {
	if runtime.GOOS == "windows" {
		return false
	}
	// comm rather than the full argv: matching arguments would also match the
	// grep-alikes and any editor with the name in a filename.
	out, err := exec.Command("ps", "-u", strconv.Itoa(os.Getuid()), "-o", "comm=").Output()
	if err != nil {
		return false // no ps, or it does not take these flags: just skip the hint
	}
	return moshServerInPS(string(out))
}

// moshServerInPS scans 'ps -o comm=' output for a mosh-server.
func moshServerInPS(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		// comm can be a full path (macOS prints one for some processes).
		if i := strings.LastIndexByte(name, '/'); i >= 0 {
			name = name[i+1:]
		}
		// HasPrefix, not Contains: a retitled "mosh-server: [mosh] ..." must
		// match, but someone's ./my-mosh-server-wrapper must not.
		if strings.HasPrefix(name, "mosh-server") {
			return true
		}
	}
	return false
}

// moshHintSettle is one tick of the wait: show the hint if this is a good
// moment, otherwise look again shortly.
func (m Model) moshHintSettle() (Model, tea.Cmd) {
	if m.moshHintDone {
		return m, nil
	}
	m.moshHintWaited += moshSettleTick

	// Never speak over a question. This one has no override: if the user leaves
	// a prompt standing that long, dropping the hint is the right outcome.
	if m.prompt != "" {
		if m.moshHintWaited >= moshPromptCap {
			m.moshHintDone = true
			return m, nil
		}
		return m, moshSettleCmd()
	}

	// Wait for the output to stop moving, which is what keeps this out of the
	// middle of the review. Capped: a busy channel might never go quiet, and an
	// imperfect moment beats never saying it.
	moved := len(m.output) != m.moshHintOutputLen
	m.moshHintOutputLen = len(m.output)
	if moved && m.moshHintWaited < moshQuietCap {
		return m, moshSettleCmd()
	}

	m.moshHintDone = true
	// Say nothing if the workaround is already on: someone who turned it on,
	// here or while the probes were in flight, does not need telling.
	if m.reserveLastColumn {
		return m, nil
	}
	lines := moshHintLines(m.moshSDACertain, m.moshPSFound)
	if lines == nil {
		return m, nil
	}
	m.output = append(m.output, OutputItem{Type: "command", Data: lines})
	return m.syncViewportContent(), nil
}

// moshHintLines is the suggestion, worded to match how much we actually know.
// Nil when there is nothing worth saying.
func moshHintLines(certain, psFound bool) []string {
	var lead []string
	switch {
	case certain:
		lead = []string{
			"Note: this session is running over mosh (it identified itself).",
		}
	case psFound:
		lead = []string{
			"Note: a mosh-server is running here, so this session may be going",
			"through it. There is no way to tell from inside a screen session.",
		}
	default:
		return nil
	}
	return append(lead,
		"mosh 1.4.0 has two display bugs that can make the input line overwrite",
		"itself. '%debug lastcol on' avoids them, at the cost of one column of",
		"typing room. See '%help lastcol'.",
	)
}
