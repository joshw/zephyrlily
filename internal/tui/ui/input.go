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
		// Ctrl+C quits
		if keyStr == "ctrl+c" {
			return m, tea.Quit
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
			m.debugMsgs = append(m.debugMsgs, fmt.Sprintf("[paste] keyStr=%q type=%v runes=%q", keyStr, msg.Type, string(msg.Runes)))
		}
		// Handle both pasted multi-line text (KeyRunes with multiple chars) and individual keystrokes
		if msg.Type == tea.KeyRunes {
			for _, r := range msg.Runes {
				m = m.pasteRune(r)
			}
			m.syncTextarea()
			m = m.maybeResizeViewport()
			return m, nil
		}
		// Non-rune keys: Enter/Ctrl+M/Ctrl+J treated the same as a newline rune.
		if keyStr == "enter" || keyStr == "ctrl+m" || keyStr == "ctrl+j" {
			m = m.pasteRune('\n')
			m.syncTextarea()
			m = m.maybeResizeViewport()
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
		return m, tea.Quit

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

	// Search
	case key.Matches(msg, m.keys.SearchBack):
		m = m.enterSearch(true)
	case key.Matches(msg, m.keys.SearchForward):
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
			m.debugMsgs = append(m.debugMsgs, "SEND:")
			m.debugMsgs = append(m.debugMsgs, strings.Split(string(jsonBytes), "\n")...)
		}
	}

	// Handle debug key toggle
	if strings.EqualFold(strings.TrimSpace(line), "%set debug keys") {
		m.debugKeys = !m.debugKeys
		state := "off"
		if m.debugKeys {
			state = "on"
		}
		m.output = append(m.output, OutputItem{Type: "command", Data: []string{"Key debug logging: " + state}})
		m = m.syncViewportContent()
		return m, nil
	}

	// Handle %page toggle for the viewport pager
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
			m.output = append(m.output, OutputItem{Type: "command", Data: lines})
			m = m.syncViewportContent()
			return m, cmd
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
		m.output = append(m.output, OutputItem{Type: "command", Data: []string{msg}})
		m = m.syncViewportContent()
		return m, nil
	}

	// Handle local commands
	localOutput, handled, asyncCmd := m.handleLocalCommand(line)
	if localOutput != nil {
		m.output = append(m.output, OutputItem{Type: "command", Data: localOutput})
	}
	if asyncCmd != nil {
		m = m.syncViewportContent()
		return m, asyncCmd
	}
	if !handled {
		m = m.trackOutgoingSend(line)
		if err := m.client.Send(line); err != nil {
			m.output = append(m.output, OutputItem{Type: "error", Data: err.Error()})
		}
	}

	m = m.syncViewportContent()
	return m, nil
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
	return m
}

func (m Model) searchRefresh() Model {
	if m.searchBuf == "" {
		m.inputValue = m.searchSave
		m.inputCursor = len(m.inputValue)
		m.searchIdx = -1
		return m
	}

	searchLower := strings.ToLower(m.searchBuf)

	// Search backward: check current line first, then history backward
	if m.searchBack {
		// Check current line first
		if strings.Contains(strings.ToLower(m.searchSave), searchLower) {
			m.inputValue = m.searchSave
			pos := findSubstringPos(m.searchSave, searchLower)
			m.inputCursor = pos
			m.searchIdx = -1 // Mark as using current line, not history
			return m
		}

		// Then search history backward
		for i := len(m.history) - 1; i >= 0; i-- {
			if strings.Contains(strings.ToLower(m.history[i]), searchLower) {
				m.searchIdx = i
				m.inputValue = m.history[i]
				pos := findSubstringPos(m.history[i], searchLower)
				m.inputCursor = pos
				return m
			}
		}
	} else {
		// Search forward: check current line first, then history forward
		// Check current line first
		if strings.Contains(strings.ToLower(m.searchSave), searchLower) {
			m.inputValue = m.searchSave
			pos := findSubstringPos(m.searchSave, searchLower)
			m.inputCursor = pos
			m.searchIdx = -1 // Mark as using current line, not history
			return m
		}

		// Then search history forward
		for i := 0; i < len(m.history); i++ {
			if strings.Contains(strings.ToLower(m.history[i]), searchLower) {
				m.searchIdx = i
				m.inputValue = m.history[i]
				pos := findSubstringPos(m.history[i], searchLower)
				m.inputCursor = pos
				return m
			}
		}
	}

	// No match found
	m.searchIdx = -1
	return m
}

func (m Model) searchStep(direction int) Model {
	if m.searchIdx < 0 {
		// Currently on saved line; search from history
		searchLower := strings.ToLower(m.searchBuf)

		if m.searchBack {
			// Search history backward
			for i := len(m.history) - 1; i >= 0; i-- {
				if strings.Contains(strings.ToLower(m.history[i]), searchLower) {
					m.searchIdx = i
					m.inputValue = m.history[i]
					pos := findSubstringPos(m.history[i], searchLower)
					m.inputCursor = pos
					return m
				}
			}
		} else {
			// Search history forward
			for i := 0; i < len(m.history); i++ {
				if strings.Contains(strings.ToLower(m.history[i]), searchLower) {
					m.searchIdx = i
					m.inputValue = m.history[i]
					pos := findSubstringPos(m.history[i], searchLower)
					m.inputCursor = pos
					return m
				}
			}
		}
		return m
	}

	// Currently on a history item; move to next/previous
	searchLower := strings.ToLower(m.searchBuf)
	start := m.searchIdx + direction

	if m.searchBack {
		for i := start; i >= 0; i-- {
			if strings.Contains(strings.ToLower(m.history[i]), searchLower) {
				m.searchIdx = i
				m.inputValue = m.history[i]
				pos := findSubstringPos(m.history[i], searchLower)
				m.inputCursor = pos
				return m
			}
		}
	} else {
		for i := start; i < len(m.history); i++ {
			if strings.Contains(strings.ToLower(m.history[i]), searchLower) {
				m.searchIdx = i
				m.inputValue = m.history[i]
				pos := findSubstringPos(m.history[i], searchLower)
				m.inputCursor = pos
				return m
			}
		}
	}

	return m
}

// findSubstringPos finds the byte position of the search string in text (case-insensitive)
func findSubstringPos(text, searchLower string) int {
	textLower := strings.ToLower(text)
	idx := strings.Index(textLower, searchLower)
	if idx < 0 {
		return 0
	}
	return idx
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
