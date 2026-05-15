package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/muesli/reflow/wordwrap"
)

// OutputItem represents a single item in the output buffer.
// It stores raw data so it can be reformatted when the window size changes.
type OutputItem struct {
	Type string      // "text", "event", "command", "error", "input", "log"
	Data interface{} // raw data (string, map[string]interface{}, []string, logMsg)
	ID   int64       // WSServerMsg.ID of the message that produced this item (0 for local items)
}

// Model is the root Bubble Tea model for the TUI.
type Model struct {
	client    *client.Client
	state     *api.StateResponse
	output    []OutputItem // scrollback items (raw, to be formatted at render time)
	input     string       // current input buffer
	prompt    string       // latest prompt text from server
	width     int
	height    int
	connected bool

	// Input cursor and edit state
	cursor   int    // byte cursor position in m.input
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

	// Input area scroll: first visible wrapped line of the input area
	inputScroll int

	// Paste mode: newlines become spaces, leading spaces after newlines are eaten
	pasteMode    bool
	pasteEatFlag bool // eating whitespace after a newline
	pasteEatBuf  bool // have seen one non-post-newline space (next space triggers eating)

	// Spell checking
	spellChecker *SpellChecker

	// Debug view
	debugMode bool
	debugMsgs []string // raw JSON messages

	// Scroll state
	pagerTop          int   // index of first visible output line (pager model)
	debugScrollOffset int   // lines scrolled back in debug (from bottom)
	lastSeenID        int64 // highest WSServerMsg.ID whose output has been visible; never decreases

	// Position restore
	storedLastSeenID     int64 // lastSeenID from proxy at startup, used to restore pagerTop
	needsPositionRestore bool  // true until we have window size to set pagerTop

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
	h.ch <- logMsg{level: r.Level.String(), text: r.Message}
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
// state is the initial state snapshot fetched at startup.
// logChan should be created with NewLogger().
// initialEvents is the historical event buffer fetched from the proxy.
// storedLastSeenID is the last seen ID from the proxy (used to restore scroll position).
func New(c *client.Client, state *api.StateResponse, logChan <-chan logMsg, initialEvents []api.WSServerMsg, storedLastSeenID int64) Model {
	// Create initial output with logo
	logoLines := formatLogo()
	output := make([]OutputItem, 0, len(logoLines)+1)
	for _, line := range logoLines {
		output = append(output, OutputItem{Type: "text", Data: line})
	}
	output = append(output, OutputItem{Type: "text", Data: ""}) // blank line

	if state != nil {
		displayName := state.Whoami
		for _, e := range state.Entities {
			if e.Handle == state.Whoami && e.Kind == "user" {
				displayName = e.Name
				break
			}
		}
		connLine := "Connected to " + state.Server + " as " +
			privateSenderStyle.Render(displayName) + " (" + state.Whoami + ")"
		output = append(output, OutputItem{Type: "text", Data: connLine})
		output = append(output, OutputItem{Type: "text", Data: ""})
	}

	m := Model{
		client:           c,
		state:            state,
		output:           output,
		prompt:           "",
		spellChecker:     NewSpellChecker(),
		logChan:          logChan,
		storedLastSeenID: storedLastSeenID,
		historyPos:       -1,
		searchIdx:        -1,
	}

	for i := range initialEvents {
		m = m.handleProxy(&initialEvents[i])
	}

	bufSize := 0
	if state != nil {
		bufSize = state.EventBufSize
	}
	if len(initialEvents) > 0 {
		slog.Info(fmt.Sprintf("loaded %d events from history (proxy buffer: %d)", len(initialEvents), bufSize))
	} else {
		slog.Info(fmt.Sprintf("no history events loaded (proxy buffer: %d, stored lastSeenID: %d)", bufSize, storedLastSeenID))
	}

	if storedLastSeenID > 0 {
		m.needsPositionRestore = true
	}

	return m
}

// serverEventMsg wraps a message arriving from the proxy.
type serverEventMsg struct{ msg *api.WSServerMsg }

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

// Init starts the event listener.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		listenCmd(m.client),
		listenLogCmd(m.logChan),
		// seen reporting starts from WindowSizeMsg once we know the real lastSeenID
	)
}

// Update handles messages and user input.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.needsPositionRestore {
			m.restorePosition()
		}
		m.advanceLastSeenID()
		return m, reportSeenNow(m.client, m.lastSeenID)

	case tea.KeyMsg:
		if m.searchMode {
			m.metaPrefix = false
			m = m.handleSearchKey(msg)
			m = m.adjustInputScroll()
			return m, nil
		}

		// ESC as meta prefix: ESC <key> is equivalent to M-<key>
		key := msg.String()
		if m.metaPrefix {
			m.metaPrefix = false
			if key != "esc" { // ESC ESC cancels without action
				key = "alt+" + key
			}
		} else if key == "esc" {
			m.metaPrefix = true
			return m, nil
		}

		// Paste mode intercepts Enter and Space before normal processing.
		if m.pasteMode {
			switch key {
			case "enter", "ctrl+m", "ctrl+j":
				if m.pasteEatFlag {
					// Consecutive newline while eating → swallow it entirely
					return m, nil
				}
				// First newline → insert a space and start eating whitespace
				m.pasteEatFlag = true
				m.pasteEatBuf = false
				m = m.insertString(" ")
				m = m.adjustInputScroll()
				return m, nil
			case " ":
				if m.pasteEatFlag {
					// Eating post-newline whitespace → swallow
					return m, nil
				}
				if m.pasteEatBuf {
					// Second consecutive space → switch to eating mode and swallow
					m.pasteEatFlag = true
					return m, nil
				}
				// First space in a run: buffer it and insert normally
				m.pasteEatBuf = true
				m = m.insertString(" ")
				m = m.adjustInputScroll()
				return m, nil
			default:
				// Any other key resets eat state and falls through to normal handling
				m.pasteEatFlag = false
				m.pasteEatBuf = false
			}
		}

		wasKill := m.lastKill
		m.lastKill = false

		switch key {
		case "ctrl+c":
			return m, tea.Quit

		case "ctrl+d":
			if m.input == "" {
				return m, tea.Quit
			}
			m = m.deleteForward()

		case "enter", "ctrl+m", "ctrl+j":
			if m.input == "" {
				m.pagerTop += m.height - 3
				m.advanceLastSeenID()
				break
			}
			line := m.input
			m = m.addHistoryEntry(line)
			m.historyPos = -1
			m.historySave = ""
			m.input = ""
			m.cursor = 0
			m.inputScroll = 0
			m.output = append(m.output, OutputItem{Type: "input", Data: line})

			// Log outgoing command to debug (formatted)
			cmdMsg := api.WSClientMsg{Type: "command", Text: line}
			if jsonBytes, err := json.MarshalIndent(cmdMsg, "", "  "); err == nil {
				lines := strings.Split(string(jsonBytes), "\n")
				m.debugMsgs = append(m.debugMsgs, "SEND:")
				m.debugMsgs = append(m.debugMsgs, lines...)
			}
			m.debugScrollOffset = 0

			if err := m.client.Send(line); err != nil {
				m.output = append(m.output, OutputItem{Type: "error", Data: err.Error()})
			}

		case "ctrl+a":
			m.cursor = 0
		case "ctrl+e":
			m.cursor = len(m.input)
		case "ctrl+b", "left":
			m = m.moveCursorBack()
		case "ctrl+f", "right":
			m = m.moveCursorForward()
		case "alt+b":
			m.cursor = wordStartBefore(m.input, m.cursor)
		case "alt+f":
			m.cursor = wordEndAfter(m.input, m.cursor)

		case "backspace", "ctrl+h":
			m = m.deleteBack()
		case "delete":
			// delete key = backward-delete per tigerlily bindings
			m = m.deleteBack()

		case "ctrl+k":
			killed := m.input[m.cursor:]
			if wasKill {
				m.killRing += killed
			} else {
				m.killRing = killed
			}
			m.input = m.input[:m.cursor]
			m.lastKill = true

		case "ctrl+u":
			killed := m.input[:m.cursor]
			if wasKill {
				m.killRing = killed + m.killRing
			} else {
				m.killRing = killed
			}
			m.input = m.input[m.cursor:]
			m.cursor = 0
			m.lastKill = true

		case "ctrl+w", "alt+backspace":
			newPos := wordStartBefore(m.input, m.cursor)
			killed := m.input[newPos:m.cursor]
			if wasKill {
				m.killRing = killed + m.killRing
			} else {
				m.killRing = killed
			}
			m.input = m.input[:newPos] + m.input[m.cursor:]
			m.cursor = newPos
			m.lastKill = true

		case "alt+d":
			newPos := wordEndAfter(m.input, m.cursor)
			killed := m.input[m.cursor:newPos]
			if wasKill {
				m.killRing += killed
			} else {
				m.killRing = killed
			}
			m.input = m.input[:m.cursor] + m.input[newPos:]
			m.lastKill = true

		case "ctrl+y":
			m = m.insertString(m.killRing)

		case "ctrl+t":
			m = m.transposeChars()
		case "alt+t":
			m = m.transposeWords()
		case "alt+c":
			m = m.capitalizeWord()
		case "alt+u":
			m = m.upcaseWord()
		case "alt+l":
			m = m.downcaseWord()

		case "ctrl+p", "up":
			m = m.historyPrev()
		case "ctrl+n", "down":
			m = m.historyNext()

		case "alt+p":
			m.pasteMode = !m.pasteMode
			m.pasteEatFlag = false
			m.pasteEatBuf = false

		case "ctrl+r":
			m = m.enterSearch(true)
		case "ctrl+s":
			m = m.enterSearch(false)

		case "ctrl+l":
			// redraw — Bubble Tea redraws automatically

		case "pgup", "alt+v":
			if m.debugMode {
				m.debugScrollOffset += m.height - 3
			} else {
				m.pagerTop -= m.height - 3
				if m.pagerTop < 0 {
					m.pagerTop = 0
				}
			}

		case "pgdn", "ctrl+v":
			if m.debugMode {
				m.debugScrollOffset -= m.height - 3
				if m.debugScrollOffset < 0 {
					m.debugScrollOffset = 0
				}
			} else {
				m.pagerTop += m.height - 3
				m.advanceLastSeenID()
			}

		case "alt+,":
			if !m.debugMode {
				m.pagerTop--
				if m.pagerTop < 0 {
					m.pagerTop = 0
				}
			}
		case "alt+.":
			if !m.debugMode {
				m.pagerTop++
				m.advanceLastSeenID()
			}
		case "alt+<":
			if !m.debugMode {
				m.pagerTop = 0
			}
		case "alt+>":
			if !m.debugMode {
				m.pagerTop = 1 << 30 // clamped to maxTop in view
				m.advanceLastSeenID()
			}

		default:
			if msg.Type == tea.KeyRunes {
				m = m.insertString(string(msg.Runes))
			} else if msg.String() == " " {
				m = m.insertString(" ")
			}
		}

		m = m.adjustInputScroll()

	case serverEventMsg:
		if msg.msg == nil {
			m.output = append(m.output, OutputItem{Type: "error", Data: "--- disconnected ---"})
			return m, nil
		}

		// Log incoming message to debug (pretty-printed and wrapped)
		if jsonBytes, err := json.MarshalIndent(msg.msg, "", "  "); err == nil {
			lines := strings.Split(string(jsonBytes), "\n")
			m.debugMsgs = append(m.debugMsgs, "RECV:")
			m.debugMsgs = append(m.debugMsgs, lines...)
		}

		m = m.handleProxy(msg.msg)
		m.advanceLastSeenID()

		return m, listenCmd(m.client)

	case logMsg:
		m.output = append(m.output, OutputItem{Type: "log", Data: msg})
		return m, listenLogCmd(m.logChan)

	case seenTickMsg:
		return m, reportSeenCmd(m.client, m.lastSeenID)
	}

	return m, nil
}

// handleProxy incorporates a proxy message into the model.
func (m Model) handleProxy(msg *api.WSServerMsg) Model {
	switch msg.Type {
	case "text":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			if text, ok := d["text"].(string); ok && text != "" {
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
				if len(lines) > 0 {
					m.output = append(m.output, OutputItem{Type: "command", Data: lines, ID: msg.ID})
				}
			}
		}

	case "event":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			event, _ := d["event"].(string)
			source, _ := d["source"].(string)
			notify, _ := d["notify"].(bool)

			// Only display events the server flagged with NOTIFY=1,
			// and suppress unidle for the current user regardless.
			if notify && !(event == "unidle" && m.state != nil && source == m.state.Whoami) {
				m.output = append(m.output, OutputItem{Type: "event", Data: d, ID: msg.ID})
			}

			// Update local state for events that affect the current user
			if m.state != nil {
				// Only update if this event is about the current user
				if source == m.state.Whoami {
					// Find and update the user's entity in state
					for i := range m.state.Entities {
						if m.state.Entities[i].Handle == m.state.Whoami && m.state.Entities[i].Kind == "user" {
							switch event {
							case "rename":
								if value, ok := d["value"].(string); ok {
									m.state.Entities[i].Name = value
								}
							case "blurb":
								if value, ok := d["value"].(string); ok {
									m.state.Entities[i].Blurb = value
								}
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
		}

	case "prompt":
		if p, ok := msg.Data.(string); ok {
			m.prompt = p
		}

	case "error":
		if e, ok := msg.Data.(string); ok {
			m.output = append(m.output, OutputItem{Type: "error", Data: e, ID: msg.ID})
		}
	}
	return m
}

// computeLastSeenID returns the highest ID among OutputItems whose rendered lines
// fall entirely within [0, pagerTop+outputLines). This is the event stream position
// of the last item the user has actually seen on screen.
func (m Model) computeLastSeenID() int64 {
	if m.height == 0 {
		return m.lastSeenID
	}
	visibleEnd := m.pagerTop + (m.height - 2)
	lineCount := 0
	var maxID int64
	for _, item := range m.output {
		lineCount += len(m.renderOutputItem(item))
		if lineCount > visibleEnd {
			break
		}
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	return maxID
}

// advanceLastSeenID updates lastSeenID from the current pager position,
// enforcing the invariant that it never decreases.
func (m *Model) advanceLastSeenID() {
	if id := m.computeLastSeenID(); id > m.lastSeenID {
		m.lastSeenID = id
	}
}

// restorePosition sets pagerTop so that the storedLastSeenID item is at the
// bottom of the visible area, replicating the scroll position from the last session.
func (m *Model) restorePosition() {
	outputLines := m.height - 2
	if outputLines <= 0 {
		return
	}
	lineCount := 0
	for _, item := range m.output {
		lineCount += len(m.renderOutputItem(item))
		if item.ID >= m.storedLastSeenID {
			break
		}
	}
	pagerTop := lineCount - outputLines
	if pagerTop < 0 {
		pagerTop = 0
	}
	m.pagerTop = pagerTop
	m.lastSeenID = m.storedLastSeenID
	m.needsPositionRestore = false
}

// seenTickMsg is sent after a periodic ReportSeen call completes.
type seenTickMsg struct{}

// reportSeenNow reports lastSeenID to the proxy immediately (no sleep),
// then returns seenTickMsg to kick off the 30-second periodic cycle.
func reportSeenNow(c *client.Client, lastSeenID int64) tea.Cmd {
	return func() tea.Msg {
		_ = c.ReportSeen(lastSeenID)
		return seenTickMsg{}
	}
}

// reportSeenCmd waits 30 seconds, reports lastSeenID, then re-schedules itself.
func reportSeenCmd(c *client.Client, lastSeenID int64) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(30 * time.Second)
		_ = c.ReportSeen(lastSeenID)
		return seenTickMsg{}
	}
}

// View renders the full TUI.
func (m Model) View() string {
	if m.height == 0 {
		return "connecting..."
	}

	if m.debugMode {
		return m.viewWithDebug()
	}
	return m.viewNormal()
}

// renderOutputItem formats an OutputItem into display lines based on current width.
func (m Model) renderOutputItem(item OutputItem) []string {
	width := m.width
	if m.debugMode {
		width = m.width / 2 // left panel in debug mode
	}

	switch item.Type {
	case "text":
		if text, ok := item.Data.(string); ok {
			// Split on newlines to handle multiline text responses
			return strings.Split(text, "\n")
		}

	case "command":
		if lines, ok := item.Data.([]string); ok {
			return lines
		}

	case "event":
		if d, ok := item.Data.(map[string]interface{}); ok {
			formatted := formatEvent(d, width)
			return strings.Split(formatted, "\n")
		}

	case "error":
		if e, ok := item.Data.(string); ok {
			return []string{errorStyle.Render("*** " + e + " ***")}
		}

	case "input":
		if line, ok := item.Data.(string); ok {
			w := width
			if w < 1 {
				w = 1
			}
			wrapped := wordwrap.String(line, w)
			lines := strings.Split(wrapped, "\n")
			for i := range lines {
				lines[i] = inputStyle.Render(lines[i])
			}
			return lines
		}

	case "commandresult":
		if lines, ok := item.Data.([]string); ok {
			// Apply command result styling to each line
			styled := make([]string, len(lines))
			for i, line := range lines {
				styled[i] = commandResultStyle.Render(line)
			}
			return styled
		}

	case "log":
		if entry, ok := item.Data.(logMsg); ok {
			var labelStyle lipgloss.Style
			switch entry.level {
			case "ERROR":
				labelStyle = logErrorSeverityStyle
			case "WARN":
				labelStyle = logInfoSeverityStyle
			default:
				labelStyle = logPrefixStyle
			}
			label := labelStyle.Render("[" + entry.level + "]")
			return []string{label + " " + entry.text}
		}
	}

	return []string{"[unknown output type]"}
}

// formatStatusBar creates a tigerlily-style status bar with user info on the left
// and server/status/time on the right.
func (m Model) formatStatusBar(extraInfo string) string {
	// Left side: user name [blurb]
	left := ""
	if m.state != nil && m.state.Whoami != "" {
		// Find the current user's entity info
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

	// Right side: server | state | time
	right := ""
	if m.state != nil {
		server := m.state.Server
		if server == "" {
			server = "unknown"
		}

		// Get user state (here/away)
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

		// Current local time
		now := time.Now()
		timeStr := fmt.Sprintf("%02d:%02d", now.Hour(), now.Minute())

		right = server + " | " + userState + " | " + timeStr
	}

	// Add extra info if provided (e.g., scroll info, debug mode indicator)
	if extraInfo != "" {
		right += " " + extraInfo
	}

	// Last-seen event ID always on the far right
	idStr := fmt.Sprintf("#%d", m.lastSeenID)
	if right == "" {
		right = idStr
	} else {
		right += " | " + idStr
	}

	// Calculate padding
	totalLen := len(left) + len(right)
	padding := ""
	if totalLen < m.width {
		padding = strings.Repeat(" ", m.width-totalLen)
	}

	content := left + padding + right
	return statusBarStyle.Width(m.width).Render(content)
}

// formatMoreBar renders the pager "-- MORE (N) --" status bar with the last-seen
// event ID on the far right and the MORE text centered in the remaining space.
func (m Model) formatMoreBar(moreCount int) string {
	idStr := fmt.Sprintf("#%d", m.lastSeenID)
	moreText := fmt.Sprintf("-- MORE (%d) --", moreCount)

	// Center moreText in the space to the left of idStr
	innerWidth := m.width - len(idStr)
	if innerWidth < 0 {
		innerWidth = 0
	}
	padding := (innerWidth - len(moreText)) / 2
	if padding < 0 {
		padding = 0
	}
	content := strings.Repeat(" ", padding) + moreText
	// Pad to fill up to where idStr starts, then append idStr
	gap := innerWidth - len(content)
	if gap > 0 {
		content += strings.Repeat(" ", gap)
	}
	content += idStr
	return statusBarStyle.Width(m.width).Render(content)
}

// viewNormal renders the standard UI (output + status + input).
func (m Model) viewNormal() string {
	// Dynamic input area: grows up to half the window height.
	totalInputLines := m.inputTotalLines()
	maxInputLines := m.height / 2
	if maxInputLines < 1 {
		maxInputLines = 1
	}
	visibleInputLines := totalInputLines
	if visibleInputLines > maxInputLines {
		visibleInputLines = maxInputLines
	}
	if visibleInputLines < 1 {
		visibleInputLines = 1
	}

	// Render all output items into lines
	var allLines []string
	for _, item := range m.output {
		allLines = append(allLines, m.renderOutputItem(item)...)
	}

	// Output area height: status bar (1) + dynamic input area
	outputLines := m.height - 1 - visibleInputLines
	if outputLines < 0 {
		outputLines = 0
	}

	// Clamp pagerTop to valid range
	maxTop := len(allLines) - outputLines
	if maxTop < 0 {
		maxTop = 0
	}
	if m.pagerTop > maxTop {
		m.pagerTop = maxTop
	}

	// Visible slice
	start := m.pagerTop
	end := start + outputLines
	if end > len(allLines) {
		end = len(allLines)
	}
	visible := allLines[start:end]

	var sb strings.Builder
	for _, line := range visible {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	// Pad to fill the output area
	for i := len(visible); i < outputLines; i++ {
		sb.WriteByte('\n')
	}

	// Status line: show MORE bar when content is waiting below, otherwise normal
	moreCount := len(allLines) - (m.pagerTop + outputLines)
	if moreCount > 0 {
		sb.WriteString(m.formatMoreBar(moreCount))
	} else {
		sb.WriteString(m.formatStatusBar(""))
	}
	sb.WriteByte('\n')

	// Multi-line input area
	sb.WriteString(strings.Join(m.renderInputArea(visibleInputLines), "\n"))

	return sb.String()
}

// viewWithDebug renders a split view: left=output, right=debug JSON.
func (m Model) viewWithDebug() string {
	leftWidth := m.width / 2
	rightWidth := m.width - leftWidth - 1 // -1 for separator

	// Dynamic input area height (same logic as viewNormal)
	totalInputLines := m.inputTotalLines()
	maxInputLines := m.height / 2
	if maxInputLines < 1 {
		maxInputLines = 1
	}
	visibleInputLines := totalInputLines
	if visibleInputLines > maxInputLines {
		visibleInputLines = maxInputLines
	}
	if visibleInputLines < 1 {
		visibleInputLines = 1
	}

	outputLines := m.height - 1 - visibleInputLines
	if outputLines < 0 {
		outputLines = 0
	}

	// Render all output items into lines
	var allOutputLines []string
	for _, item := range m.output {
		allOutputLines = append(allOutputLines, m.renderOutputItem(item)...)
	}

	// Clamp pagerTop for output panel
	maxTop := len(allOutputLines) - outputLines
	if maxTop < 0 {
		maxTop = 0
	}
	if m.pagerTop > maxTop {
		m.pagerTop = maxTop
	}

	// Apply pagerTop to output
	startOutput := m.pagerTop
	endOutput := startOutput + outputLines
	if endOutput > len(allOutputLines) {
		endOutput = len(allOutputLines)
	}
	visibleOutput := allOutputLines[startOutput:endOutput]

	// Wrap debug lines to fit right panel
	wrappedDebug := wrapLines(m.debugMsgs, rightWidth)

	// Clamp debug scroll offset
	maxScrollDebug := len(wrappedDebug) - outputLines
	if maxScrollDebug < 0 {
		maxScrollDebug = 0
	}
	if m.debugScrollOffset > maxScrollDebug {
		m.debugScrollOffset = maxScrollDebug
	}

	// Apply scroll offset to debug
	endDebug := len(wrappedDebug) - m.debugScrollOffset
	startDebug := endDebug - outputLines
	if startDebug < 0 {
		startDebug = 0
	}
	if endDebug > len(wrappedDebug) {
		endDebug = len(wrappedDebug)
	}
	visibleDebug := wrappedDebug[startDebug:endDebug]

	var sb strings.Builder
	for i := 0; i < outputLines; i++ {
		// Left pane (output)
		left := ""
		if i < len(visibleOutput) {
			left = visibleOutput[i]
		}
		// Truncate and pad considering ANSI codes
		left = truncateAndPad(left, leftWidth)
		sb.WriteString(left)

		// Separator
		sb.WriteString("|")

		// Right pane (debug)
		right := ""
		if i < len(visibleDebug) {
			right = visibleDebug[i]
		}
		right = truncateAndPad(right, rightWidth)
		sb.WriteString(right)
		sb.WriteByte('\n')
	}

	// Status line
	moreCount := len(allOutputLines) - (m.pagerTop + outputLines)
	var status string
	if moreCount > 0 {
		status = m.formatMoreBar(moreCount)
	} else {
		extraInfo := ""
		if m.debugScrollOffset > 0 {
			extraInfo = fmt.Sprintf("[dbg:-%d]", m.debugScrollOffset)
		}
		status = m.formatStatusBar(extraInfo)
	}
	sb.WriteString(status)
	sb.WriteByte('\n')

	// Multi-line input area (full width, below both panels)
	sb.WriteString(strings.Join(m.renderInputArea(visibleInputLines), "\n"))

	return sb.String()
}

// truncateAndPad truncates a string to maxWidth (considering ANSI codes)
// and pads it to exactly maxWidth with spaces.
func truncateAndPad(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	// Use lipgloss to handle ANSI-aware width setting
	return lipgloss.NewStyle().Width(maxWidth).Render(s)
}

// wrapLines wraps each line in the input to fit within maxWidth.
func wrapLines(lines []string, maxWidth int) []string {
	if maxWidth <= 0 {
		return lines
	}
	var wrapped []string
	for _, line := range lines {
		if len(line) <= maxWidth {
			wrapped = append(wrapped, line)
		} else {
			// Break into chunks of maxWidth
			for len(line) > maxWidth {
				wrapped = append(wrapped, line[:maxWidth])
				line = line[maxWidth:]
			}
			if len(line) > 0 {
				wrapped = append(wrapped, line)
			}
		}
	}
	return wrapped
}
