package ui

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/joshw/zephyrlily/internal/proxy/api"
)

// completionItem implements list.Item for the completion popup.
type completionItem struct {
	entity api.EntityJSON
}

func (i completionItem) Title() string {
	name := strings.ReplaceAll(i.entity.Name, " ", "_")
	if i.entity.Kind == "disc" {
		name = "-" + name
	}
	return name
}

func (i completionItem) Description() string {
	return i.entity.Kind
}

func (i completionItem) FilterValue() string {
	return i.entity.Name
}

// completionDelegate customizes the list item rendering.
type completionDelegate struct{}

func (d completionDelegate) Height() int { return 1 }

func (d completionDelegate) Spacing() int { return 0 }

func (d completionDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d completionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(completionItem)
	if !ok {
		return
	}

	name := i.Title()
	kind := i.Description()

	str := fmt.Sprintf("  %s (%s)", name, kind)
	if index == m.Index() {
		str = "> " + name + " (" + kind + ")"
		str = lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Render(str)
	}

	_, _ = fmt.Fprint(w, str)
}

// newCompletionList creates a new list.Model for completion popup.
func newCompletionList(matches []api.EntityJSON, width int) list.Model {
	items := make([]list.Item, len(matches))
	for i, e := range matches {
		items[i] = completionItem{entity: e}
	}

	// Calculate height based on number of items (max 10)
	height := len(items)
	if height > 10 {
		height = 10
	}

	// Use our custom delegate for compact single-line items
	delegate := completionDelegate{}

	l := list.New(items, delegate, width, height)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowFilter(false)
	l.SetShowHelp(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()

	// Minimal styling
	l.Styles.NoItems = lipgloss.NewStyle()

	return l
}

// showCompletionPopup initializes the completion popup with matches.
func (m Model) showCompletionPopup(matches []api.EntityJSON, token, fore string) Model {
	m.completionActive = true
	m.completionToken = token
	m.completionFore = fore
	m.completionList = newCompletionList(matches, 30)
	return m
}

// hideCompletionPopup closes the completion popup.
func (m Model) hideCompletionPopup() Model {
	m.completionActive = false
	return m
}

// acceptCompletion inserts the selected completion.
func (m Model) acceptCompletion() Model {
	if !m.completionActive {
		return m
	}

	item, ok := m.completionList.SelectedItem().(completionItem)
	if !ok {
		return m.hideCompletionPopup()
	}

	// Build the completed name
	name := strings.ReplaceAll(item.entity.Name, " ", "_")
	if item.entity.Kind == "disc" {
		name = "-" + name
	}

	// Replace token with completed name
	newPartial := m.completionFore + name
	cursor := m.inputCursor
	m.inputValue = newPartial + m.inputValue[cursor:]
	m.inputCursor = len(newPartial)

	return m.hideCompletionPopup()
}

// renderCompletionPopup renders the completion popup overlay.
func (m Model) renderCompletionPopup() string {
	if !m.completionActive {
		return ""
	}

	// Style the popup with a border
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1)

	return style.Render(m.completionList.View())
}

// handleCompletionKey handles key events when the completion popup is active.
func (m Model) handleCompletionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter", "ctrl+m", "ctrl+j", "tab":
		// Accept selection
		m = m.acceptCompletion()
		m.syncTextarea()
		m = m.maybeResizeViewport()
		return m, nil

	case "up", "ctrl+p":
		m.completionList.CursorUp()
		return m, nil

	case "down", "ctrl+n":
		m.completionList.CursorDown()
		return m, nil

	case "ctrl+c", "ctrl+g":
		// Cancel
		m = m.hideCompletionPopup()
		return m, nil

	default:
		// Any other key dismisses popup and is processed normally
		m = m.hideCompletionPopup()
		// Don't return - fall through to normal processing
	}

	// For non-handled keys, we need to continue to normal key handling
	// But we can't call handleNormalKey from here (infinite loop)
	// So we return and let Update re-dispatch
	return m, nil
}
