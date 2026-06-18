package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// editMeta carries the identity of what is being edited.
type editMeta struct {
	contentType string // "info" or "memo"
	target      string // "me" or a handle
	name        string // memo name; empty for info
}

// editorFetchResultMsg is delivered when the async content fetch completes.
type editorFetchResultMsg struct {
	meta  editMeta
	lines []string
	err   error
}

// editorSaveResultMsg is delivered when the async save completes.
type editorSaveResultMsg struct {
	meta editMeta
	err  error
}

// fetchContentCmd returns a Cmd that fetches existing content and then enters
// edit mode. If the fetch fails (e.g. no existing content) it opens a blank editor.
func (m Model) fetchContentCmd(meta editMeta) tea.Cmd {
	return func() tea.Msg {
		lines, err := m.client.FetchContent(meta.contentType, meta.target, meta.name)
		return editorFetchResultMsg{meta: meta, lines: lines, err: err}
	}
}

// saveContentCmd returns a Cmd that stores content on the server.
func (m Model) saveContentCmd(meta editMeta, content string) tea.Cmd {
	lines := strings.Split(content, "\n")
	return func() tea.Msg {
		err := m.client.StoreContent(meta.contentType, meta.target, meta.name, lines)
		return editorSaveResultMsg{meta: meta, err: err}
	}
}

// newEditorModel creates a configured textarea for editing.
func newEditorModel(width, height int, content string) textarea.Model {
	ta := textarea.New()
	ta.SetWidth(width)
	ta.SetHeight(height)
	ta.SetValue(content)
	ta.Focus()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.Cursor.Blink = false
	return ta
}

// editorTitle builds the status-bar title for the editor header.
func (m Model) editorTitle() string {
	switch m.editMeta.contentType {
	case "info":
		if m.editMeta.target == "" || m.editMeta.target == "me" {
			return "Editing your info"
		}
		return "Editing info for " + m.editMeta.target
	case "memo":
		if m.editMeta.target == "" || m.editMeta.target == "me" {
			return "Editing memo: " + m.editMeta.name
		}
		return "Editing memo \"" + m.editMeta.name + "\" on " + m.editMeta.target
	default:
		return "Editing"
	}
}

// viewEditor renders the full-screen editing overlay.
func (m Model) viewEditor() string {
	header := commandResultStyle.Render(m.editorTitle())
	footer := statusBarStyle.Width(m.width).Render("Ctrl+S  save   Esc  cancel")
	return strings.Join([]string{header, m.editor.View(), footer}, "\n")
}

// handleEditorMsg routes messages while in editor mode.
// Returns (updatedModel, cmd, handled). When handled is false the caller
// should process the message normally (e.g. server events still update state).
func (m Model) handleEditorMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+s":
			return m, m.saveContentCmd(m.editMeta, m.editor.Value()), true
		case "esc":
			m.editMode = false
			return m, nil, true
		default:
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(msg)
			return m, cmd, true
		}
	case tea.WindowSizeMsg:
		m.editor.SetWidth(msg.Width)
		m.editor.SetHeight(msg.Height - 2)
		return m, nil, true
	}
	return m, nil, false
}
