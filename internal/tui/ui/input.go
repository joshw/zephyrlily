package ui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/joshw/zephyrlily/internal/tui/ascify"
)

// syncTextarea updates the textarea to match inputValue and inputCursor.
func (m *Model) syncTextarea() {
	m.input.SetValue(m.inputValue)
	m.input.SetCursor(m.inputCursor)
}

// maybeResizeViewport updates viewport if input height changed.
func (m Model) maybeResizeViewport() Model {
	newHeight := m.calculateInputHeight()
	currentViewportHeight := m.viewport.Height
	expectedViewportHeight := m.height - 1 - newHeight // -1 for status bar
	if expectedViewportHeight < 1 {
		expectedViewportHeight = 1
	}
	if currentViewportHeight != expectedViewportHeight {
		m = m.updateViewportSize()
	}
	return m
}

// handleAuthKey handles key events in authentication dialog mode.
func (m Model) handleAuthKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()

	// Double C-c to quit, as in normal mode; any other key cancels it.
	quitArmed := m.quitPending
	m.quitPending = false

	switch keyStr {
	case "tab":
		// Switch between username and password fields
		if m.authField == 0 {
			m.authField = 1
			m.usernameInput.Blur()
			m.passwordInput.Focus()
		} else {
			m.authField = 0
			m.passwordInput.Blur()
			m.usernameInput.Focus()
		}
		return m, nil

	case "enter", "ctrl+m", "ctrl+j":
		// Ignore if auth is already in progress
		if m.authInProgress {
			return m, nil
		}
		// Submit only when on password field
		if m.authField != 1 {
			// In username field, Tab to password instead
			m.authField = 1
			m.usernameInput.Blur()
			m.passwordInput.Focus()
			return m, nil
		}
		// Get values and attempt auth
		m.authUsername = m.usernameInput.Value()
		m.authPassword = m.passwordInput.Value()
		if m.authUsername == "" {
			m.authError = "Username required"
			return m, nil
		}
		if m.authPassword == "" {
			m.authError = "Password required"
			return m, nil
		}
		m.authError = ""
		m.authInProgress = true
		return m, attemptAuthCmd(m.client, m.authUsername, m.authPassword)

	case "esc", "ctrl+c":
		// Ctrl+C quits (on the second consecutive press)
		if keyStr == "ctrl+c" {
			if quitArmed {
				return m, tea.Quit
			}
			m.quitPending = true
			return m, nil
		}
		// ESC dismisses auth dialog (but we don't allow that for now)
		return m, nil

	case "ctrl+z":
		// Suspend: return a command that will suspend the app
		return m, func() tea.Msg { return tea.Suspend() }

	default:
		// Route to active textarea
		if m.authField == 0 {
			m.usernameInput, _ = m.usernameInput.Update(msg)
		} else {
			m.passwordInput, _ = m.passwordInput.Update(msg)
		}
		return m, nil
	}
}

// handleNormalKey handles key events in normal (non-special) mode.
func (m Model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()

	// Double C-c to quit: only a second consecutive C-c exits; any other key
	// cancels the pending quit.
	quitArmed := m.quitPending
	m.quitPending = false

	// ESC as meta prefix: ESC <key> is equivalent to M-<key>
	if m.metaPrefix {
		m.metaPrefix = false
		// Only printable runes and Backspace have M- bindings (M-b, M-f, M-v,
		// M-<, M-Bksp, …); nothing here is bound to Meta+Ctrl or Meta+arrow. A
		// lone ESC in front of such a key is almost always the stray tail of a
		// split escape sequence (e.g. the terminal flushing Alt-B as "ESC" then
		// "b" can leave a dangling ESC), so alt-ifying it would turn the next
		// Ctrl-B/Ctrl-F/arrow into an unbound combo and silently swallow it.
		// Drop the prefix and handle those keys normally instead.
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeyBackspace {
			// Synthesize an alt+ key by setting the Alt flag
			msg.Alt = true
			keyStr = msg.String()
		}
	} else if keyStr == "esc" {
		// ESC dismisses completion popup if active
		if m.completionActive {
			m = m.hideCompletionPopup()
			return m, nil
		}
		m.metaPrefix = true
		return m, nil
	}

	// Completion popup intercepts navigation keys
	if m.completionActive {
		return m.handleCompletionKey(msg)
	}

	// Check for paste mode toggle (allow escaping paste mode)
	if key.Matches(msg, m.keys.PasteMode) {
		m.pasteMode = !m.pasteMode
		m.pasteEatFlag = false
		m.pasteEatBuf = false
		// Toggling changes the prompt ("Paste:" vs none), which changes the
		// first-line width and therefore the input height; resize the viewport
		// so the layout doesn't exceed the screen and corrupt the display.
		m = m.maybeResizeViewport()
		return m, nil
	}

	// Paste mode intercepts Enter and Space
	if m.pasteMode {
		if m.debugMode {
			m.appendDebug(fmt.Sprintf("[paste] keyStr=%q type=%v runes=%q", keyStr, msg.Type, string(msg.Runes)))
		}
		// Handle both pasted multi-line text (KeyRunes with multiple chars) and
		// individual keystrokes. Alt-modified runes are not pasted text — pastes
		// never carry the Alt flag — they are M- chords (M-f/M-b word motion,
		// M-d, …); let them fall through to their normal bindings.
		if msg.Type == tea.KeyRunes && !msg.Alt {
			for _, r := range msg.Runes {
				m = m.pasteRune(r)
			}
			m.syncTextarea()
			m = m.maybeResizeViewport()
			m.armPagerIfAtBottom()
			return m, nil
		}
		// Non-rune keys: Enter/Ctrl+M/Ctrl+J treated the same as a newline rune.
		if keyStr == "enter" || keyStr == "ctrl+m" || keyStr == "ctrl+j" {
			m = m.pasteRune('\n')
			m.syncTextarea()
			m = m.maybeResizeViewport()
			m.armPagerIfAtBottom()
			return m, nil
		}
		// Non-enter, non-rune key: clear flags
		m.pasteEatFlag = false
		m.pasteEatBuf = false
	}

	wasKill := m.lastKill
	m.lastKill = false

	switch {
	// Quit
	case key.Matches(msg, m.keys.Quit):
		if quitArmed {
			return m, tea.Quit
		}
		m.quitPending = true
		m.output = append(m.output, OutputItem{Type: "text", Data: "Press C-c again to exit."})
		m = m.syncViewportContent()

	case key.Matches(msg, m.keys.ForceQuit):
		if m.inputValue == "" {
			return m, tea.Quit
		}
		m = m.deleteForward()

	// Suspend (job control); the UI is repainted on resume via tea.ResumeMsg.
	case key.Matches(msg, m.keys.Suspend):
		return m, tea.Suspend

	// Submit
	case key.Matches(msg, m.keys.Submit):
		return m.handleSubmit()

	// Navigation
	case key.Matches(msg, m.keys.LineStart):
		m.inputCursor = 0
	case key.Matches(msg, m.keys.LineEnd):
		m.inputCursor = len(m.inputValue)
	case key.Matches(msg, m.keys.CharBack):
		if m.inputCursor > 0 {
			m.inputCursor--
		}
	case key.Matches(msg, m.keys.CharForward):
		if m.inputCursor < len(m.inputValue) {
			m.inputCursor++
		}
	case key.Matches(msg, m.keys.WordBack):
		m.inputCursor = wordStartBefore(m.inputValue, m.inputCursor)
	case key.Matches(msg, m.keys.WordForward):
		m.inputCursor = wordEndAfter(m.inputValue, m.inputCursor)

	// Editing
	case key.Matches(msg, m.keys.DeleteBack):
		m = m.deleteBack()
	case key.Matches(msg, m.keys.DeleteForward):
		m = m.deleteForward()
	case key.Matches(msg, m.keys.DeleteWord):
		m = m.deleteWordForward(wasKill)
	case key.Matches(msg, m.keys.DeleteWordBack):
		m = m.deleteWordBack(wasKill)
	case key.Matches(msg, m.keys.KillLine):
		m = m.killLine(wasKill)
	case key.Matches(msg, m.keys.KillLineBack):
		m = m.killLineBack(wasKill)
	case key.Matches(msg, m.keys.Yank):
		m = m.yank()
	case key.Matches(msg, m.keys.Transpose):
		m = m.transposeChars()
	case key.Matches(msg, m.keys.TransposeWord):
		m = m.transposeWords()
	case key.Matches(msg, m.keys.Capitalize):
		m = m.capitalizeWord()
	case key.Matches(msg, m.keys.Uppercase):
		m = m.upcaseWord()
	case key.Matches(msg, m.keys.Lowercase):
		m = m.downcaseWord()

	// History
	case key.Matches(msg, m.keys.HistoryPrev):
		m = m.historyPrev()
	case key.Matches(msg, m.keys.HistoryNext):
		m = m.historyNext()

	// Search. Disabled in paste mode: handleSearchKey bypasses paste handling
	// entirely, so cursor movement and the paste-mode toggle would stop working
	// until the search is dismissed.
	case key.Matches(msg, m.keys.SearchBack) && !m.pasteMode:
		m = m.enterSearch(true)
	case key.Matches(msg, m.keys.SearchForward) && !m.pasteMode:
		m = m.enterSearch(false)

	// Tab completion
	case key.Matches(msg, m.keys.TabComplete):
		m = m.tabComplete()

	// Viewport navigation
	case key.Matches(msg, m.keys.PageUp):
		m.autoPageAnchor = -1
		if m.debugMode {
			m.debugViewport.PageUp()
		} else {
			m.viewport.PageUp()
		}
	case key.Matches(msg, m.keys.PageDown):
		m.autoPageAnchor = -1
		if m.debugMode {
			m.debugViewport.PageDown()
		} else {
			m.viewport.PageDown()
			m.advanceLastSeenID()
		}
	case key.Matches(msg, m.keys.ScrollUp):
		m.autoPageAnchor = -1
		m.viewport.ScrollUp(1)
	case key.Matches(msg, m.keys.ScrollDown):
		m.autoPageAnchor = -1
		m.viewport.ScrollDown(1)
		m.advanceLastSeenID()
	case key.Matches(msg, m.keys.GotoTop):
		m.autoPageAnchor = -1
		m.viewport.GotoTop()
	case key.Matches(msg, m.keys.GotoBottom):
		m.autoPageAnchor = -1
		m.viewport.GotoBottom()
		m.advanceLastSeenID()

	// Mode toggles
	case key.Matches(msg, m.keys.DebugMode):
		// Toggling debug halves/restores the viewport width, which rewraps output;
		// anchor on the top item (at the current width) so it stays in view.
		m.scrollAnchor = m.topVisibleItemIndex()
		m.debugMode = !m.debugMode
		m.renderEpoch++ // render width changed; item caches are stale
		m = m.updateViewportSize()
	case key.Matches(msg, m.keys.Redraw):
		// Bubbletea handles redraw automatically

	default:
		// Handle intelligent expand keys
		if msg.Type == tea.KeyRunes {
			s := string(msg.Runes)
			switch s {
			case ";", ":", ",", "=":
				m = m.handleExpandKey(s)
				m.syncTextarea()
				m.armPagerIfAtBottom()
				return m, nil
			default:
				m = m.insertString(s)
			}
		} else if keyStr == " " {
			m = m.insertString(" ")
		}
	}

	m.syncTextarea()
	m = m.maybeResizeViewport()
	m.armPagerIfAtBottom()
	return m, nil
}

// handleSubmit processes Enter key - sends command or advances pager.
func (m Model) handleSubmit() (tea.Model, tea.Cmd) {
	line := m.inputValue

	if line == "" {
		if m.prompt != "" {
			// Empty response to server prompt - will echo below
		} else if !m.viewport.AtBottom() {
			// Behind the latest output - use the blank Enter to advance the pager
			// instead of sending it.
			m.viewport.PageDown()
			m.advanceLastSeenID()
			m.autoPageAnchor = -1 // clear auto-paging on manual pager advance
			// …but if this advance caught us up, re-arm so output arriving while
			// we're idle pauses again instead of streaming past.
			m.armPagerIfAtBottom()
			return m, nil
		}
		// Otherwise (caught up to the bottom) fall through and send the blank line.
	} else {
		m = m.addHistoryEntry(line)
	}

	return m.submitLine(line)
}

// submitLine echoes, dispatches, and sends a single input line exactly as if the
// user had typed it and pressed Enter. It is the shared core of handleSubmit and
// is also used to replay commands from the zlilyStartup memo.
func (m Model) submitLine(line string) (Model, tea.Cmd) {
	// Auto-scroll to the response only if the user was already following the
	// bottom; if they're scrolled back reading history, leave the viewport put.
	if m.viewport.AtBottom() {
		m.autoPageAnchor = m.viewport.TotalLineCount()
	} else {
		m.autoPageAnchor = -1
	}

	// Echo the sent line
	m.output = append(m.output, OutputItem{Type: "input", Data: line})

	// Reset input state
	m.historyPos = -1
	m.historySave = ""
	m.inputValue = ""
	m.inputCursor = 0
	m.input.SetValue("")
	m.prompt = ""

	// The input has collapsed back to a single line; reclaim the freed space
	// for the viewport now so the layout doesn't stay expanded until the next
	// keypress or scroll. Done before content is paged in so the subsequent
	// syncViewportContent calls use the correct viewport height.
	m = m.maybeResizeViewport()

	// Log outgoing command to debug
	if m.debugMode {
		cmdMsg := struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{"command", line}
		if jsonBytes, err := json.MarshalIndent(cmdMsg, "", "  "); err == nil {
			m.appendDebug("SEND:")
			m.appendDebug(strings.Split(string(jsonBytes), "\n")...)
		}
	}

	// Handle client-side commands (%page, %set debug keys, %style, %spell, …).
	m, localOutput, localCmd, recognized := m.applyLocalCommand(line)
	if localOutput != nil {
		m.output = append(m.output, OutputItem{Type: "command", Data: localOutput})
	}
	if localCmd != nil {
		m = m.syncViewportContent()
		return m, localCmd
	}
	if !recognized {
		m = m.trackOutgoingSend(line)
		if err := m.client.Send(line); err != nil {
			m.output = append(m.output, OutputItem{Type: "error", Data: err.Error()})
		}
	}

	m = m.syncViewportContent()
	return m, nil
}

// applyLocalCommand handles a command line the client interprets itself (one the
// proxy does not own): %set debug keys, %page and its variants, and the commands
// dispatched by handleLocalCommand (%style, %spell, %help, %info/%memo edit). It
// returns the updated model, output lines to display, an optional tea.Cmd, and
// whether the line was recognised as a local command. It is shared by submitLine
// (interactive input) and the "clientcommand" handler (commands forwarded by the
// proxy from the zlilyStartup memo), so both paths behave identically.
func (m Model) applyLocalCommand(line string) (Model, []string, tea.Cmd, bool) {
	// Debug key toggle.
	if strings.EqualFold(strings.TrimSpace(line), "%set debug keys") {
		m.debugKeys = !m.debugKeys
		state := "off"
		if m.debugKeys {
			state = "on"
		}
		return m, []string{"Key debug logging: " + state}, nil, true
	}

	// %page toggle for the viewport pager.
	if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "%page" {
		// %page wheel [on|off] controls mouse-wheel scrolling of the viewport.
		if len(fields) >= 2 && strings.EqualFold(fields[1], "wheel") {
			var lines []string
			var cmd tea.Cmd
			switch {
			case len(fields) == 3 && strings.EqualFold(fields[2], "on"):
				m.mouseWheel = true
				cmd = tea.EnableMouseCellMotion
				lines = append([]string{"Mouse-wheel scrolling: on"}, mouseWheelWarning...)
			case len(fields) == 3 && strings.EqualFold(fields[2], "off"):
				m.mouseWheel = false
				cmd = tea.DisableMouse
				lines = []string{"Mouse-wheel scrolling: off"}
			case len(fields) == 2:
				state := "off"
				if m.mouseWheel {
					state = "on"
				}
				lines = []string{"Mouse-wheel scrolling: " + state}
			default:
				lines = []string{"Usage: %page wheel on|off"}
			}
			return m, lines, cmd, true
		}

		var msg string
		switch {
		case len(fields) == 2 && strings.EqualFold(fields[1], "off"):
			m.pagerEnabled = false
			msg = "Viewport pager: off"
		case len(fields) == 2 && strings.EqualFold(fields[1], "on"):
			m.pagerEnabled = true
			msg = "Viewport pager: on"
		case len(fields) == 1:
			state := "on"
			if !m.pagerEnabled {
				state = "off"
			}
			msg = "Viewport pager: " + state
		default:
			msg = "Usage: %page on|off|wheel"
		}
		return m, []string{msg}, nil, true
	}

	out, handled, asyncCmd := m.handleLocalCommand(line)
	return m, out, asyncCmd, handled
}

// handleReconnectKey handles key events during reconnect prompt.
func (m Model) handleReconnectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter", "ctrl+m", "ctrl+j":
		m.reconnectPrompt = false
		m.authInProgress = true
		m.output = append(m.output, OutputItem{Type: "text", Data: "(reconnecting…)"})
		m = m.syncViewportContent()
		return m, reconnectCmd(m.client)
	case "n", "N", "esc", "ctrl+c":
		return m, tea.Quit
	}
	return m, nil
}

// handleSearchKey handles key events during incremental search.
func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.metaPrefix = false

	// Any cursor-motion key ends the search, accepting the current match, and
	// is then processed normally — from the cursor position at the start of
	// the match (where withSearchResult left it). This must run before the
	// switch below: M-b/M-f arrive as alt-flagged runes, which the default
	// case would otherwise append to the search pattern.
	if key.Matches(msg, m.keys.LineStart) || key.Matches(msg, m.keys.LineEnd) ||
		key.Matches(msg, m.keys.CharBack) || key.Matches(msg, m.keys.CharForward) ||
		key.Matches(msg, m.keys.WordBack) || key.Matches(msg, m.keys.WordForward) {
		m.searchMode = false
		return m.handleNormalKey(msg)
	}

	switch msg.String() {
	case "ctrl+r":
		m.searchBack = true
		m = m.searchStep(-1)
	case "ctrl+s":
		m.searchBack = false
		m = m.searchStep(1)
	case "ctrl+g", "esc":
		// Cancel search, restore original input
		m.searchMode = false
		m.inputValue = m.searchSave
		m.inputCursor = len(m.inputValue)
	case "enter", "ctrl+m", "ctrl+j":
		// Accept current match
		m.searchMode = false
	case "backspace", "ctrl+h":
		if len(m.searchBuf) > 0 {
			m.searchBuf = m.searchBuf[:len(m.searchBuf)-1]
			m = m.searchRefresh()
		}
	case " ":
		m.searchBuf += " "
		m = m.searchRefresh()
	default:
		if msg.Type == tea.KeyRunes {
			m.searchBuf += string(msg.Runes)
			m = m.searchRefresh()
		}
	}

	m.syncTextarea()
	m = m.maybeResizeViewport()
	return m, nil
}

// Search helpers

func (m Model) enterSearch(backward bool) Model {
	m.searchMode = true
	m.searchBack = backward
	m.searchBuf = ""
	m.searchSave = m.inputValue
	m.searchIdx = -1
	// Anchor the search at the cursor end of the current line: reverse search
	// scans leftward from the end, forward search rightward from the start.
	if backward {
		m.searchPos = len(m.searchSave)
	} else {
		m.searchPos = 0
	}
	return m
}

// The incremental search space is an ordered list of "lines": history items
// (oldest..newest at indices 0..len-1) followed by the current saved line at
// index len(history). Backward search moves toward older lines / leftward
// within a line; forward search moves toward newer lines / rightward.

// searchLineAt returns the text of linear line L (len(history) == current line).
func (m Model) searchLineAt(L int) string {
	if L >= len(m.history) {
		return m.searchSave
	}
	return m.history[L]
}

// searchLinear returns the linear index of the current match.
func (m Model) searchLinear() int {
	if m.searchIdx < 0 {
		return len(m.history)
	}
	return m.searchIdx
}

// withSearchResult records a match at linear line L, byte offset pos.
func (m Model) withSearchResult(L, pos int) Model {
	if L >= len(m.history) {
		m.searchIdx = -1
	} else {
		m.searchIdx = L
	}
	m.searchPos = pos
	m.inputValue = m.searchLineAt(L)
	m.inputCursor = pos
	return m
}

// searchFind scans for the current pattern starting from linear line L at byte
// offset off, moving in direction (-1 backward/older, +1 forward/newer). When
// inclusive is false the starting offset itself is skipped. It first looks
// within the starting line, then crosses into adjacent lines. Returns the line,
// match offset, and whether a match was found.
func (m Model) searchFind(L, off, direction int, inclusive bool) (int, int, bool) {
	searchLower := strings.ToLower(m.searchBuf)
	if searchLower == "" {
		return 0, 0, false
	}

	if direction < 0 {
		limit := off
		if !inclusive {
			limit = off - 1
		}
		if pos := lastMatchAtMost(m.searchLineAt(L), searchLower, limit); pos >= 0 {
			return L, pos, true
		}
		for LL := L - 1; LL >= 0; LL-- {
			line := m.searchLineAt(LL)
			if pos := lastMatchAtMost(line, searchLower, len(line)); pos >= 0 {
				return LL, pos, true
			}
		}
	} else {
		from := off
		if !inclusive {
			from = off + 1
		}
		if pos := firstMatchAtLeast(m.searchLineAt(L), searchLower, from); pos >= 0 {
			return L, pos, true
		}
		for LL := L + 1; LL <= len(m.history); LL++ {
			if pos := firstMatchAtLeast(m.searchLineAt(LL), searchLower, 0); pos >= 0 {
				return LL, pos, true
			}
		}
	}
	return 0, 0, false
}

// searchRefresh re-runs the search after the pattern changes (typing/backspace),
// re-anchoring from the current match position in the active direction.
func (m Model) searchRefresh() Model {
	if m.searchBuf == "" {
		m.inputValue = m.searchSave
		m.inputCursor = len(m.inputValue)
		m.searchIdx = -1
		m.searchPos = len(m.searchSave)
		return m
	}

	dir := 1
	if m.searchBack {
		dir = -1
	}
	if L, pos, ok := m.searchFind(m.searchLinear(), m.searchPos, dir, true); ok {
		m = m.withSearchResult(L, pos)
	}
	return m
}

// searchMatchSpan returns the byte range [start, end) of the current match
// within inputValue, for highlighting it in place. ok is false when there is
// nothing to highlight: search not active, empty pattern, or a failing search
// (the pattern no longer matches at the recorded position, which happens when
// typing a character finds no match and searchRefresh keeps the previous
// line/position).
func (m Model) searchMatchSpan() (start, end int, ok bool) {
	if !m.searchMode || m.searchBuf == "" {
		return 0, 0, false
	}
	start = m.searchPos
	if start < 0 || start > len(m.inputValue) {
		return 0, 0, false
	}
	searchLower := strings.ToLower(m.searchBuf)
	if !strings.HasPrefix(strings.ToLower(m.inputValue[start:]), searchLower) {
		return 0, 0, false
	}
	end = start + len(searchLower)
	if end > len(m.inputValue) {
		end = len(m.inputValue)
	}
	return start, end, true
}

// searchStep moves to the next match in direction (C-r/C-s repeats), skipping
// the current match position.
func (m Model) searchStep(direction int) Model {
	if m.searchBuf == "" {
		return m
	}
	if L, pos, ok := m.searchFind(m.searchLinear(), m.searchPos, direction, false); ok {
		m = m.withSearchResult(L, pos)
	}
	return m
}

// firstMatchAtLeast returns the byte offset of the first occurrence of
// searchLower (already lowercase) in text at or after from, or -1 if none.
func firstMatchAtLeast(text, searchLower string, from int) int {
	if from < 0 {
		from = 0
	}
	if from > len(text) {
		return -1
	}
	idx := strings.Index(strings.ToLower(text)[from:], searchLower)
	if idx < 0 {
		return -1
	}
	return from + idx
}

// lastMatchAtMost returns the byte offset of the last (rightmost) occurrence of
// searchLower (already lowercase) in text starting at or before limit, or -1.
func lastMatchAtMost(text, searchLower string, limit int) int {
	if limit < 0 {
		return -1
	}
	textLower := strings.ToLower(text)
	if limit > len(textLower) {
		limit = len(textLower)
	}
	for i := limit; i >= 0; i-- {
		if strings.HasPrefix(textLower[i:], searchLower) {
			return i
		}
	}
	return -1
}

// History helpers

func (m Model) addHistoryEntry(line string) Model {
	// Don't add duplicates of the most recent entry
	if len(m.history) > 0 && m.history[len(m.history)-1] == line {
		return m
	}
	m.history = append(m.history, line)
	return m
}

func (m Model) historyPrev() Model {
	if len(m.history) == 0 {
		return m
	}

	if m.historyPos == -1 {
		// Save current input and start browsing
		m.historySave = m.inputValue
		m.historyPos = len(m.history) - 1
	} else if m.historyPos > 0 {
		m.historyPos--
	}

	m.inputValue = m.history[m.historyPos]
	m.inputCursor = len(m.inputValue)
	return m
}

func (m Model) historyNext() Model {
	if m.historyPos == -1 {
		return m
	}

	if m.historyPos < len(m.history)-1 {
		m.historyPos++
		m.inputValue = m.history[m.historyPos]
	} else {
		// Return to live input
		m.historyPos = -1
		m.inputValue = m.historySave
	}

	m.inputCursor = len(m.inputValue)
	return m
}

// Input manipulation helpers

func (m Model) deleteBack() Model {
	if m.inputCursor > 0 && m.inputCursor <= len(m.inputValue) {
		m.inputValue = m.inputValue[:m.inputCursor-1] + m.inputValue[m.inputCursor:]
		m.inputCursor--
	}
	return m
}

func (m Model) deleteForward() Model {
	if m.inputCursor < len(m.inputValue) {
		m.inputValue = m.inputValue[:m.inputCursor] + m.inputValue[m.inputCursor+1:]
	}
	return m
}

func (m Model) deleteWordBack(wasKill bool) Model {
	newPos := wordStartBefore(m.inputValue, m.inputCursor)
	killed := m.inputValue[newPos:m.inputCursor]

	if wasKill {
		m.killRing = killed + m.killRing
	} else {
		m.killRing = killed
	}
	m.lastKill = true

	m.inputValue = m.inputValue[:newPos] + m.inputValue[m.inputCursor:]
	m.inputCursor = newPos
	return m
}

func (m Model) deleteWordForward(wasKill bool) Model {
	newPos := wordEndAfter(m.inputValue, m.inputCursor)
	killed := m.inputValue[m.inputCursor:newPos]

	if wasKill {
		m.killRing += killed
	} else {
		m.killRing = killed
	}
	m.lastKill = true

	m.inputValue = m.inputValue[:m.inputCursor] + m.inputValue[newPos:]
	return m
}

func (m Model) killLine(wasKill bool) Model {
	killed := m.inputValue[m.inputCursor:]

	if wasKill {
		m.killRing += killed
	} else {
		m.killRing = killed
	}
	m.lastKill = true

	m.inputValue = m.inputValue[:m.inputCursor]
	return m
}

func (m Model) killLineBack(wasKill bool) Model {
	killed := m.inputValue[:m.inputCursor]

	if wasKill {
		m.killRing = killed + m.killRing
	} else {
		m.killRing = killed
	}
	m.lastKill = true

	m.inputValue = m.inputValue[m.inputCursor:]
	m.inputCursor = 0
	return m
}

func (m Model) yank() Model {
	if m.killRing == "" {
		return m
	}
	m.inputValue = m.inputValue[:m.inputCursor] + m.killRing + m.inputValue[m.inputCursor:]
	m.inputCursor += len(m.killRing)
	return m
}

func (m Model) transposeChars() Model {
	if len(m.inputValue) < 2 {
		return m
	}

	pos := m.inputCursor
	if pos >= len(m.inputValue) {
		pos = len(m.inputValue) - 1
	}
	if pos == 0 {
		return m
	}

	chars := []byte(m.inputValue)
	chars[pos-1], chars[pos] = chars[pos], chars[pos-1]
	m.inputValue = string(chars)
	m.inputCursor = pos + 1
	return m
}

func (m Model) transposeWords() Model {
	pos := m.inputCursor

	// Find word boundaries around cursor
	word1End := wordStartBefore(m.inputValue, pos)
	if word1End == 0 {
		return m
	}
	word1Start := wordStartBefore(m.inputValue, word1End)
	word2Start := pos
	for word2Start < len(m.inputValue) && !isWordChar(m.inputValue[word2Start]) {
		word2Start++
	}
	word2End := wordEndAfter(m.inputValue, word2Start)

	if word2Start >= word2End || word1Start >= word1End {
		return m
	}

	word1 := m.inputValue[word1Start:word1End]
	word2 := m.inputValue[word2Start:word2End]
	between := m.inputValue[word1End:word2Start]

	m.inputValue = m.inputValue[:word1Start] + word2 + between + word1 + m.inputValue[word2End:]
	m.inputCursor = word1Start + len(word2) + len(between) + len(word1)
	return m
}

func (m Model) capitalizeWord() Model {
	pos := m.inputCursor

	// Skip non-word chars
	for pos < len(m.inputValue) && !isWordChar(m.inputValue[pos]) {
		pos++
	}
	if pos >= len(m.inputValue) {
		return m
	}

	// Capitalize first letter, lowercase rest
	end := wordEndAfter(m.inputValue, pos)
	word := m.inputValue[pos:end]
	if len(word) > 0 {
		word = strings.ToUpper(string(word[0])) + strings.ToLower(word[1:])
	}

	m.inputValue = m.inputValue[:pos] + word + m.inputValue[end:]
	m.inputCursor = end
	return m
}

func (m Model) upcaseWord() Model {
	pos := m.inputCursor

	for pos < len(m.inputValue) && !isWordChar(m.inputValue[pos]) {
		pos++
	}
	if pos >= len(m.inputValue) {
		return m
	}

	end := wordEndAfter(m.inputValue, pos)
	word := strings.ToUpper(m.inputValue[pos:end])

	m.inputValue = m.inputValue[:pos] + word + m.inputValue[end:]
	m.inputCursor = end
	return m
}

func (m Model) downcaseWord() Model {
	pos := m.inputCursor

	for pos < len(m.inputValue) && !isWordChar(m.inputValue[pos]) {
		pos++
	}
	if pos >= len(m.inputValue) {
		return m
	}

	end := wordEndAfter(m.inputValue, pos)
	word := strings.ToLower(m.inputValue[pos:end])

	m.inputValue = m.inputValue[:pos] + word + m.inputValue[end:]
	m.inputCursor = end
	return m
}

// pasteRune applies one pasted rune to the input using paste-mode whitespace
// rules: a run of whitespace (space, tab, CR, or LF) collapses to a single
// space, and any other rune is inserted verbatim. pasteEatBuf/pasteEatFlag
// carry the run state across calls, so callers must feed runes in order.
func (m Model) pasteRune(r rune) Model {
	if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
		// Whitespace: emit one space for the run, then eat the rest.
		if m.pasteEatFlag {
			return m
		}
		if m.pasteEatBuf {
			m.pasteEatFlag = true
			return m
		}
		m.pasteEatBuf = true
		return m.insertString(" ")
	}
	// Non-whitespace: end any whitespace run and insert.
	m.pasteEatFlag = false
	m.pasteEatBuf = false
	return m.insertString(string(r))
}

// insertString inserts s at the cursor position, converting Unicode to ASCII.
func (m Model) insertString(s string) Model {
	// Convert Unicode characters to ASCII approximations
	s = ascify.String(s)
	// Drop ASCII control characters (tabs, stray CR/LF, etc.). The input is a
	// single logical line and renderInputArea hard-wraps by byte offset assuming
	// one byte == one display column; a literal tab renders as several columns
	// and would desync the wrap from what the terminal draws. After ascify the
	// string is pure ASCII, so keeping only printable bytes guarantees the
	// invariant and a clean hard wrap at the window width.
	s = stripControl(s)
	m.inputValue = m.inputValue[:m.inputCursor] + s + m.inputValue[m.inputCursor:]
	m.inputCursor += len(s)
	return m
}

// stripControl removes ASCII control characters (bytes < 0x20 and DEL) from s.
// s is expected to already be pure ASCII (post-ascify).
func stripControl(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 0x20 && c != 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Word boundary helpers

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func wordStartBefore(s string, pos int) int {
	for pos > 0 && !isWordChar(s[pos-1]) {
		pos--
	}
	for pos > 0 && isWordChar(s[pos-1]) {
		pos--
	}
	return pos
}

func wordEndAfter(s string, pos int) int {
	for pos < len(s) && !isWordChar(s[pos]) {
		pos++
	}
	for pos < len(s) && isWordChar(s[pos]) {
		pos++
	}
	return pos
}

// adjustInputScroll is a compatibility method (viewport handles this now)
