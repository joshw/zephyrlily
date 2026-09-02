package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

// OutputItem represents a single item in the output buffer.
// It stores raw data so it can be reformatted when the window size changes.
// mouseWheelLines is how many output lines one wheel notch scrolls.
const mouseWheelLines = 3

// mouseOnNotice is printed whenever mouse mode is switched on by %mouse.
// Turning on mouse reporting makes the terminal forward wheel and click events
// to the app, which pre-empts its own click-drag text selection — there is no
// way to have both, so the notice spells out the two ways back to a working
// copy/paste. Each terminal exposes a different modifier to bypass reporting
// and select natively; M-m is the escape hatch for anyone whose terminal is not
// listed, or who would rather not learn which modifier theirs uses.
var mouseOnNotice = []string{
	"Mouse mode: on (wheel scrolls the output; click moves the input cursor)",
	"",
	"This captures the mouse, so click-drag text selection no longer works",
	"normally. To select and copy text, either hold a bypass modifier while",
	"dragging:",
	"  - most terminals (xterm, GNOME Terminal, Windows Terminal): Shift",
	"  - iTerm2: Option (⌥)",
	"  - macOS Terminal.app: Fn (or Shift)",
	"or press M-m to toggle mouse mode off, select as usual, then M-m again.",
	"",
	"Put '%mouse on' in your zlilyStartup memo to have it on at every login.",
}

type OutputItem struct {
	Type string      // "text", "event", "command", "error", "input", "log"
	Data interface{} // raw data (string, map[string]interface{}, []string, logMsg)
	ID   int64       // WSServerMsg.ID of the message that produced this item (0 for local items)

	// Render cache: the lines this item produced the last time it was rendered,
	// valid while cacheEpoch matches Model.renderEpoch (see renderItem). Without
	// it every incoming message re-renders (regex, wrapping, styling) the whole
	// scrollback — O(n²) over a session.
	cache      []string
	cacheEpoch int
}

// maxScrollback caps the number of OutputItems retained; the oldest are
// trimmed once exceeded so a long-lived session's memory stays bounded.
const maxScrollback = 10000

// scrollbackSlack is how far past the cap the scrollback is allowed to run
// before a trim. Trimming copies the retained items to a fresh slice, so
// trimming one item per message — which is what a session sitting at the cap
// does forever — would put an O(scrollback) memmove back on the path of every
// incoming message, the very thing the incremental sync exists to remove.
// Dropping a block at a time amortizes that to a fraction of one item's cost
// per message, at the price of holding a few hundred extra items. Same
// bulk-eviction reasoning as dedupCap's pruning below.
const scrollbackSlack = maxScrollback / 16

// maxDebugMsgs caps the debug-pane transcript lines for the same reason.
const maxDebugMsgs = 2000

// Model is the root Bubble Tea model for the TUI.
type Model struct {
	client *client.Client
	state  *api.StateResponse
	output []OutputItem // scrollback items (raw, to be formatted at render time)
	prompt string       // latest prompt text from server
	width  int
	height int

	// Output area (see scrollview.go for why this is not bubbles/viewport)
	viewport scrollView

	// syncedItems/syncedEpoch record what the viewport's line content already
	// reflects: the number of leading output items whose rendered lines have
	// been appended to it, and the render epoch they were rendered at. An
	// item's lines never change once rendered — items are only appended at the
	// end and trimmed from the front, and nothing rewrites the Data of one
	// already in the scrollback — so as long as these two agree with the
	// model, syncViewportContent has only the newest items left to append.
	// A mismatch (epoch bumped by a width/debug-split/whoami change, or the
	// item count going backwards) falls back to rebuilding the whole thing.
	syncedItems int
	syncedEpoch int

	// Input area - using bubbles/textarea for display
	input textarea.Model

	// Manual cursor tracking (textarea doesn't expose byte position)
	inputValue  string
	inputCursor int

	// Key bindings
	keys KeyMap

	// Kill ring for emacs-style editing
	killRing string // last killed text for C-y yank
	lastKill bool   // whether prev action was a kill (for kill-append)

	// Command history
	history     []string // sent commands, oldest first
	historyPos  int      // -1=live, ≥0=browsing (0=oldest)
	historySave string   // live input saved when entering history browse

	// Incremental search
	searchMode bool
	searchBack bool   // true=reverse (C-r), false=forward (C-s)
	searchBuf  string // pattern typed so far
	searchSave string // input saved on entering search
	searchIdx  int    // index of current match in history (-1=current line)
	searchPos  int    // byte offset of current match within the current line/item

	// Meta prefix: Esc followed by a key is treated as M-<key>
	metaPrefix bool

	// Double C-c to quit: set by the first C-c, cleared by any other key.
	quitPending bool

	// Paste mode: newlines become spaces, leading spaces after newlines are eaten
	pasteMode    bool
	pasteEatFlag bool // eating whitespace after a newline
	pasteEatBuf  bool // have seen one non-post-newline space (next space triggers eating)

	// Spell checking
	spellChecker *SpellChecker

	// Debug view
	debugMode     bool
	debugKeys     bool     // log every key event to debugMsgs
	debugMsgs     []string // raw JSON messages
	debugViewport scrollView

	// Scroll state
	lastSeenID int64 // highest WSServerMsg.ID whose output has been visible; never decreases

	// processedIDs records every WSServerMsg ID already incorporated into the
	// model, from either delivery path. The live WebSocket stream and the GET
	// /events history replay overlap: the socket delivers events from the
	// moment it connects (including the login-time buffer replay for fresh
	// sessions), while the history fetch returns everything after the proxy's
	// stored last-seen ID — which can lag far behind what the socket already
	// delivered. Without this guard a reattach re-appends the detach review
	// and anything else in the overlap. A set (not a high-water mark) because
	// the replay legitimately carries IDs both below and above what has been
	// seen live. A pointer, like the diagnostic rings, so the recorded state
	// survives the value-copies Model goes through on every Update.
	processedIDs *dedupSet

	// sessionToken is the proxy session token that lastSeenID, processedIDs and
	// the IDs stored on output items all belong to. The proxy numbers messages
	// from a per-session counter that restarts at 1, and a reconnect always
	// builds a new session, so a token change invalidates every one of them —
	// see resetSessionIDs.
	sessionToken string

	// Auto-paging: auto-scroll up to one page of output past the anchor, then
	// pause at -- MORE -- (see syncViewportContent).
	autoPageAnchor int  // viewport line count at the user's last interaction while at the bottom; -1 = disabled
	pagerEnabled   bool // false = never pause; scroll straight to bottom (%page off)
	// mouseEnabled gates mouse reporting as a whole (%mouse / M-m; off by
	// default): wheel scrolling of the viewport and click-to-position in the
	// input line both ride on it, because both need the terminal to forward
	// mouse events — which costs native click-drag selection (see
	// mouseOnNotice). It stays a command as well as a key binding so that
	// '%mouse on' can go in a zlilyStartup memo.
	mouseEnabled bool

	// Link previews: gray ghost text drawn after a URL in the input line,
	// summarising the page it points at. See linkpreview.go for why the text is
	// held here rather than spliced into inputValue.
	//
	// The maps are shared across the value-copies Model goes through on every
	// Update, which is what we want: linkPreviewCache is a session-lifetime memo
	// of what each URL resolved to, and linkPreviewPending must be seen by every
	// copy or one URL would be fetched once per keystroke.
	linkPreviewOn        bool                // %linkpreview toggle
	linkPreviewCache     map[string]string   // url -> summary ("" = nothing worth showing)
	linkPreviewPending   map[string]bool     // url -> fetch in flight
	linkPreviewDismissed map[previewKey]bool // previews backspaced away on this line

	// reserveLastColumn keeps the input area off the terminal's last column, so
	// the renderer never writes the cell whose pending-wrap state trips two mosh
	// bugs. Off by default, because it costs a visible column of typing room and
	// only mosh users need it; moshHintShown covers offering it to them. See
	// reservedColumns for why it works, and %debug lastcol.
	reserveLastColumn bool

	// mosh detection state, gathered once per session (never per reconnect).
	// See moshdetect.go: moshSDACertain is the terminal identifying itself as
	// mosh, moshPSFound the weaker "a mosh-server is running here".
	moshProbed     bool
	moshSDACertain bool
	moshPSFound    bool

	// shortenHintShown records that the M-s reminder has been printed, so it is
	// offered once a session rather than at every URL.
	shortenHintShown bool

	// shortOriginals maps a short URL back to the URL it was made from, so a
	// link this session shortened can be previewed by its destination rather
	// than by the shortener's own page. See previewTarget.
	shortOriginals map[string]string

	// shortenService names the shortener M-s uses, as chosen by %shorten. It is
	// held by name rather than as a urlshorten.Service so that the zero Model
	// resolves to the default instead of carrying a nil interface; "" means the
	// default.
	shortenService string

	// shortenPending holds the M-s requests currently out, so a second press on
	// a URL already being shortened does not mint a second short link for it.
	// Shared across value-copies for the same reason linkPreviewPending is: a
	// copy that could not see it would start the request again.
	shortenPending map[urlOccurrence]bool

	// renderEpoch versions the per-item render cache. It is bumped whenever
	// something that affects how items render changes: the window width, the
	// debug split (which halves the render width), or the whoami identity.
	// renderItem re-renders an item whose cacheEpoch doesn't match.
	renderEpoch int

	// seenLoopStarted records that the recurring 5-second ReportSeen loop
	// (seenTickMsg → reportSeenCmd → seenTickMsg …) has been started. It must
	// run at most once per process: each extra chain would live forever.
	seenLoopStarted bool

	// cursorReport is the terminal's own answer to a cursor-position query
	// (DSR/CPR), recorded so %debug snapshot can print where the terminal
	// thinks the cursor is rather than only where this app thinks it left it.
	cursorReport cursorReport

	// snapshotHardcopy holds GNU screen's dump of the live display for the
	// snapshot currently being assembled, captured before the pre-snapshot
	// repaint; snapshotHardcopyErr is why there is none.
	snapshotHardcopy    string
	snapshotHardcopyErr string

	// snapshotProbeFrame is the frame the model believed was on screen at the
	// moment the hardcopy was taken. Comparing the two is the measurement the
	// whole snapshot exists for, so they must describe the same instant — see
	// the pause in the %debug snapshot command.
	snapshotProbeFrame string

	// redrawOnShrink makes the input area shrinking (viewport growing) force a
	// full repaint, via forceRedraw below. Toggled by %debug redraw.
	//
	// ON by default, restored after being turned off to hunt the bug it hides.
	// A six-way matrix over the transport, the terminal and screen settled the
	// attribution: mosh is present in every reproduction and absent from every
	// clean run, while iTerm2 and Terminal.app both reproduce and screen turns
	// out to be irrelevant.
	//
	//   iTerm2       + mosh + screen   reproduces
	//   iTerm2       + mosh            reproduces
	//   Terminal.app + mosh + screen   reproduces
	//   Terminal.app + mosh            reproduces
	//   iTerm2       + ssh  + screen   clean
	//   iTerm2       + ssh             clean
	//
	// An instrumented re-investigation had concluded the opposite -- that
	// mosh's scroll heuristic fires but corrupts nothing. That conclusion came
	// from comparisons made through charmbracelet/x/vt, which does not
	// implement insert mode and so mis-renders the streams this app produces
	// (see display-bug-repro/README.md). It could not have detected a
	// difference it renders away, which is how mosh was wrongly cleared.
	//
	// The mechanism inside mosh is still open. Insert at a full line, by either
	// ICH or IRM, survives a mosh loopback intact, so it is something more
	// specific than character insertion alone. Turn this off with '%debug
	// redraw off' to observe the fault; the snapshot records which way it was
	// set, because a clean snapshot taken with it on proves nothing.
	redrawOnShrink bool

	// forceRedraw requests a full repaint (tea.ClearScreen) after the current
	// Update call, when redrawOnShrink is enabled and the input area has just
	// shrunk. Consumed and cleared by Update, which wraps update's normal Cmd
	// with tea.ClearScreen when this is set.
	forceRedraw bool

	// scrollAnchor is the output-item index to keep at the top of the viewport
	// across a width-changing resize (which rewraps and invalidates raw line
	// offsets). -1 means no anchor / use the raw offset. Set by the resize/debug
	// callers and consumed once by updateViewportSize.
	scrollAnchor int

	// Position restore
	storedLastSeenID     int64 // lastSeenID from proxy at startup, used to restore scroll position
	needsPositionRestore bool  // true until we have window size to set scroll position

	// Reconnect prompt: shown when the Lily connection drops.
	reconnectPrompt bool

	// Authentication dialog
	authMode       bool
	authError      string
	authUsername   string
	authPassword   string
	authField      int // 0=username, 1=password
	usernameInput  textarea.Model
	passwordInput  textarea.Model
	authenticated  bool // true after auth succeeds
	authInProgress bool // true while attempting auth

	// Intelligent expand state (mirrors tigerlily expand.pl)
	expandSender    string   // last person who private/emoted us  (recalled by ':')
	expandRecips    string   // last destination we sent to        (recalled by ';')
	expandSendgroup string   // group from last multi-recip private (recalled by '=')
	pastSends       []string // recent destinations, newest first (capped at pastSendsMax)

	// Completion popup state
	completionActive bool       // true when popup is visible
	completionList   list.Model // bubbles/list for selection
	completionToken  string     // the partial text being completed
	completionFore   string     // text before the token (to preserve)

	// In-TUI editor (info / memo)
	editMode bool
	editor   textarea.Model
	editMeta editMeta

	// Logging
	logChan <-chan logMsg // receives log messages to display

	// Diagnostics for %debug snapshot (snapshot.go). The rings are pointers
	// so recording survives the value-copies Model goes through on every
	// Update; the tap exposes the raw byte tail the renderer wrote.
	rendererTap RendererTap
	inputEvents *ring
	msgMeta     *ring

	// perf times how long each class of event takes to handle, bucketed by
	// wall-clock window so a snapshot shows whether responsiveness has been
	// degrading over the session's life (see perf.go). A pointer for the same
	// reason as the rings, and additionally because View — which records the
	// render timing — may run on a different goroutine than Update.
	perf *perfMetrics
}

// logMsg carries a severity level and text for display in the TUI output window.
// It is used both as a channel entry type and as a Bubble Tea message.
type logMsg struct {
	level string // "DEBUG", "INFO", "WARN", "ERROR"
	text  string
}

// slogHandler implements slog.Handler and forwards records to the TUI log channel.
type slogHandler struct {
	ch chan<- logMsg
}

func (h *slogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *slogHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value.Any())
		return true
	})
	h.ch <- logMsg{level: r.Level.String(), text: b.String()}
	return nil
}

func (h *slogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *slogHandler) WithGroup(_ string) slog.Handler      { return h }

// NewLogger creates a log channel and returns a slog.Logger that writes to the TUI.
func NewLogger() (chan logMsg, *slog.Logger) {
	ch := make(chan logMsg, 100)
	return ch, slog.New(&slogHandler{ch: ch})
}

// New creates a Model wired to the given proxy client.
// State and event history are fetched asynchronously in Init() so that SLCP prompts
// arriving during the login sync are visible and answerable in the TUI.
func New(c *client.Client, logChan <-chan logMsg, startupMsgs ...string) Model {
	logoLines := formatLogo()
	output := make([]OutputItem, 0, len(logoLines)+1+len(startupMsgs))
	for _, line := range logoLines {
		output = append(output, OutputItem{Type: "text", Data: line})
	}
	output = append(output, OutputItem{Type: "text", Data: ""})
	for _, msg := range startupMsgs {
		output = append(output, OutputItem{Type: "text", Data: msg})
	}

	// Create input textarea
	ti := textarea.New()
	ti.ShowLineNumbers = false
	ti.CharLimit = 0
	ti.Focus()
	ti.Prompt = ""
	ti.SetHeight(1)

	// Create auth textareas
	usernameField := textarea.New()
	usernameField.ShowLineNumbers = false
	usernameField.CharLimit = 0
	usernameField.Prompt = ""
	usernameField.SetHeight(1)
	usernameField.Focus()

	passwordField := textarea.New()
	passwordField.ShowLineNumbers = false
	passwordField.CharLimit = 0
	passwordField.Prompt = ""
	passwordField.SetHeight(1)

	return Model{
		client:            c,
		output:            output,
		input:             ti,
		keys:              NewKeyMap(),
		spellChecker:      NewSpellChecker(),
		logChan:           logChan,
		historyPos:        -1,
		searchIdx:         -1,
		autoPageAnchor:    -1,
		redrawOnShrink:    true,  // see the field's doc; %debug redraw off to hunt the bug
		reserveLastColumn: false, // off by default: costs a column. See reservedColumns.
		pagerEnabled:      true,
		linkPreviewOn:     true,
		scrollAnchor:      -1,
		renderEpoch:       1, // 1 so zero-valued item caches (epoch 0) read as stale
		authMode:          !c.HasToken(),
		authField:         0,
		usernameInput:     usernameField,
		passwordInput:     passwordField,
		inputEvents:       newRing(inputEventRingCap),
		msgMeta:           newRing(msgMetaRingCap),
		perf:              newPerfMetrics(),
		processedIDs:      newDedupSet(),

		linkPreviewCache:   make(map[string]string),
		linkPreviewPending: make(map[string]bool),
	}
}

// dedupCap bounds the processedIDs set. IDs further behind the newest one than
// this have fallen out of the scrollback (capped at maxScrollback items), so a
// re-delivery would no longer be a visible duplicate and their entries can be
// dropped. 2× leaves headroom for messages that carry an ID but never append
// an item (non-notify events, prompts).
const dedupCap = 2 * maxScrollback

// dedupSet tracks which message IDs have been incorporated. Held by pointer in
// Model (like the diagnostic rings) so updates survive value copies.
type dedupSet struct {
	seen  map[int64]bool
	maxID int64 // highest ID ever recorded; prune floor is maxID-dedupCap
}

func newDedupSet() *dedupSet { return &dedupSet{seen: make(map[int64]bool)} }

// seenID reports whether an ID has already been incorporated, without recording
// it. ID 0 (messages that carry none) is never "seen".
func (d *dedupSet) seenID(id int64) bool {
	return id != 0 && d.seen[id]
}

// alreadyProcessed reports whether the message carries an ID already
// incorporated into the model, recording it otherwise. Messages without an ID
// (prompts from some paths, keepalives) are never considered duplicates.
func (d *dedupSet) alreadyProcessed(msg *api.WSServerMsg) bool {
	if msg.ID == 0 {
		return false
	}
	if d.seen[msg.ID] {
		return true
	}
	d.seen[msg.ID] = true
	if msg.ID > d.maxID {
		d.maxID = msg.ID
	}
	// Prune rarely and in bulk: entries at least dedupCap behind the newest ID
	// protect content the scrollback no longer retains. The extra quarter of
	// slack keeps the O(n) sweep amortized — pruning exactly at the cap would
	// re-run it on every message once the window filled.
	if len(d.seen) > dedupCap+dedupCap/4 {
		for id := range d.seen {
			if id <= d.maxID-dedupCap {
				delete(d.seen, id)
			}
		}
	}
	return false
}

// Messages for async operations

type initialStateMsg struct {
	state  *api.StateResponse
	events []api.WSServerMsg
	err    error
}

type serverEventMsg struct{ msg *api.WSServerMsg }

type seenTickMsg struct{}

type authResultMsg struct {
	username string
	password string
	// token is the proxy session token the login returned. The model compares it
	// with the token it is already tracking to tell a fresh proxy session from a
	// re-attach to the existing one (see Model.sessionToken).
	token string
	// newClient is set when the result came from a reconnect (a fresh client was
	// created); the model swaps to it. It is nil for the initial login, which
	// authenticates the model's existing client in place.
	newClient *client.Client
	err       error
}

type editorFetchResultMsg struct {
	meta  editMeta
	lines []string
	err   error
}

type editorSaveResultMsg struct {
	meta editMeta
	err  error
}

// appendDebug appends lines to the debug-pane transcript, trimming the oldest
// entries beyond maxDebugMsgs so the transcript cannot grow without bound.
func (m *Model) appendDebug(lines ...string) {
	m.debugMsgs = append(m.debugMsgs, lines...)
	if over := len(m.debugMsgs) - maxDebugMsgs; over > 0 {
		// Copy to a fresh slice so the trimmed backing array can be collected.
		m.debugMsgs = append([]string(nil), m.debugMsgs[over:]...)
	}
}

// renderItem returns the rendered lines for output item i, re-rendering only
// when the cache is stale (renderEpoch changed, i.e. width/debug-split/whoami
// changed). All scrollback walks must use this rather than renderOutputItem so
// a long session doesn't re-render the entire buffer on every message.
func (m Model) renderItem(i int) []string {
	it := &m.output[i]
	if it.cacheEpoch != m.renderEpoch || it.cache == nil {
		it.cache = m.renderOutputItem(*it)
		it.cacheEpoch = m.renderEpoch
	}
	return it.cache
}

// fetchInitialStateCmd fetches state (blocking until the SLCP sync is done).
func fetchInitialStateCmd(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		state, err := c.FetchState()
		if err != nil {
			return initialStateMsg{err: err}
		}
		var events []api.WSServerMsg
		if state.LastSeenID > 0 {
			afterID := state.LastSeenID
			for {
				batch, more, err := c.FetchEvents(afterID, 200)
				if err != nil {
					break
				}
				events = append(events, batch...)
				if !more || len(batch) == 0 {
					break
				}
				afterID = batch[len(batch)-1].ID
			}
		}
		return initialStateMsg{state: state, events: events}
	}
}

// attemptAuthCmd authenticates with the proxy and fetches initial state.
func attemptAuthCmd(c *client.Client, username, password string) tea.Cmd {
	return func() tea.Msg {
		if err := c.Auth(username, password); err != nil {
			return authResultMsg{username: username, password: password, err: err}
		}
		if err := c.Connect(); err != nil {
			return authResultMsg{username: username, password: password, err: err}
		}
		return authResultMsg{username: username, password: password, token: c.Token(), err: nil}
	}
}

// listenCmd returns a Bubble Tea command that blocks on the next proxy event.
func listenCmd(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-c.Events
		if !ok {
			return serverEventMsg{nil}
		}
		return serverEventMsg{msg}
	}
}

// listenLogCmd returns a Bubble Tea command that blocks on the next log message.
func listenLogCmd(logChan <-chan logMsg) tea.Cmd {
	return func() tea.Msg {
		return <-logChan
	}
}

// reconnectCmd re-runs the login against the proxy using the stored credentials
// and yields an authResultMsg, so reconnection follows the exact same path as
// the initial login (state fetch, history replay, startup memo) — the only
// difference being that the user is not prompted for credentials unless the
// stored ones are rejected.
func reconnectCmd(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		nc, err := c.Reconnect()
		return authResultMsg{newClient: nc, token: nc.Token(), err: err}
	}
}

// reportSeenNow reports lastSeenID to the proxy immediately. It is a one-shot:
// it deliberately does NOT yield a seenTickMsg, because every seenTickMsg
// spawns a reportSeenCmd chain that lives forever — returning one here made
// every resize/reconnect add another eternal 5-second reporting loop.
func reportSeenNow(c *client.Client, lastSeenID int64) tea.Cmd {
	return func() tea.Msg {
		_ = c.ReportSeen(lastSeenID)
		return nil
	}
}

// reportSeenCmd waits 5 seconds, reports lastSeenID, then re-schedules.
func reportSeenCmd(c *client.Client, lastSeenID int64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(5 * time.Second)
		_ = c.ReportSeen(lastSeenID)
		return seenTickMsg{}
	}
}

// Init starts the event listener.
func (m Model) Init() tea.Cmd {
	if m.authMode {
		return nil
	}
	return tea.Batch(
		listenCmd(m.client),
		listenLogCmd(m.logChan),
		fetchInitialStateCmd(m.client),
	)
}

// Update handles a message and, if the input area shrank this cycle with
// %debug redraw enabled, appends a forced full repaint (see redrawOnShrink for
// what that works around and why it is off by default). The actual message
// handling lives in update; this wrapper exists only to give every return path
// in that large switch a single place to check the flag, rather than threading
// it through each one individually.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	start := time.Now()
	newModel, cmd := m.update(msg)
	nm := newModel.(Model)
	// Time the whole handling of this message, then take a gauge sample if one
	// is due. Sampling after recording keeps its (rate-limited) cost out of the
	// latency it would otherwise inflate.
	done := time.Now()
	nm.perf.record(m.perfCategory(msg), done.Sub(start))
	if nm.perf.dueForSample(done) {
		nm.perf.sample(nm.perfGauge())
	}
	if nm.forceRedraw {
		nm.forceRedraw = false
		cmd = tea.Batch(cmd, tea.ClearScreen)
	}
	return nm, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.ResumeMsg:
		// Returning from suspend (C-z / fg). Bubbletea restores the terminal and
		// re-hides the cursor automatically; force a full clear so any artifacts
		// left by the foreground process are wiped and the UI repaints cleanly.
		m.recordEvent("resume")
		return m, tea.ClearScreen

	case tea.WindowSizeMsg:
		m.recordEvent("resize %dx%d (was %dx%d)", msg.Width, msg.Height, m.width, m.height)
		// A width change rewraps the output, so the raw scroll offset no longer
		// maps to the same message. Anchor on the item currently at the top
		// (computed at the OLD width, before we mutate m.width) so the view stays
		// put instead of jumping — e.g. when re-attaching screen from a
		// different-sized terminal. Skip on the first size (m.width == 0): there
		// is nothing to preserve and rendering at width 0 is meaningless.
		if msg.Width != m.width {
			if m.width > 0 {
				m.scrollAnchor = m.topVisibleItemIndex()
			}
			// The anchor above is computed from caches at the old width; only
			// after that may the new width invalidate them.
			m.renderEpoch++
		}
		m.width = msg.Width
		m.height = msg.Height
		m = m.updateViewportSize()
		if m.editMode {
			m.editor.SetWidth(m.width)
			m.editor.SetHeight(m.height - 2)
		}
		if m.needsPositionRestore {
			m.restorePosition()
		}
		m.advanceLastSeenID()
		return m, reportSeenNow(m.client, m.lastSeenID)

	case shortenResultMsg:
		// A shorten rewrites the input line, so the textarea is resynced and the
		// input area re-measured: the replacement is a different length than
		// what it replaced and may free up or claim a wrapped line.
		m = m.applyShortenResult(msg)
		m.syncTextarea()
		m = m.maybeResizeViewport()
		// The line now holds a URL it did not a moment ago. Ask for its preview
		// the same way typing one would — force, because nothing follows the
		// short URL to mark it finished, and it is as finished as it will get.
		m, cmds := m.previewCmds(true)
		return m, tea.Batch(cmds...)

	case linkPreviewResultMsg:
		// A preview landing does not touch inputValue, so nothing about the
		// message being composed changes — but the input area may now need an
		// extra line for the ghost text, so the viewport is re-measured.
		m = m.applyPreviewResult(msg)
		m = m.maybeResizeViewport()
		return m, nil

	case tea.KeyPressMsg:
		m.recordKeyEvent(msg)
		if m.debugKeys {
			m.appendDebug(fmt.Sprintf("KEY: %s", msg.String()))
		}

		if m.authMode {
			return m.handleAuthKey(msg)
		}

		if m.editMode {
			return m.handleEditorMsg(msg)
		}

		if m.reconnectPrompt {
			return m.handleReconnectKey(msg)
		}

		if m.searchMode {
			return m.handleSearchKey(msg)
		}

		return m.handleNormalKey(msg)

	case tea.PasteMsg:
		// v2 delivers bracketed pastes as a distinct message instead of a
		// multi-rune KeyMsg; route it by mode (see handlePaste).
		m.recordEvent("paste len=%d mode=%s", len(msg.Content), m.modeName())
		return m.handlePaste(msg)

	case tea.MouseClickMsg:
		m.recordEvent("click button=%d at=%d,%d", msg.Button, msg.X, msg.Y)
		// Left-click in the input area moves the cursor to that cell. Like the
		// wheel, this needs the terminal to report mouse events, which is only
		// requested while m.mouseEnabled is set (%mouse on / M-m) — see
		// mouseOnNotice for why that is off by default. Auth and edit modes
		// own the whole screen and have their own input widgets, so the input
		// area's geometry does not apply there.
		if !m.mouseEnabled || m.authMode || m.editMode || m.reconnectPrompt ||
			msg.Button != tea.MouseLeft {
			return m, nil
		}
		// The completion popup owns the cursor position while it is open; let
		// the click dismiss it first so the move applies to the real input.
		if m.completionActive {
			m = m.hideCompletionPopup()
		}
		moved, ok := m.handleInputClick(msg.X, msg.Y)
		if !ok {
			return m, nil
		}
		m = moved
		// Leaving an incremental search swaps the search prompt back out, which
		// changes the first line's width and so can change the input height.
		m = m.maybeResizeViewport()
		m.armPagerIfAtBottom()
		return m, nil

	case tea.MouseWheelMsg:
		m.recordEvent("wheel button=%d at=%d,%d", msg.Button, msg.X, msg.Y)
		// Only the output viewport reacts to the wheel; auth/edit modes own the
		// screen and have no scrollback to page through. mouseEnabled gates the
		// whole feature (off by default; toggled via %mouse or M-m, which flip
		// m.mouseEnabled — View() reports the matching MouseMode declaratively).
		if !m.mouseEnabled || m.authMode || m.editMode {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.autoPageAnchor = -1
			if m.debugMode {
				m.debugViewport.ScrollUp(mouseWheelLines)
			} else {
				m.viewport.ScrollUp(mouseWheelLines)
			}
		case tea.MouseWheelDown:
			m.autoPageAnchor = -1
			if m.debugMode {
				m.debugViewport.ScrollDown(mouseWheelLines)
			} else {
				m.viewport.ScrollDown(mouseWheelLines)
				m.advanceLastSeenID()
			}
		}
		m.armPagerIfAtBottom()
		return m, nil

	case authResultMsg:
		// A reconnect creates a fresh client; adopt it (success or failure, so a
		// credential re-prompt can retry on it).
		if msg.newClient != nil {
			m.client = msg.newClient
		}
		if msg.err != nil {
			m.authInProgress = false
			// Re-prompt for credentials when they were rejected, or whenever the
			// initial credential dialog is already showing. A non-auth failure
			// during a reconnect (proxy unreachable, ws error) instead offers a
			// plain retry without re-typing credentials.
			if m.authMode || errors.Is(msg.err, client.ErrAuthFailed) {
				m.authMode = true
				m.reconnectPrompt = false
				m.authError = msg.err.Error()
				m.authPassword = ""
				m.passwordInput.SetValue("")
				m.usernameInput.SetValue(m.authUsername)
				m.authField = 1
				m.usernameInput.Blur()
				m.passwordInput.Focus()
				return m, nil
			}
			m.reconnectPrompt = true
			m.output = append(m.output, OutputItem{Type: "error", Data: "reconnect failed: " + msg.err.Error()})
			m = m.syncViewportContent()
			return m, nil
		}
		m.authMode = false
		m.authenticated = true
		m.authInProgress = false
		m.reconnectPrompt = false
		// A reconnect hands back a brand-new proxy session (the old one is torn
		// down the moment the Lily socket closes), and its message IDs start
		// over from 1. Adopting its ID space is what lets the login output
		// through; without it the blurb and review prompts read as duplicates of
		// the dead session's traffic and the reconnect looks like it hung.
		if msg.token != m.sessionToken {
			m.sessionToken = msg.token
			m.resetSessionIDs()
		}
		// Run the identical post-login sequence (state fetch, history replay,
		// startup memo) for both initial login and reconnect.
		cmds := []tea.Cmd{
			listenCmd(m.client),
			listenLogCmd(m.logChan),
			fetchInitialStateCmd(m.client),
		}
		// Start the recurring ReportSeen loop exactly once: the chain never
		// terminates (and always reads the current m.client), so starting
		// another on every reconnect would accumulate loops forever.
		if !m.seenLoopStarted {
			m.seenLoopStarted = true
			cmds = append(cmds, reportSeenCmd(m.client, 0))
		}
		// Look for mosh once per session, not once per reconnect.
		if !m.moshProbed {
			m.moshProbed = true
			cmds = append(cmds, detectMoshCmds()...)
		}
		return m, tea.Batch(cmds...)

	case moshPSMsg:
		m.moshPSFound = msg.found
		return m, nil

	case uv.SecondaryDeviceAttributesEvent:
		// Unsolicited replies are possible (a terminal answering something else
		// we sent), so this only ever sets the flag, never clears it.
		if isMoshSDA(msg) {
			m.moshSDACertain = true
		}
		return m, nil

	case moshDecideMsg:
		// Say nothing if the workaround is already on: someone who turned it on,
		// here or while the probes were in flight, does not need telling.
		if lines := moshHintLines(m.moshSDACertain, m.moshPSFound); lines != nil && !m.reserveLastColumn {
			m.output = append(m.output, OutputItem{Type: "command", Data: lines})
			m = m.syncViewportContent()
		}
		return m, nil

	case serverEventMsg:
		if msg.msg == nil {
			m.reconnectPrompt = true
			return m, nil
		}

		// Only collect the JSON transcript while the debug pane is open: the
		// pretty-printed copy of every message is expensive to produce and, if
		// collected unconditionally, retains (a multiple of) all traffic ever
		// received for the life of the session.
		if m.debugMode {
			if jsonBytes, err := json.MarshalIndent(msg.msg, "", "  "); err == nil {
				m.appendDebug("RECV:")
				m.appendDebug(strings.Split(string(jsonBytes), "\n")...)
			}
		}

		// Drop messages the history replay already incorporated: events that
		// reach the proxy while GET /events is being served come back in the
		// replay and on the live stream.
		if m.processedIDs.alreadyProcessed(msg.msg) {
			m.recordMsgMeta("drop dup type=%s id=%d", msg.msg.Type, msg.msg.ID)
			if m.reconnectPrompt {
				return m, nil
			}
			return m, listenCmd(m.client)
		}

		var proxyCmd tea.Cmd
		m, proxyCmd = m.handleProxy(msg.msg)
		m.advanceLastSeenID()
		m = m.syncViewportContent()

		if m.reconnectPrompt {
			return m, nil
		}
		return m, tea.Batch(proxyCmd, listenCmd(m.client))

	case logMsg:
		if msg.level == "DEBUG" {
			m.appendDebug(msg.text)
		} else {
			m.output = append(m.output, OutputItem{Type: "log", Data: msg})
			m = m.syncViewportContent()
		}
		return m, listenLogCmd(m.logChan)

	case seenTickMsg:
		return m, reportSeenCmd(m.client, m.lastSeenID)

	case snapshotCaptureMsg:
		return m, m.captureSnapshot(msg.path)

	case snapshotProbeMsg:
		// The terminal has caught up with the frame this command caused. Record
		// what we believe is showing, then ask screen what is actually showing.
		m.snapshotProbeFrame = m.viewContent()
		return m, hardcopyCmd(msg.path)

	case snapshotHardcopyMsg:
		// The terminal has been measured; now repaint and capture our own side.
		m.snapshotHardcopy = msg.text
		if msg.err != nil {
			m.snapshotHardcopyErr = msg.err.Error()
		}
		return m, tea.Sequence(
			func() tea.Msg { return tea.ClearScreen() },
			tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
				return snapshotCaptureMsg{path: msg.path}
			}),
		)

	case tea.CursorPositionMsg:
		// Answer to a cursor-position query. Only %debug snapshot asks, so this
		// is cheap to keep around and is read while assembling the snapshot.
		m.cursorReport = cursorReport{x: msg.X, y: msg.Y, at: time.Now(), ok: true}
		return m, nil

	case snapshotResultMsg:
		if msg.err != nil {
			m.output = append(m.output, OutputItem{Type: "error",
				Data: "debug snapshot failed: " + msg.err.Error()})
		} else {
			m.output = append(m.output, OutputItem{Type: "text",
				Data: "Debug snapshot written: " + msg.path})
		}
		m = m.syncViewportContent()
		return m, nil

	case initialStateMsg:
		if msg.err != nil {
			m.output = append(m.output, OutputItem{Type: "error", Data: "state: " + msg.err.Error()})
			m = m.syncViewportContent()
			return m, nil
		}
		// Whoami is only populated once the Lily sync completes (which can be
		// gated behind interactive login prompts). If the state fetch returned
		// before that (the safety-net timeout), don't overwrite good prior state
		// or print an empty "Connected to … ()" line.
		if msg.state != nil && msg.state.Whoami != "" {
			m.state = msg.state
			// Whoami feeds event rendering (own-message styling); cached lines
			// rendered before it was known are stale.
			m.renderEpoch++
		}
		if m.state != nil && m.state.Whoami != "" {
			displayName := m.state.Whoami
			for _, e := range m.state.Entities {
				if e.Handle == m.state.Whoami && e.Kind == "user" {
					displayName = e.Name
					break
				}
			}
			connLine := "Connected to " + m.state.Server + " as " +
				privateSenderStyle.Render(displayName) + " (" + m.state.Whoami + ")"
			m.output = append(m.output, OutputItem{Type: "text", Data: connLine})
			m.output = append(m.output, OutputItem{Type: "text", Data: ""})
		}
		if len(msg.events) > 0 {
			slog.Info(fmt.Sprintf("loaded %d events from history (proxy buffer: %d)", len(msg.events), msg.state.EventBufSize))
		}
		// The replay below drops prompt events wholesale, so Lily's pending
		// prompt comes from the /state snapshot instead — but only one this
		// client never received live. A prompt it did receive live is already
		// applied (if still unanswered) or was deliberately cleared by
		// submitLine (if answered), and re-applying that one is exactly the
		// stale "asked me twice" prompt left standing in the input area.
		//
		// This must precede the replay: alreadyProcessed records each ID as it
		// goes, so after the loop every replayed ID — including the prompt's —
		// reads as seen whether or not it ever arrived live.
		if msg.state.Prompt != "" && !m.processedIDs.seenID(msg.state.PromptID) {
			m.recordMsgMeta("apply state prompt id=%d", msg.state.PromptID)
			m.prompt = msg.state.Prompt
		}

		var replayCmds []tea.Cmd
		for i := range msg.events {
			// Skip events the live WebSocket stream already delivered. The
			// replay range starts at the proxy's stored last-seen ID, which
			// during a reattach lags behind the login output (detach review,
			// prompts) the socket has been streaming while the state fetch
			// blocked on the SLCP sync — replaying those duplicated the review.
			if m.processedIDs.alreadyProcessed(&msg.events[i]) {
				m.recordMsgMeta("skip replayed type=%s id=%d", msg.events[i].Type, msg.events[i].ID)
				continue
			}
			// A prompt is live interactive state; one arriving via history was
			// answered (or superseded) before the replay ran, and re-applying
			// it would resurrect a stale prompt in the input area. Dropping
			// every replayed prompt is safe because a prompt that is genuinely
			// still pending was taken from the /state snapshot above.
			if msg.events[i].Type == "prompt" {
				m.recordMsgMeta("skip replayed prompt id=%d", msg.events[i].ID)
				continue
			}
			var cmd tea.Cmd
			m, cmd = m.handleProxy(&msg.events[i])
			if cmd != nil {
				replayCmds = append(replayCmds, cmd)
			}
		}
		m.storedLastSeenID = msg.state.LastSeenID
		m = m.syncViewportContent()
		if m.storedLastSeenID > 0 {
			// Restore scroll to the stored last-seen position. If we already know
			// the window size (the usual ordering — bubbletea sends the initial
			// WindowSizeMsg before auth+state finish), do it now. Otherwise defer
			// to the first WindowSizeMsg. Restoring here (rather than always
			// deferring) keeps needsPositionRestore from lingering until the user's
			// first manual resize, where it would override the resize anchor and
			// yank the viewport to a stale position.
			if m.viewport.Height() > 0 {
				m.restorePosition()
			} else {
				m.needsPositionRestore = true
			}
		}
		// The zlilyStartup memo is fetched and replayed by the proxy now; any
		// client-only commands it contains arrive as "clientcommand" events.
		replayCmds = append(replayCmds, reportSeenNow(m.client, m.lastSeenID))
		return m, tea.Batch(replayCmds...)

	case editorFetchResultMsg:
		content := strings.Join(msg.lines, "\n")
		m.editMeta = msg.meta
		m.editor = newEditorModel(m.width, m.height-2, content)
		m.editMode = true
		return m, nil

	case editorSaveResultMsg:
		m.editMode = false
		if msg.err != nil {
			m.output = append(m.output, OutputItem{Type: "error", Data: msg.err.Error()})
		} else {
			var saved string
			switch msg.meta.contentType {
			case "info":
				saved = "(info saved)"
			case "memo":
				saved = "(memo \"" + msg.meta.name + "\" saved)"
			}
			m.output = append(m.output, OutputItem{Type: "text", Data: saved})
		}
		m = m.syncViewportContent()
		return m, nil
	}

	return m, tea.Batch(cmds...)
}

// updateViewportSize recalculates viewport dimensions based on window size.
func (m Model) updateViewportSize() Model {
	inputHeight := m.calculateInputHeight()
	viewportHeight := m.height - 1 - inputHeight // -1 for status bar
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	viewportWidth := m.width
	if m.debugMode {
		viewportWidth = m.width / 2
	}

	// Preserve scroll state
	wasAtBottom := m.viewport.AtBottom()
	oldYOffset := m.viewport.YOffset()

	// Resize in place rather than replacing the view. A height change (the
	// common case: the input area grew or shrank by a line) leaves every
	// rendered line still valid, so keeping the content spares the rebuild
	// that a fresh view would force. A width change invalidates the lines
	// themselves, but that path has already bumped renderEpoch, which makes
	// the sync below rebuild them anyway.
	m.viewport.SetWidth(viewportWidth)
	m.viewport.SetHeight(viewportHeight)

	if m.debugMode {
		debugWasAtBottom := m.debugViewport.AtBottom()
		debugOldYOffset := m.debugViewport.YOffset()
		m.debugViewport.SetWidth(m.width - viewportWidth - 1)
		m.debugViewport.SetHeight(viewportHeight)
		// Sync debug content and restore position
		m.debugViewport.SetContent(strings.Join(m.debugMsgs, "\n"))
		if debugWasAtBottom {
			m.debugViewport.GotoBottom()
		} else {
			m.debugViewport.SetYOffset(debugOldYOffset)
		}
	}

	m.input.SetWidth(m.width)
	m.input.SetHeight(inputHeight)

	m = m.syncViewportContent()

	// Restore scroll position after content sync.
	switch {
	case wasAtBottom:
		m.viewport.GotoBottom()
	case m.scrollAnchor >= 0:
		// Width changed: re-anchor on the item that was at the top, recomputed at
		// the new width so the same message stays in view instead of jumping.
		off := m.itemStartLine(m.scrollAnchor)
		if max := m.viewport.TotalLineCount() - m.viewport.Height(); off > max {
			off = max
		}
		if off < 0 {
			off = 0
		}
		m.viewport.SetYOffset(off)
	default:
		m.viewport.SetYOffset(oldYOffset)
	}
	m.scrollAnchor = -1

	return m
}

// topVisibleItemIndex returns the index of the output item occupying the top
// visible line of the viewport (viewport.YOffset), measured at the current width.
// Returns 0 when there is no output.
func (m Model) topVisibleItemIndex() int {
	target := m.viewport.YOffset()
	lineCount := 0
	for i := range m.output {
		lineCount += len(m.renderItem(i))
		if lineCount > target {
			return i
		}
	}
	if len(m.output) == 0 {
		return 0
	}
	return len(m.output) - 1
}

// itemStartLine returns the number of rendered lines before output item idx,
// measured at the current width.
func (m Model) itemStartLine(idx int) int {
	lineCount := 0
	for i := 0; i < idx && i < len(m.output); i++ {
		lineCount += len(m.renderItem(i))
	}
	return lineCount
}

// calculateInputHeight returns the number of lines needed for input area.
func (m Model) calculateInputHeight() int {
	maxInputLines := m.height / 2
	if maxInputLines < 1 {
		maxInputLines = 1
	}

	lines := m.inputTotalLines()
	if lines > maxInputLines {
		lines = maxInputLines
	}
	if lines < 1 {
		lines = 1
	}
	return lines
}

// inputPromptDisplayWidth returns the display width of the prompt.
func (m Model) inputPromptDisplayWidth() int {
	promptText := m.inputPromptText()
	if promptText == "" {
		return 0
	}
	return len(promptText) + 1 // +1 for space after prompt
}

// reservedColumns is how many columns at the right edge the input area leaves
// empty, over and above the cell it already reserves for the cursor.
//
// Zero unless the mosh workaround is on, since it costs a visible column and
// exists to sidestep two mosh bugs rather than for any layout reason. Both need
// something written to the terminal's last column:
// writing there leaves the cursor on it with a wrap pending, and mosh mishandles
// that state in two ways — an ANSI mode change discards the pending wrap
// (mosh's SM/RM registration), and its frame generator ends up believing the
// cursor is one past the last column and emits a backspace that lands a cell
// early (Display::new_frame / append_move). The input line is where this bites,
// because it sits on the bottom row, which is the one row mosh's put_row does
// not re-anchor with a CR/LF afterwards.
//
// Keeping the input area off the last column means the renderer never writes
// that cell. Trailing blanks it emits as an erase-to-end-of-line, which leaves
// no wrap pending; the one thing it does write out at an explicit column is the
// virtual cursor cell, and that is inside the input area's width too (see the
// +1 in inputTotalLines). So neither bug has anything to trigger on. That is
// worth a column: it
// prevents the corruption rather than repainting over it afterwards, which is
// all redrawOnShrink can do. Both patches are filed upstream
// (display-bug-repro/upstream); this is what protects users running a mosh that
// does not have them yet, which will be most of them for a long time.
//
// It is off by default: most connections are not mosh, and the empty column is
// noticeable. When a mosh-server turns up on the machine at login we suggest
// turning it on (see detectMoshCmd), and '%debug lastcol on' does that.
func (m Model) reservedColumns() int {
	if m.reserveLastColumn {
		return 1
	}
	return 0
}

// inputFirstLineWidth returns columns available for input on line 0 (after prompt).
func (m Model) inputFirstLineWidth() int {
	w := m.width - m.inputPromptDisplayWidth() - m.reservedColumns()
	if w < 1 {
		w = 1
	}
	return w
}

// inputLineStart returns the byte offset of inputValue where display line k of
// the input area begins. Line 0 holds the bytes that fit beside the prompt;
// every later line spans a full terminal width. The input area wraps by byte
// offset rather than display width, so these are the same units the renderer,
// the height calculation, and the hit test all work in.
func (m Model) inputLineStart(k int) int {
	if k <= 0 {
		return 0
	}
	return m.inputFirstLineWidth() + (k-1)*m.inputWrapWidth()
}

// inputLineEnd returns the exclusive byte offset where display line k ends,
// clamped to the length of the input.
func (m Model) inputLineEnd(k int) int {
	end := m.inputFirstLineWidth()
	if k > 0 {
		end += k * m.inputWrapWidth()
	}
	if n := m.inputDisplayLen(); end > n {
		end = n
	}
	return end
}

// inputDisplayLen returns the byte length of the input line as drawn, including
// any preview text woven into it. The input area's geometry is all in these
// display bytes, so a preview wraps and grows the area like typed text.
func (m Model) inputDisplayLen() int {
	n := len(m.inputValue)
	for _, g := range m.ghosts() {
		n += len(g.text)
	}
	return n
}

// inputWrapWidth returns the columns available on continuation lines (2..n),
// which carry no prompt and so span the terminal width less reservedColumns.
func (m Model) inputWrapWidth() int {
	w := m.width - m.reservedColumns()
	if w < 1 {
		w = 1
	}
	return w
}

// inputTotalLines returns total display lines needed for input, including cursor.
func (m Model) inputTotalLines() int {
	if m.width <= 0 {
		return 1
	}
	firstWidth := m.inputFirstLineWidth()
	n := m.inputDisplayLen() + 1 // +1 reserves cell for cursor
	if n <= firstWidth {
		return 1
	}
	rw := m.inputWrapWidth()
	return 1 + (n-firstWidth+rw-1)/rw
}

// syncViewportContent updates viewport with rendered output.
func (m Model) syncViewportContent() Model {
	start := time.Now()
	defer func() { m.perf.record(perfSync, time.Since(start)) }()

	// Capture follow state before trimming/SetContent: adding lines leaves
	// YOffset unchanged, so AtBottom() would read false afterwards even if we
	// were following the bottom a moment ago.
	wasAtBottom := m.viewport.AtBottom()
	prevLines := m.viewport.TotalLineCount()

	// Trim scrollback beyond the cap, shifting the scroll state up by the
	// dropped lines so the view (and the pager anchor) stays on the same
	// content instead of jumping.
	if over := len(m.output) - maxScrollback; over > scrollbackSlack {
		dropped := 0
		for i := 0; i < over; i++ {
			dropped += len(m.renderItem(i))
		}
		// Copy to a fresh slice so the trimmed items can be collected.
		m.output = append([]OutputItem(nil), m.output[over:]...)
		m.syncedItems = max(m.syncedItems-over, 0)
		prevLines -= dropped
		if prevLines < 0 {
			prevLines = 0
		}
		// TrimTop drops the same lines from the view and carries the offset up
		// with them; anything a rebuild below would redo is harmless.
		m.viewport.TrimTop(dropped)
		if m.autoPageAnchor >= 0 {
			m.autoPageAnchor -= dropped
			if m.autoPageAnchor < 0 {
				m.autoPageAnchor = 0
			}
		}
	}

	// Append only what the view has not seen. Rebuilding every line on every
	// message is what made a long session sluggish: the cost grew with the
	// scrollback, so each incoming message occupied the update loop for longer
	// and keystrokes waited behind it (docs/responsiveness-findings.md).
	if m.syncedEpoch != m.renderEpoch || m.syncedItems > len(m.output) {
		var lines []string
		for i := range m.output {
			lines = append(lines, m.renderItem(i)...)
		}
		m.viewport.SetLines(lines)
		m.syncedEpoch = m.renderEpoch
	} else if m.syncedItems < len(m.output) {
		var lines []string
		for i := m.syncedItems; i < len(m.output); i++ {
			lines = append(lines, m.renderItem(i)...)
		}
		m.viewport.AppendLines(lines)
	}
	m.syncedItems = len(m.output)

	totalLines := m.viewport.TotalLineCount()

	// Auto-paging: after user sends input, scroll to show up to one page of new output
	if m.autoPageAnchor >= 0 {
		newLines := totalLines - m.autoPageAnchor
		if !m.pagerEnabled || newLines <= m.viewport.Height() {
			// Pager off, or new output fits in one page - show it all
			m.viewport.GotoBottom()
		} else {
			// More than one page of new output - show one page past anchor, then stop
			targetOffset := m.autoPageAnchor
			if targetOffset > totalLines-m.viewport.Height() {
				targetOffset = totalLines - m.viewport.Height()
			}
			m.viewport.SetYOffset(targetOffset)
			m.autoPageAnchor = -1 // disable further auto-paging until next user input
		}
	} else if wasAtBottom || totalLines <= m.viewport.Height() {
		// We were following the bottom. Keep following for incremental output, but
		// if a whole page or more arrives at once, show one page from where we were
		// and pause the pager so the new output isn't scrolled past unseen.
		newLines := totalLines - prevLines
		if !m.pagerEnabled || newLines <= m.viewport.Height() || m.viewport.Height() <= 0 {
			m.viewport.GotoBottom()
		} else {
			targetOffset := prevLines
			if targetOffset > totalLines-m.viewport.Height() {
				targetOffset = totalLines - m.viewport.Height()
			}
			m.viewport.SetYOffset(targetOffset)
		}
	}

	if m.debugMode {
		m.debugViewport.SetContent(strings.Join(m.debugMsgs, "\n"))
		m.debugViewport.GotoBottom()
	}

	return m
}

// handleProxy incorporates a proxy message into the model. It may return a
// tea.Cmd (e.g. when a forwarded clientcommand toggles mouse-wheel mode).
func (m Model) handleProxy(msg *api.WSServerMsg) (Model, tea.Cmd) {
	m.recordMsgMeta("recv type=%s id=%d", msg.Type, msg.ID)
	var cmd tea.Cmd
	switch msg.Type {
	case "text":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			if text, ok := d["text"].(string); ok {
				m.output = append(m.output, OutputItem{Type: "text", Data: text, ID: msg.ID})
			}
		}

	case "commandresult":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			if linesRaw, ok := d["lines"].([]interface{}); ok {
				lines := make([]string, 0, len(linesRaw))
				for _, lineRaw := range linesRaw {
					if line, ok := lineRaw.(string); ok {
						lines = append(lines, line)
					}
				}
				m.output = append(m.output, OutputItem{Type: "command", Data: lines, ID: msg.ID})
			}
		}

	case "event":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			event, _ := d["event"].(string)
			source, _ := d["source"].(string)
			notify, _ := d["notify"].(bool)

			if notify && (event != "unidle" || m.state == nil || source != m.state.Whoami) {
				m.output = append(m.output, OutputItem{Type: "event", Data: d, ID: msg.ID})
			}

			// Only private sends drive the ':' recall and the cursor-0 Tab
			// default. Emotes are always public (sent to a discussion), so they
			// must not hijack that state with whoever last emoted in public.
			if event == "private" {
				m = m.trackIncomingPrivate(d)
			}

			if m.state != nil && source == m.state.Whoami {
				for i := range m.state.Entities {
					if m.state.Entities[i].Handle == m.state.Whoami && m.state.Entities[i].Kind == "user" {
						switch event {
						case "rename":
							if value, ok := d["value"].(string); ok {
								m.state.Entities[i].Name = value
							}
						case "blurb":
							// value is absent (not just empty) when the blurb is
							// cleared, since the proxy serializes it with omitempty.
							// Assign unconditionally so clearing resets it to "".
							value, _ := d["value"].(string)
							m.state.Entities[i].Blurb = value
						case "here":
							m.state.Entities[i].State = "here"
						case "away":
							m.state.Entities[i].State = "away"
						}
						break
					}
				}
			}
		}

	case "clientcommand":
		// A client-only command the proxy forwarded for local execution (e.g.
		// %style/%spell/%page replayed from the zlilyStartup memo, or a command
		// this client doesn't own). Run it through the same local-command handler
		// as interactive input so behaviour matches, and report unknown commands.
		if d, ok := msg.Data.(map[string]interface{}); ok {
			if text, ok := d["text"].(string); ok && strings.TrimSpace(text) != "" {
				var out []string
				var recognized bool
				m, out, cmd, recognized = m.applyLocalCommand(text)
				if !recognized && out == nil {
					out = []string{"Unknown command: " + strings.Fields(text)[0]}
				}
				if out != nil {
					m.output = append(m.output, OutputItem{Type: "command", Data: out, ID: msg.ID})
				}
			}
		}

	case "prompt":
		if p, ok := msg.Data.(string); ok {
			m.prompt = p
		}

	case "error":
		if e, ok := msg.Data.(string); ok {
			m.output = append(m.output, OutputItem{Type: "error", Data: e, ID: msg.ID})
			if e == "lily connection closed" {
				m.reconnectPrompt = true
			}
		}
	}
	return m, cmd
}

// computeLastSeenID returns the highest ID among visible OutputItems.
func (m Model) computeLastSeenID() int64 {
	if m.height == 0 {
		return m.lastSeenID
	}

	// With viewport, we track what's visible
	visibleEnd := m.viewport.YOffset() + m.viewport.Height()
	lineCount := 0
	var maxID int64

	for i := range m.output {
		lineCount += len(m.renderItem(i))
		if lineCount > visibleEnd {
			break
		}
		if m.output[i].ID > maxID {
			maxID = m.output[i].ID
		}
	}
	return maxID
}

// advanceLastSeenID updates lastSeenID from the current viewport position.
func (m *Model) advanceLastSeenID() {
	if id := m.computeLastSeenID(); id > m.lastSeenID {
		m.lastSeenID = id
	}
}

// resetSessionIDs drops the message-ID bookkeeping that is only meaningful
// inside a single proxy session, and is called when the model adopts a new one.
// The proxy assigns IDs from a per-session counter that restarts at 1, so
// carrying the previous session's state across a reconnect breaks the new
// session two ways: processedIDs reports its opening messages — the "enter a
// blurb" and "review now?" prompts among them — as already-seen duplicates and
// drops them, and lastSeenID, sitting far above anything the new session has
// sent, reports the whole login as read. Scrollback itself survives the
// reconnect, but the IDs on those items are stale too: computeLastSeenID takes
// the maximum over the visible ones, so leaving them set would re-inflate
// lastSeenID on the very next message.
func (m *Model) resetSessionIDs() {
	m.processedIDs = newDedupSet()
	m.lastSeenID = 0
	m.storedLastSeenID = 0
	m.needsPositionRestore = false
	for i := range m.output {
		m.output[i].ID = 0
	}
}

// armPagerIfAtBottom (re)arms the auto-page anchor when the viewport is
// following the bottom. It runs after every user interaction (keys, wheel,
// blank-Enter pager advance), so the pager pauses once more than a page of
// output accumulates after the user's LAST interaction — not only after their
// last submitted line. Without this, catching up to the bottom with PgDn,
// blank Enter, or the wheel left the anchor disarmed, and output trickling in
// while the user was away followed the bottom forever instead of holding at
// -- MORE -- (and dragged lastSeenID along, so a reconnect restored to the
// bottom too).
func (m *Model) armPagerIfAtBottom() {
	if m.viewport.Height() > 0 && m.viewport.AtBottom() {
		m.autoPageAnchor = m.viewport.TotalLineCount()
	}
}

// restorePosition sets viewport scroll position from stored lastSeenID.
func (m *Model) restorePosition() {
	if m.viewport.Height() <= 0 {
		return
	}

	lineCount := 0
	for i := range m.output {
		lineCount += len(m.renderItem(i))
		if m.output[i].ID >= m.storedLastSeenID {
			break
		}
	}

	offset := lineCount - m.viewport.Height()
	if offset < 0 {
		offset = 0
	}
	m.viewport.SetYOffset(offset)
	m.lastSeenID = m.storedLastSeenID
	m.needsPositionRestore = false
}

// View renders the full TUI.
func (m Model) View() tea.View {
	start := time.Now()
	content := m.viewContent()
	m.perf.record(perfRender, time.Since(start))
	v := tea.NewView(content)
	v.AltScreen = true
	// The terminal cursor stays hidden (v.Cursor nil): every mode draws its
	// own virtual cursor inside the content string.
	if m.mouseEnabled && !m.authMode && !m.editMode {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// viewContent renders the whole frame as a styled string.
func (m Model) viewContent() string {
	if m.height == 0 {
		return "connecting..."
	}
	if m.authMode {
		return m.viewAuth()
	}
	if m.editMode {
		return m.viewEditor()
	}
	if m.debugMode {
		return m.viewWithDebug()
	}
	return m.viewNormal()
}

// viewNormal renders the standard UI (output + status + input).
func (m Model) viewNormal() string {
	var sb strings.Builder

	// Output viewport (potentially with completion popup overlay)
	if m.completionActive {
		sb.WriteString(m.renderViewportWithPopup())
	} else {
		sb.WriteString(m.viewport.View())
	}
	sb.WriteByte('\n')

	// Status bar
	sb.WriteString(m.formatStatusBar())
	sb.WriteByte('\n')

	// Input area with prompt
	sb.WriteString(m.renderInputArea())

	return sb.String()
}

// viewAuth renders the authentication dialog on top of the splash screen.
func (m Model) viewAuth() string {
	var sb strings.Builder

	// Render splash/logo from output
	splashLines := []string{}
	for _, item := range m.output {
		if s, ok := item.Data.(string); ok {
			splashLines = append(splashLines, s)
		}
	}

	// Show splash (limit to top half)
	maxSplashLines := m.height / 2
	if len(splashLines) > maxSplashLines {
		splashLines = splashLines[:maxSplashLines]
	}
	for _, line := range splashLines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	// Build auth dialog
	var dialogContent strings.Builder

	if m.authInProgress {
		// Show authenticating message
		dialogContent.WriteString("Authenticating...\n")
		dialogContent.WriteString("Please wait while we verify your credentials.")
	} else {
		cursorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow

		// Username field
		usernameVal := m.usernameInput.Value()
		if m.authField == 0 {
			dialogContent.WriteString("Username: " + usernameVal + cursorStyle.Render("▌") + "\n")
		} else {
			dialogContent.WriteString("Username: " + usernameVal + "\n")
		}

		// Password field
		maskedPass := strings.Repeat("•", utf8.RuneCountInString(m.passwordInput.Value()))
		dialogContent.WriteString("Password: " + maskedPass)
		if m.authField == 1 {
			dialogContent.WriteString(cursorStyle.Render("▌"))
		}
		dialogContent.WriteString("\n")

		if m.authError != "" {
			errorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
			dialogContent.WriteString("\n" + errorStyle.Render("Error: "+m.authError) + "\n")
		}

		dialogContent.WriteString("\nTab: switch | Enter: submit")

		if m.quitPending {
			dialogContent.WriteString("\n\nPress C-c again to exit.")
		}
	}

	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")). // cyan
		Padding(1).
		Width(40)

	dialog := dialogStyle.Render(dialogContent.String())

	// Center dialog horizontally
	dialogLines := strings.Split(dialog, "\n")
	for _, line := range dialogLines {
		lineWidth := lipgloss.Width(line)
		padding := (m.width - lineWidth) / 2
		if padding < 0 {
			padding = 0
		}
		sb.WriteString(strings.Repeat(" ", padding))
		sb.WriteString(line)
		sb.WriteByte('\n')
	}

	// Pad to m.height
	currentLines := strings.Count(sb.String(), "\n")
	for currentLines < m.height {
		sb.WriteByte('\n')
		currentLines++
	}

	return sb.String()
}

// renderViewportWithPopup renders the viewport with the completion popup at the bottom.
func (m Model) renderViewportWithPopup() string {
	viewportLines := strings.Split(m.viewport.View(), "\n")
	popup := m.renderCompletionPopup()
	popupLines := strings.Split(popup, "\n")

	popupHeight := len(popupLines)

	// Keep viewport lines that aren't covered by popup
	keepLines := len(viewportLines) - popupHeight
	if keepLines < 0 {
		keepLines = 0
	}

	var result []string
	result = append(result, viewportLines[:keepLines]...)
	result = append(result, popupLines...)

	// Ensure we have the right number of lines
	for len(result) < m.viewport.Height() {
		result = append(result, "")
	}

	return strings.Join(result[:m.viewport.Height()], "\n")
}

// viewWithDebug renders split view: left=output, right=debug JSON.
func (m Model) viewWithDebug() string {
	leftWidth := m.width / 2
	rightWidth := m.width - leftWidth - 1

	leftLines := strings.Split(m.viewport.View(), "\n")
	rightLines := strings.Split(m.debugViewport.View(), "\n")

	var sb strings.Builder
	for i := 0; i < m.viewport.Height(); i++ {
		left := ""
		if i < len(leftLines) {
			left = leftLines[i]
		}
		left = truncateAndPad(left, leftWidth)
		sb.WriteString(left)
		sb.WriteString("│")

		right := ""
		if i < len(rightLines) {
			right = rightLines[i]
		}
		right = truncateAndPad(right, rightWidth)
		sb.WriteString(right)
		sb.WriteByte('\n')
	}

	sb.WriteString(m.formatStatusBar())
	sb.WriteByte('\n')
	sb.WriteString(m.renderInputArea())

	return sb.String()
}

// formatStatusBar creates the status bar.
func (m Model) formatStatusBar() string {
	left := ""
	if m.state != nil && m.state.Whoami != "" {
		for _, e := range m.state.Entities {
			if e.Handle == m.state.Whoami && e.Kind == "user" {
				left = e.Name
				if e.Blurb != "" {
					left += " [" + e.Blurb + "]"
				}
				break
			}
		}
		if left == "" {
			left = m.state.Whoami
		}
	}

	right := ""
	if m.state != nil {
		server := m.state.Server
		if server == "" {
			server = "unknown"
		}

		userState := ""
		for _, e := range m.state.Entities {
			if e.Handle == m.state.Whoami && e.Kind == "user" {
				userState = e.State
				break
			}
		}
		if userState == "" {
			userState = "here"
		}

		now := time.Now()
		timeStr := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())

		// [M] marks mouse mode as active, so the reason click-drag selection
		// is not behaving is visible rather than something to remember.
		mouse := ""
		if m.mouseEnabled {
			mouse = "[M] | "
		}

		right = server + " | " + userState + " | " + mouse + timeStr
	}

	// MORE indicator centered when not at bottom
	center := ""
	if !m.viewport.AtBottom() && m.viewport.TotalLineCount() > m.viewport.Height() {
		moreCount := m.viewport.TotalLineCount() - m.viewport.YOffset() - m.viewport.Height()
		if moreCount > 0 {
			center = fmt.Sprintf("-- MORE (%d) --", moreCount)
		}
	}

	// Build status bar with left, center, right
	leftLen := len(left)
	centerLen := len(center)
	rightLen := len(right)

	// Calculate padding
	totalContent := leftLen + centerLen + rightLen
	if totalContent >= m.width {
		// Not enough space - just do left + right
		padding := ""
		if leftLen+rightLen < m.width {
			padding = barPad(m.width - leftLen - rightLen)
		}
		return statusBarStyle.Width(m.width).Render(left + padding + right)
	}

	// Center the center text (overall in the status bar, not between left/right)
	overallCenter := (m.width - centerLen) / 2
	leftPad := overallCenter - leftLen
	if leftPad < 0 {
		leftPad = 0
	}
	rightPad := m.width - rightLen - (leftLen + leftPad + centerLen)
	if rightPad < 0 {
		rightPad = 0
	}

	content := left + barPad(leftPad) + center + barPad(rightPad) + right
	return statusBarStyle.Width(m.width).Render(content)
}

// barPad returns n columns of background-colored padding for the status bar.
// It uses NBSP (U+00A0) rather than plain spaces: bubbletea v2's renderer
// "draws" runs of plain spaces with EL/ECH erases and assumes back-color-erase
// (ultraviolet canClearWith), so on terminals without BCE — GNU screen 4.00.03
// among them — space padding renders as default background and the bar
// vanishes. NBSP cells are not erase-optimizable and are always written
// explicitly with their background. Covered by
// TestE2E_PTYScreenStatusBarNotBCEReliant.
func barPad(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("\u00a0", n)
}

// renderInputArea renders the prompt and input with spell checking highlights.
func (m Model) renderInputArea() string {
	promptText := m.inputPromptText()
	promptRendered := ""
	if promptText != "" {
		promptRendered = promptStyle.Render(promptText)
	}

	// Build a per-byte misspelled lookup for O(1) access in the render loop.
	misspelledAt := make([]bool, len(m.inputValue)+1)
	for _, w := range m.spellChecker.ParseWords(m.inputValue) {
		if w.Misspelled {
			for i := w.Start; i < w.End; i++ {
				misspelledAt[i] = true
			}
		}
	}

	firstWidth := m.inputFirstLineWidth()
	rw := m.inputWrapWidth()

	// Draw over the display string — inputValue with preview text woven in —
	// and carry the cursor across into its coordinates. Everything below is in
	// display bytes; inOff tracks the matching offset in inputValue so the
	// spell-check lookup, which is indexed by what the user actually typed,
	// stays aligned across the ghost text it must skip.
	gs := m.ghosts()
	display, ghostSpans := m.inputDisplay()
	cursor := toDisplay(gs, m.inputCursor)
	inputLen := len(display)

	// Active incremental-search match, highlighted in place.
	matchStart, matchEnd, matchOK := m.searchMatchSpan()
	if matchOK {
		matchStart, matchEnd = toDisplay(gs, matchStart), toDisplay(gs, matchEnd)
	}

	// Calculate how many lines we'll render
	visibleLines := m.calculateInputHeight()

	var lines []string
	for lineIdx := 0; lineIdx < visibleLines; lineIdx++ {
		var sb strings.Builder

		if lineIdx == 0 {
			sb.WriteString(promptRendered)
		}

		start := m.inputLineStart(lineIdx)
		end := m.inputLineEnd(lineIdx)

		// Offset in inputValue matching display offset start, for misspelledAt.
		inOff := toInput(gs, start)

		spanIdx := 0
		for j := start; j < end; {
			// Advance past ghost spans this byte has already cleared.
			for spanIdx < len(ghostSpans) && j >= ghostSpans[spanIdx].end {
				spanIdx++
			}
			inGhost := spanIdx < len(ghostSpans) && ghostSpans[spanIdx].contains(j)

			// Emit a whole run of preview text in one styled chunk instead of
			// an escape pair per character. The other in-place styles here mark
			// a word or a search hit; a preview is a sentence, and per-rune
			// styling would put a kilobyte of escapes on the wire for every
			// keystroke — which is both slow on a remote link and exactly the
			// shape of output that overruns screen's escape buffer.
			if inGhost && j != cursor {
				runEnd := min(ghostSpans[spanIdx].end, end)
				if cursor > j && cursor < runEnd {
					runEnd = cursor // the cursor cell is drawn on its own below
				}
				sb.WriteString(linkPreviewStyle.Render(display[j:runEnd]))
				j = runEnd
				continue
			}

			_, size := utf8.DecodeRuneInString(display[j:])
			ch := display[j : j+size]

			switch {
			case j == cursor:
				sb.WriteString(cursorStyle.Render(ch))
			case inGhost:
				sb.WriteString(linkPreviewStyle.Render(ch))
			case matchOK && j >= matchStart && j < matchEnd:
				sb.WriteString(searchMatchStyle.Render(ch))
			case inOff < len(misspelledAt) && misspelledAt[inOff]:
				sb.WriteString(misspelledStyle.Render(ch))
			default:
				sb.WriteString(ch)
			}
			j += size
			if !inGhost {
				inOff += size
			}
		}

		// Cursor at end of this line or past end of input
		cursorOnThisLine := false
		if lineIdx == 0 {
			cursorOnThisLine = cursor >= start && cursor < firstWidth
		} else {
			lineEndPos := firstWidth + lineIdx*rw
			cursorOnThisLine = cursor >= start && cursor < lineEndPos
		}
		if cursor >= inputLen && lineIdx == visibleLines-1 {
			cursorOnThisLine = true
		}
		if cursor == end && cursorOnThisLine {
			sb.WriteString(cursorStyle.Render(" "))
		}

		lines = append(lines, sb.String())
	}

	return strings.Join(lines, "\n")
}

// inputPromptText returns the text to display as the input prompt.
func (m Model) inputPromptText() string {
	if m.reconnectPrompt {
		return "Reconnect? (Y/n)"
	}
	if m.searchMode {
		dir := "i-search"
		if m.searchBack {
			dir = "reverse-i-search"
		}
		// The match is highlighted in place in the input line, so the prompt
		// only echoes the pattern when the search is failing and there is no
		// highlight to show what was typed.
		if _, _, ok := m.searchMatchSpan(); ok || m.searchBuf == "" {
			return fmt.Sprintf("(%s):", dir)
		}
		return fmt.Sprintf("(failing %s)`%s':", dir, m.searchBuf)
	}
	if m.pasteMode {
		return "Paste:"
	}
	return m.prompt
}

// truncateAndPad truncates a string to maxWidth and pads with spaces.
func truncateAndPad(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Width(maxWidth).Render(s)
}
