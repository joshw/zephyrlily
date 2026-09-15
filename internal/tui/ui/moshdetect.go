package ui

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

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

// When to say it is not decided here. The hint is a note about the display, so
// it can wait for a gap rather than landing in the middle of something the user
// is being asked to answer; that pacing is shared with every other unsolicited
// notice and lives in notices.go.

// moshPSMsg carries the result of the process-table check.
type moshPSMsg struct{ found bool }

// detectMoshCmd starts the probe. Deciding what to say with the answer is paced
// separately; see notices.go.
func detectMoshCmd() tea.Cmd {
	return func() tea.Msg { return moshPSMsg{found: moshServerRunning()} }
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

// moshHintNotice is the hint as a queued notice (see notices.go for the
// pacing).
//
// Both reasons for saying nothing are evaluated when the notice fires rather
// than when it was queued, which is what the deferred render is for. Minutes
// can pass in between: the probe may not have answered yet at queueing time,
// and someone who turned the workaround on during the wait - here, or in the
// zlilyStartup memo replaying behind us - does not need telling about it.
func moshHintNotice() pendingNotice {
	return pendingNotice{name: "mosh hint", render: func(m Model) (Model, []string) {
		if m.reserveLastColumn {
			return m, nil
		}
		return m, moshHintLines(m.moshPSFound)
	}}
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
