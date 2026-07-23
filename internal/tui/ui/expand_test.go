package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDestination(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"alice;hello there", "alice"},
		{"alice, bob: hey", "alice, bob"},
		{"  cafe ; trimmed  ", "cafe"},
		{"no separator here", ""},
		{"/who", ""},         // command prefix
		{"$review", ""},      // command prefix
		{"?help", ""},        // command prefix
		{"%options", ""},     // command prefix
		{"#123", ""},         // command prefix
		{";leading sep", ""}, // idx <= 0
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			assert.Equal(t, tt.want, parseDestination(tt.line))
		})
	}
}

func TestNameFromEntities(t *testing.T) {
	entities := map[string]interface{}{
		"#1": map[string]interface{}{"name": "Alice"},
		"#2": map[string]interface{}{"name": ""},       // empty name → fall back
		"#3": map[string]interface{}{"other": "field"}, // no name key → fall back
	}

	assert.Equal(t, "Alice", nameFromEntities(entities, "#1"))
	assert.Equal(t, "#2", nameFromEntities(entities, "#2"))
	assert.Equal(t, "#3", nameFromEntities(entities, "#3"))
	assert.Equal(t, "#9", nameFromEntities(entities, "#9"), "unknown handle falls back to itself")
	assert.Equal(t, "#1", nameFromEntities(nil, "#1"), "nil map falls back to handle")
}

func TestTrackIncomingPrivate(t *testing.T) {
	// Whoami is #me; Alice (#1) is a user.
	m := Model{state: &api.StateResponse{Whoami: "#me"}}
	m = m.trackIncomingPrivate(map[string]interface{}{
		"source":   "#1",
		"recips":   []interface{}{"#me"},
		"entities": map[string]interface{}{"#1": map[string]interface{}{"name": "Alice", "kind": "user"}},
	})
	assert.Equal(t, "Alice", m.expandSender, "private sender becomes the ':' recall")
	assert.Equal(t, []string{"Alice"}, m.pastSends, "private sender becomes the Tab default")
}

// TestExpandKeyAtWrapBoundaryResizesViewport is a regression test for a
// display-corruption report: typing near the right edge of the terminal
// intermittently left stray/duplicated characters that survived backspacing.
// The recipient-syntax keys (';', ':', ',', '=') route through
// handleExpandKey via a branch in handleNormalKey that returned early,
// skipping the maybeResizeViewport call every other input-mutating path
// makes. If one of those keys happens to be the character that pushes the
// input across the 1-line/2-line wrap boundary, the viewport height goes
// stale relative to the input area's new height — a real desync between what
// the app renders and what actually fits, at exactly the "wrap edge" the
// report described, that plain characters crossing the same boundary don't
// trigger.
func TestExpandKeyAtWrapBoundaryResizesViewport(t *testing.T) {
	logChan, _ := NewLogger()
	base := New(client.New(""), logChan)
	base.authMode = false
	base = sizeTo(t, base, 20, 10) // firstWidth = 20 (no prompt)

	expectedHeightFor := func(m Model) int {
		return m.height - 1 - m.calculateInputHeight()
	}

	send := func(m Model, r rune) Model {
		upd, _ := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		return upd.(Model)
	}

	// 19 chars: n=len+1=20 <= firstWidth=20, so still a single input line.
	// The 20th character is the one that must cross into a second line.
	m := base
	m.inputValue = strings.Repeat("a", 19)
	m.inputCursor = 19
	m = m.maybeResizeViewport()
	require.Equal(t, 1, m.calculateInputHeight(), "fixture must start on a single input line")
	require.Equal(t, expectedHeightFor(m), m.viewport.Height(), "fixture must start in sync")

	for _, r := range []rune{'x', ';', ':', ',', '='} {
		t.Run(string(r), func(t *testing.T) {
			got := send(m, r)
			require.Equal(t, 20, len(got.inputValue))
			require.Equal(t, 2, got.calculateInputHeight(), "20 chars should need 2 input lines")
			assert.Equal(t, expectedHeightFor(got), got.viewport.Height(),
				"viewport must resize to match the taller input area")
		})
	}
}
