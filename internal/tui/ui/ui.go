package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
)

// OutputItem represents a single item in the output buffer.
// It stores raw data so it can be reformatted when the window size changes.
type OutputItem struct {
	Type string      // "text", "event", "command", "error", "input"
	Data interface{} // raw data (string, map[string]interface{}, []string)
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

	// Debug view
	debugMode bool
	debugMsgs []string // raw JSON messages

	// Scroll state
	scrollOffset      int // lines scrolled back in output
	debugScrollOffset int // lines scrolled back in debug
}

// New creates a Model wired to the given proxy client.
// state is the initial state snapshot fetched at startup.
func New(c *client.Client, state *api.StateResponse) Model {
	// Create initial output with logo
	logoLines := formatLogo()
	output := make([]OutputItem, 0, len(logoLines))
	for _, line := range logoLines {
		output = append(output, OutputItem{Type: "text", Data: line})
	}

	return Model{
		client: c,
		state:  state,
		output: output,
		prompt: "",
	}
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

// Init starts the event listener.
func (m Model) Init() tea.Cmd {
	return listenCmd(m.client)
}

// Update handles messages and user input.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+d":
			return m, tea.Quit

		case "ctrl+t":
			// Toggle debug mode
			m.debugMode = !m.debugMode

		case "pgup":
			// Scroll up
			if m.debugMode {
				m.debugScrollOffset += m.height - 3
			} else {
				m.scrollOffset += m.height - 3
			}

		case "pgdn":
			// Scroll down
			if m.debugMode {
				m.debugScrollOffset -= m.height - 3
				if m.debugScrollOffset < 0 {
					m.debugScrollOffset = 0
				}
			} else {
				m.scrollOffset -= m.height - 3
				if m.scrollOffset < 0 {
					m.scrollOffset = 0
				}
			}
		}

		switch msg.Type {
		case tea.KeyEnter:
			if m.input == "" {
				break
			}
			line := m.input
			m.input = ""
			m.output = append(m.output, OutputItem{Type: "input", Data: line})

			// Auto-scroll to bottom when sending a command
			m.scrollOffset = 0

			// Log outgoing command to debug (formatted)
			cmdMsg := api.WSClientMsg{Type: "command", Text: line}
			if jsonBytes, err := json.MarshalIndent(cmdMsg, "", "  "); err == nil {
				lines := strings.Split(string(jsonBytes), "\n")
				m.debugMsgs = append(m.debugMsgs, "SEND:")
				m.debugMsgs = append(m.debugMsgs, lines...)
			}
			m.debugScrollOffset = 0 // also scroll debug to bottom

			if err := m.client.Send(line); err != nil {
				m.output = append(m.output, OutputItem{Type: "error", Data: err.Error()})
			}

		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}

		case tea.KeySpace:
			m.input += " "

		default:
			if msg.Type == tea.KeyRunes {
				m.input += string(msg.Runes)
			}
		}

	case serverEventMsg:
		if msg.msg == nil {
			m.output = append(m.output, OutputItem{Type: "error", Data: "--- disconnected ---"})
			return m, nil
		}

		// Auto-scroll to bottom if we were already there
		wasAtBottom := m.scrollOffset == 0
		wasDebugAtBottom := m.debugScrollOffset == 0

		// Log incoming message to debug (pretty-printed and wrapped)
		if jsonBytes, err := json.MarshalIndent(msg.msg, "", "  "); err == nil {
			lines := strings.Split(string(jsonBytes), "\n")
			m.debugMsgs = append(m.debugMsgs, "RECV:")
			m.debugMsgs = append(m.debugMsgs, lines...)
		}

		m = m.handleProxy(msg.msg)

		// Keep scrolled to bottom if we were there before new messages
		if wasAtBottom {
			m.scrollOffset = 0
		}
		if wasDebugAtBottom {
			m.debugScrollOffset = 0
		}

		return m, listenCmd(m.client)
	}

	return m, nil
}

// handleProxy incorporates a proxy message into the model.
func (m Model) handleProxy(msg *api.WSServerMsg) Model {
	switch msg.Type {
	case "text":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			if text, ok := d["text"].(string); ok && text != "" {
				m.output = append(m.output, OutputItem{Type: "text", Data: text})
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
					m.output = append(m.output, OutputItem{Type: "command", Data: lines})
				}
			}
		}

	case "event":
		if d, ok := msg.Data.(map[string]interface{}); ok {
			m.output = append(m.output, OutputItem{Type: "event", Data: d})

			// Update local state for events that affect the current user
			if m.state != nil {
				event, _ := d["event"].(string)
				source, _ := d["source"].(string)

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
			m.output = append(m.output, OutputItem{Type: "error", Data: e})
		}
	}
	return m
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
			return []string{inputPrefixStyle.Render("> ") + line}
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

	// Calculate padding
	totalLen := len(left) + len(right)
	padding := ""
	if totalLen < m.width {
		padding = strings.Repeat(" ", m.width-totalLen)
	}

	content := left + padding + right
	return statusBarStyle.Width(m.width).Render(content)
}

// viewNormal renders the standard UI (output + status + input).
func (m Model) viewNormal() string {
	// Render all output items into lines
	var allLines []string
	for _, item := range m.output {
		allLines = append(allLines, m.renderOutputItem(item)...)
	}

	// Output area: last N lines that fit above the input bar.
	outputLines := m.height - 2 // leave room for status and input

	// Clamp scroll offset to valid range
	maxScroll := len(allLines) - outputLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollOffset > maxScroll {
		m.scrollOffset = maxScroll
	}

	// Apply scroll offset
	end := len(allLines) - m.scrollOffset
	start := end - outputLines
	if start < 0 {
		start = 0
	}
	if end > len(allLines) {
		end = len(allLines)
	}
	visible := allLines[start:end]

	var sb strings.Builder
	for _, line := range visible {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	// pad to fill the output area
	for i := len(visible); i < outputLines; i++ {
		sb.WriteByte('\n')
	}

	// Status line
	extraInfo := ""
	if m.scrollOffset > 0 {
		extraInfo = fmt.Sprintf("[-%d]", m.scrollOffset)
	}
	status := m.formatStatusBar(extraInfo)
	sb.WriteString(status)
	sb.WriteByte('\n')

	// Input line
	sb.WriteString(promptStyle.Render(m.prompt) + " " + m.input + "_")

	return sb.String()
}

// viewWithDebug renders a split view: left=output, right=debug JSON.
func (m Model) viewWithDebug() string {
	leftWidth := m.width / 2
	rightWidth := m.width - leftWidth - 1 // -1 for separator

	outputLines := m.height - 2

	// Render all output items into lines
	var allOutputLines []string
	for _, item := range m.output {
		allOutputLines = append(allOutputLines, m.renderOutputItem(item)...)
	}

	// Clamp output scroll offset
	maxScrollOutput := len(allOutputLines) - outputLines
	if maxScrollOutput < 0 {
		maxScrollOutput = 0
	}
	if m.scrollOffset > maxScrollOutput {
		m.scrollOffset = maxScrollOutput
	}

	// Apply scroll offset to output
	endOutput := len(allOutputLines) - m.scrollOffset
	startOutput := endOutput - outputLines
	if startOutput < 0 {
		startOutput = 0
	}
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
	extraInfo := ""
	if m.scrollOffset > 0 || m.debugScrollOffset > 0 {
		extraInfo = fmt.Sprintf("[out:-%d dbg:-%d]", m.scrollOffset, m.debugScrollOffset)
	}
	status := m.formatStatusBar(extraInfo)
	sb.WriteString(status)
	sb.WriteByte('\n')

	// Input line
	sb.WriteString(promptStyle.Render(m.prompt) + " " + m.input + "_")

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
