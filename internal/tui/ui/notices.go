package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Pacing unsolicited notices: the mosh hint, the once-per-session feature tip,
// and whatever joins them later.
//
// When to say it. These are notes, so they can wait for a gap; what they must
// not do is land in the middle of something the user is being asked to answer.
// Logging in ends with Lily's own prompts ("enter a blurb", "review now?") and
// then the review itself, and unsolicited lines arriving between the question
// and the answer is what this pacing avoids.
//
// So: nothing before the initial state fetch returns, which is gated on the
// login sync and so happens only once those prompts are answered; then nothing
// while the user is in the middle of anything; then wait for the output to go
// quiet, which is what puts a notice after the review rather than through the
// middle of it.
//
// One notice per quiet gap, in queue order. Two of them arriving together is
// the wall of text the pacing exists to prevent, and printing them one gap
// apart costs nothing: nobody is waiting on either.
//
// Both waits are capped. The quiet wait is capped because a busy channel may
// never fall silent and the alternative to an imperfect moment is never saying
// it at all. The busy wait has a much longer cap and drops the notice rather
// than printing anyway: speaking over a standing question is the one outcome
// worth giving up entirely to avoid, and a cap is also what stops the tick
// chain from running all session behind a -- MORE -- nobody is clearing.
const (
	noticeSettleTick = 1500 * time.Millisecond
	noticeQuietCap   = 45 * time.Second
	noticeBusyCap    = 5 * time.Minute
)

// pendingNotice is one queued notice.
//
// render is called at the moment the notice would be printed, not when it was
// queued, and may return nil to withdraw it. That is the whole point of the
// indirection: minutes can pass between queueing and a quiet gap, and whether
// a notice is still worth showing is a question about the state it would be
// shown in. It returns a Model so a notice can record that it fired.
type pendingNotice struct {
	name string // for %debug snapshot and test failures
	// itemType is the OutputItem.Type to append the notice as, which is what
	// decides how it is drawn: a tip is ruled off, the mosh hint reads as
	// ordinary command output.
	itemType string
	render   func(Model) (Model, []string)
}

// pendingNoticeNames lists a queue by name, for %debug snapshot.
func pendingNoticeNames(queue []pendingNotice) []string {
	names := make([]string, 0, len(queue))
	for _, n := range queue {
		names = append(names, n.name)
	}
	return names
}

// noticeSettleMsg is one tick of the wait for a good moment to speak up.
type noticeSettleMsg struct{}

// noticeSettleCmd schedules the next look for a gap.
func noticeSettleCmd() tea.Cmd {
	return tea.Tick(noticeSettleTick, func(time.Time) tea.Msg { return noticeSettleMsg{} })
}

// queueNotice adds one to the back of the queue and starts the settle loop if
// it is not already running. The returned cmd is nil when the loop was already
// going, since a second tick chain would make both of them advance the waited
// counter twice per tick.
func (m Model) queueNotice(n pendingNotice) (Model, tea.Cmd) {
	m.pendingNotices = append(m.pendingNotices, n)
	if m.noticeSettling {
		return m, nil
	}
	m.noticeSettling = true
	m.noticeOutputLen = len(m.output)
	m.noticeWaited = 0
	return m, noticeSettleCmd()
}

// noticeSettle is one tick of the wait: print the notice at the head of the
// queue if this is a good moment, otherwise look again shortly.
func (m Model) noticeSettle() (Model, tea.Cmd) {
	if len(m.pendingNotices) == 0 {
		m.noticeSettling = false
		return m, nil
	}
	m.noticeWaited += noticeSettleTick

	// Never speak over a question, or into a view that is not showing the
	// scrollback: the editor, the login dialog and the reconnect prompt all
	// replace it, and a search is about to move it. These are states the user
	// is in the middle of and will leave, so waiting is right - but they share
	// the long cap, so a prompt left standing does not keep a timer alive for
	// the rest of the session.
	//
	// Being scrolled back is deliberately not one of them. A notice is appended
	// at the end of the scrollback, so someone reading history has not missed
	// it - it is waiting at the bottom, which is where they are heading.
	// Deferring on that would only mean dropping the notice after the cap on
	// exactly the sessions with a backlog worth reading.
	if m.prompt != "" || m.authMode || m.editMode || m.searchMode || m.reconnectPrompt {
		if m.noticeWaited >= noticeBusyCap {
			return m.dropNotice()
		}
		return m, noticeSettleCmd()
	}

	// Wait for the output to stop moving, which is what keeps this out of the
	// middle of the review. Capped: a busy channel might never go quiet, and an
	// imperfect moment beats never saying it.
	moved := len(m.output) != m.noticeOutputLen
	m.noticeOutputLen = len(m.output)
	if moved && m.noticeWaited < noticeQuietCap {
		return m, noticeSettleCmd()
	}

	n := m.pendingNotices[0]
	m.pendingNotices = m.pendingNotices[1:]
	m.noticeWaited = 0

	var lines []string
	m, lines = n.render(m)
	if lines != nil {
		m.output = append(m.output, OutputItem{Type: n.itemType, Data: lines})
		// Our own output must not read as movement on the next tick, or a
		// second queued notice would always wait an extra gap for nothing.
		m.noticeOutputLen = len(m.output)
		m = m.syncViewportContent()
	}
	if len(m.pendingNotices) == 0 {
		m.noticeSettling = false
		return m, nil
	}
	return m, noticeSettleCmd()
}

// dropNotice gives up on the notice at the head of the queue and moves to the
// next, if any.
func (m Model) dropNotice() (Model, tea.Cmd) {
	m.pendingNotices = m.pendingNotices[1:]
	m.noticeWaited = 0
	if len(m.pendingNotices) == 0 {
		m.noticeSettling = false
		return m, nil
	}
	return m, noticeSettleCmd()
}

// queueSessionNoticesWhenReady queues the once-per-session notices, once both
// of the things they wait on have happened. It is called from each of them and
// does nothing until the second one arrives.
//
// Two conditions, for two different reasons. stateReady means the initial state
// fetch returned, which is gated on the login sync and so means Lily's own
// prompts have been answered - that is the timing rule in the note above.
// tipsLoaded means the stored %tips setting has been read, which is a local
// read and in practice lands long before the other; waiting for it anyway is
// what stops a first tip from racing the preference that says not to show one.
//
// The mosh hint is queued first: it is conditional and actionable, where a tip
// is neither.
func (m Model) queueSessionNoticesWhenReady() (Model, tea.Cmd) {
	if m.noticesQueued || !m.stateReady || !m.tipsLoaded {
		return m, nil
	}
	m.noticesQueued = true

	var cmds []tea.Cmd
	add := func(n pendingNotice) {
		var cmd tea.Cmd
		m, cmd = m.queueNotice(n)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if m.moshProbed {
		add(moshHintNotice())
	}
	add(sessionTipNotice())
	return m, tea.Batch(cmds...)
}

// sessionTipNotice is the once-per-session feature tip.
//
// Both the decision to show one and the choice of which are made at fire time.
// The decision, because a '%tips off' replayed from a zlilyStartup memo arrives
// as a clientcommand some unknowable moment after login, and re-checking here
// is what lets it still take effect. The choice, because the contextual M-s
// hint may have spent this session's tip in the meantime (see maybeShortenHint)
// and there is then nothing to pick.
func sessionTipNotice() pendingNotice {
	return pendingNotice{name: "session tip", itemType: "tip", render: func(m Model) (Model, []string) {
		if !m.tipsEnabled || m.tipShown {
			return m, nil
		}
		m.tipShown = true
		return m, tipNotice(randomTip())
	}}
}
