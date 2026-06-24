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

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

// OutputItem represents a single item in the output buffer.
// It stores raw data so it can be reformatted when the window size changes.
// mouseWheelLines is how many output lines one wheel notch scrolls.
const mouseWheelLines = 3

// mouseWheelWarning is printed when wheel scrolling is enabled. Enabling mouse
// reporting makes the terminal forward clicks to the app, which pre-empts its
// native click-drag text selection; each terminal exposes a modifier to bypass
// reporting and select normally.
var mouseWheelWarning = []string{
	"Warning: this captures the mouse, so click-drag text selection no longer",
	"works normally. To select/copy text, hold a bypass modifier while dragging:",
	"  - most terminals (xterm, GNOME Terminal, Windows Terminal): Shift",
	"  - iTerm2: Option (⌥)",
	"  - macOS Terminal.app: Fn (or Shift)",
	"Use '%page wheel off' to restore normal selection.",
}

type OutputItem struct {
	Type string      // "text", "event", "command", "error", "input", "log"
	Data interface{} // raw data (string, map[string]interface{}, []string, logMsg)
	ID   int64       // WSServerMsg.ID of the message that produced this item (0 for local items)
}

// Model is the root Bubble Tea model for the TUI.
type Model struct {
	client *client.Client
	state  *api.StateResponse
	output []OutputItem // scrollback items (raw, to be formatted at render time)
	prompt string       // latest prompt text from server
	width  int
	height int

	// Output area - using bubbles/viewport
	viewport viewport.Model

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
	searchIdx  int    // index of current match in history (-1=none)

	// Meta prefix: Esc followed by a key is treated as M-<key>
	metaPrefix bool

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
	debugViewport viewport.Model

	// Scroll state
	lastSeenID int64 // highest WSServerMsg.ID whose output has been visible; never decreases

	// Auto-paging: after user sends input, auto-scroll up to one page of new output
	autoPageAnchor int  // line count when user sent command; -1 = disabled
	pagerEnabled   bool // false = never pause; scroll straight to bottom (%page off)
	mouseWheel     bool // mouse-wheel scrolling of the viewport (%page wheel); off by default

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
		client:         c,
		output:         output,
		input:          ti,
		keys:           NewKeyMap(),
		spellChecker:   NewSpellChecker(),
		logChan:        logChan,
		historyPos:     -1,
		searchIdx:      -1,
		autoPageAnchor: -1,
		pagerEnabled:   true,
		scrollAnchor:   -1,
		authMode:       !c.HasToken(),
		authField:      0,
		usernameInput:  usernameField,
		passwordInput:  passwordField,
	}
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
		return authResultMsg{username: username, password: password, err: nil}
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
		return authResultMsg{newClient: nc, err: err}
	}
}

// reportSeenNow reports lastSeenID to the proxy immediately.
func reportSeenNow(c *client.Client, lastSeenID int64) tea.Cmd {
	return func() tea.Msg {
		_ = c.ReportSeen(lastSeenID)
		return seenTickMsg{}
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
		return tea.HideCursor
	}
	return tea.Batch(
		listenCmd(m.client),
		listenLogCmd(m.logChan),
		tea.HideCursor,
		fetchInitialStateCmd(m.client),
	)
}

// Update handles messages and user input.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.ResumeMsg:
		// Returning from suspend (C-z / fg). Bubbletea restores the terminal and
		// re-hides the cursor automatically; force a full clear so any artifacts
		// left by the foreground process are wiped and the UI repaints cleanly.
		return m, tea.ClearScreen

	case tea.WindowSizeMsg:
		// A width change rewraps the output, so the raw scroll offset no longer
		// maps to the same message. Anchor on the item currently at the top
		// (computed at the OLD width, before we mutate m.width) so the view stays
		// put instead of jumping — e.g. when re-attaching screen from a
		// different-sized terminal. Skip on the first size (m.width == 0): there
		// is nothing to preserve and rendering at width 0 is meaningless.
		if m.width > 0 && msg.Width != m.width {
			m.scrollAnchor = m.topVisibleItemIndex()
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

	case tea.KeyMsg:
		if m.debugKeys {
			m.debugMsgs = append(m.debugMsgs, fmt.Sprintf("KEY: %s", msg.String()))
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

	case tea.MouseMsg:
		// Only the output viewport reacts to the wheel; auth/edit modes own the
		// screen and have no scrollback to page through. mouseWheel gates the
		// whole feature (off by default; toggled via %page wheel).
		if !m.mouseWheel || m.authMode || m.editMode {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.autoPageAnchor = -1
			if m.debugMode {
				m.debugViewport.ScrollUp(mouseWheelLines)
			} else {
				m.viewport.ScrollUp(mouseWheelLines)
			}
		case tea.MouseButtonWheelDown:
			m.autoPageAnchor = -1
			if m.debugMode {
				m.debugViewport.ScrollDown(mouseWheelLines)
			} else {
				m.viewport.ScrollDown(mouseWheelLines)
				m.advanceLastSeenID()
			}
		}
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
		// Run the identical post-login sequence (state fetch, history replay,
		// startup memo) for both initial login and reconnect.
		return m, tea.Batch(
			listenCmd(m.client),
			listenLogCmd(m.logChan),
			fetchInitialStateCmd(m.client),
			reportSeenCmd(m.client, 0),
		)

	case serverEventMsg:
		if msg.msg == nil {
			m.reconnectPrompt = true
			return m, nil
		}

		if jsonBytes, err := json.MarshalIndent(msg.msg, "", "  "); err == nil {
			m.debugMsgs = append(m.debugMsgs, "RECV:")
			m.debugMsgs = append(m.debugMsgs, strings.Split(string(jsonBytes), "\n")...)
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
			m.debugMsgs = append(m.debugMsgs, msg.text)
		} else {
			m.output = append(m.output, OutputItem{Type: "log", Data: msg})
			m = m.syncViewportContent()
		}
		return m, listenLogCmd(m.logChan)

	case seenTickMsg:
		return m, reportSeenCmd(m.client, m.lastSeenID)

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
		var replayCmds []tea.Cmd
		for i := range msg.events {
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
			if m.viewport.Height > 0 {
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
	oldYOffset := m.viewport.YOffset

	m.viewport = viewport.New(viewportWidth, viewportHeight)
	m.viewport.Style = lipgloss.NewStyle()

	if m.debugMode {
		debugWasAtBottom := m.debugViewport.AtBottom()
		debugOldYOffset := m.debugViewport.YOffset
		m.debugViewport = viewport.New(m.width-viewportWidth-1, viewportHeight)
		m.debugViewport.Style = lipgloss.NewStyle()
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
		if max := m.viewport.TotalLineCount() - m.viewport.Height; off > max {
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
	target := m.viewport.YOffset
	lineCount := 0
	for i, item := range m.output {
		lineCount += len(m.renderOutputItem(item))
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
		lineCount += len(m.renderOutputItem(m.output[i]))
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

// inputFirstLineWidth returns columns available for input on line 0 (after prompt).
func (m Model) inputFirstLineWidth() int {
	w := m.width - m.inputPromptDisplayWidth()
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
	n := len(m.inputValue) + 1 // +1 reserves cell for cursor
	if n <= firstWidth {
		return 1
	}
	rw := m.width
	if rw < 1 {
		rw = 1
	}
	return 1 + (n-firstWidth+rw-1)/rw
}

// syncViewportContent updates viewport with rendered output.
func (m Model) syncViewportContent() Model {
	var lines []string
	for _, item := range m.output {
		lines = append(lines, m.renderOutputItem(item)...)
	}
	// Capture follow state before SetContent: adding lines leaves YOffset
	// unchanged, so AtBottom() would read false afterwards even if we were
	// following the bottom a moment ago.
	wasAtBottom := m.viewport.AtBottom()
	prevLines := m.viewport.TotalLineCount()
	m.viewport.SetContent(strings.Join(lines, "\n"))

	totalLines := m.viewport.TotalLineCount()

	// Auto-paging: after user sends input, scroll to show up to one page of new output
	if m.autoPageAnchor >= 0 {
		newLines := totalLines - m.autoPageAnchor
		if !m.pagerEnabled || newLines <= m.viewport.Height {
			// Pager off, or new output fits in one page - show it all
			m.viewport.GotoBottom()
		} else {
			// More than one page of new output - show one page past anchor, then stop
			targetOffset := m.autoPageAnchor
			if targetOffset > totalLines-m.viewport.Height {
				targetOffset = totalLines - m.viewport.Height
			}
			m.viewport.SetYOffset(targetOffset)
			m.autoPageAnchor = -1 // disable further auto-paging until next user input
		}
	} else if wasAtBottom || totalLines <= m.viewport.Height {
		// We were following the bottom. Keep following for incremental output, but
		// if a whole page or more arrives at once, show one page from where we were
		// and pause the pager so the new output isn't scrolled past unseen.
		newLines := totalLines - prevLines
		if !m.pagerEnabled || newLines <= m.viewport.Height || m.viewport.Height <= 0 {
			m.viewport.GotoBottom()
		} else {
			targetOffset := prevLines
			if targetOffset > totalLines-m.viewport.Height {
				targetOffset = totalLines - m.viewport.Height
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

			if event == "private" || event == "emote" {
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
	visibleEnd := m.viewport.YOffset + m.viewport.Height
	lineCount := 0
	var maxID int64

	for _, item := range m.output {
		itemLines := len(m.renderOutputItem(item))
		lineCount += itemLines
		if lineCount > visibleEnd {
			break
		}
		if item.ID > maxID {
			maxID = item.ID
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

// restorePosition sets viewport scroll position from stored lastSeenID.
func (m *Model) restorePosition() {
	if m.viewport.Height <= 0 {
		return
	}

	lineCount := 0
	for _, item := range m.output {
		lineCount += len(m.renderOutputItem(item))
		if item.ID >= m.storedLastSeenID {
			break
		}
	}

	offset := lineCount - m.viewport.Height
	if offset < 0 {
		offset = 0
	}
	m.viewport.SetYOffset(offset)
	m.lastSeenID = m.storedLastSeenID
	m.needsPositionRestore = false
}

// View renders the full TUI.
func (m Model) View() string {
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
	for len(result) < m.viewport.Height {
		result = append(result, "")
	}

	return strings.Join(result[:m.viewport.Height], "\n")
}

// viewWithDebug renders split view: left=output, right=debug JSON.
func (m Model) viewWithDebug() string {
	leftWidth := m.width / 2
	rightWidth := m.width - leftWidth - 1

	leftLines := strings.Split(m.viewport.View(), "\n")
	rightLines := strings.Split(m.debugViewport.View(), "\n")

	var sb strings.Builder
	for i := 0; i < m.viewport.Height; i++ {
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

		right = server + " | " + userState + " | " + timeStr
	}

	// MORE indicator centered when not at bottom
	center := ""
	if !m.viewport.AtBottom() && m.viewport.TotalLineCount() > m.viewport.Height {
		moreCount := m.viewport.TotalLineCount() - m.viewport.YOffset - m.viewport.Height
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
			padding = strings.Repeat(" ", m.width-leftLen-rightLen)
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

	content := left + strings.Repeat(" ", leftPad) + center + strings.Repeat(" ", rightPad) + right
	return statusBarStyle.Width(m.width).Render(content)
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
	rw := m.width
	if rw < 1 {
		rw = 1
	}

	cursor := m.inputCursor
	inputLen := len(m.inputValue)

	// Calculate how many lines we'll render
	visibleLines := m.calculateInputHeight()

	// lineStart returns byte offset where line k begins
	lineStart := func(k int) int {
		if k == 0 {
			return 0
		}
		return firstWidth + (k-1)*rw
	}

	// lineEnd returns byte offset where line k ends (exclusive)
	lineEnd := func(k int) int {
		var end int
		if k == 0 {
			end = firstWidth
		} else {
			end = firstWidth + k*rw
		}
		if end > inputLen {
			end = inputLen
		}
		return end
	}

	var lines []string
	for lineIdx := 0; lineIdx < visibleLines; lineIdx++ {
		var sb strings.Builder

		if lineIdx == 0 {
			sb.WriteString(promptRendered)
		}

		start := lineStart(lineIdx)
		end := lineEnd(lineIdx)

		for j := start; j < end; {
			_, size := utf8.DecodeRuneInString(m.inputValue[j:])
			ch := m.inputValue[j : j+size]
			switch {
			case j == cursor:
				sb.WriteString(cursorStyle.Render(ch))
			case misspelledAt[j]:
				sb.WriteString(misspelledStyle.Render(ch))
			default:
				sb.WriteString(ch)
			}
			j += size
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
		return fmt.Sprintf("(%s)`%s':", dir, m.searchBuf)
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
