package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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
// So there is one question we can actually answer: is this user running a
// mosh-server on this machine. It is blunt — it is equally true of a mosh
// session that is not this one — but it survives a multiplexer, and it is the
// only signal that never claims more than it knows.
//
// Asking the terminal to identify itself looked better and was not. Mosh
// emulates the terminal and answers Secondary Device Attributes with
// "\033[>1;10;0c" (see CSI_SDA in mosh's src/terminal/terminalfunctions.cc),
// which reads like a fingerprint until you notice what it says: "a plain VT220,
// firmware 10". That is a natural thing for any emulator to claim, and Ghostty
// claims exactly it — reported by a user running zlily locally and being told
// they were on mosh. The query is gone rather than merely downgraded: a reply
// that cannot establish mosh cannot corroborate it either.
//
// So the process check gates the hint entirely, and the wording stays hedged
// even when it fires. A wrong answer then costs one suggestion the user can
// ignore; no behaviour changes on it.

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

// detectMoshCmd starts the probe. Deciding what to say with the answer is paced
// separately; see moshSettleMsg.
func detectMoshCmd() tea.Cmd {
	return func() tea.Msg { return moshPSMsg{found: moshServerRunning()} }
}

// moshSettleCmd schedules the next look for a gap.
func moshSettleCmd() tea.Cmd {
	return tea.Tick(moshSettleTick, func(time.Time) tea.Msg { return moshSettleMsg{} })
}

// moshServerRunning reports whether this user owns a mosh-server process.
func moshServerRunning() bool {
	// A browser tab reaches no process table, and mosh cannot be in the path
	// between this program and its display anyway.
	if !moshDetectable {
		return false
	}
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
	lines := moshHintLines(m.moshPSFound)
	if lines == nil {
		return m, nil
	}
	m.output = append(m.output, OutputItem{Type: "command", Data: lines})
	return m.syncViewportContent(), nil
}

// moshHintLines is the suggestion. Nil when there is nothing worth saying.
//
// The wording stays hedged whatever we think we know. Nothing available to us
// distinguishes this session from another of the same user's, so stating it as
// fact would be wrong for anyone who keeps a mosh session open elsewhere — and
// being told about a transport you are not using reads as a bug in zlily.
func moshHintLines(psFound bool) []string {
	if !psFound {
		return nil
	}
	return []string{
		"Note: you may be using mosh - a mosh-server is running on this machine.",
		"mosh 1.4.0 has two display bugs that can make the input line overwrite",
		"itself. '%debug lastcol on' avoids them, at the cost of one column of",
		"typing room. See '%help lastcol'.",
	}
}
