package ui

import (
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
)

// editMeta carries the identity of what is being edited.
type editMeta struct {
	contentType string // "info" or "memo"
	target      string // "me" or a handle
	name        string // memo name; empty for info
}

// fetchContentCmd returns a Cmd that fetches existing content and then enters edit mode.
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
func (m Model) handleEditorMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+s":
		return m, m.saveContentCmd(m.editMeta, m.editor.Value())
	case "esc":
		m.editMode = false
		return m, nil
	default:
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}
}
