package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/joshw/zephyrlily/internal/tui/client"
)

// The "Remember password" box is gated on credsStorable, which is false in the
// browser build (there is no home directory and no keyring). These pin the
// native side of that gate: the browser side is covered by
// TestBrowser_NoStoredTokenShowsLogin, which asserts the box is absent.

func authDialog(t *testing.T) string {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.width, m.height = 100, 30
	return m.View().Content
}

func TestAuthDialogOffersToRememberWhenAStoreExists(t *testing.T) {
	if !credsStorable {
		t.Skip("this build has nowhere to store a password")
	}
	view := authDialog(t)
	if !strings.Contains(view, "Remember password") {
		t.Error("the box is missing on a build that can store a password")
	}
	// Space is only meaningful when there is a box to toggle.
	if !strings.Contains(view, "Space: toggle") {
		t.Error("the hint should mention Space when the box is present")
	}
}

func TestAuthDialogHidesRememberWhenNothingCanStore(t *testing.T) {
	if credsStorable {
		t.Skip("this build can store a password")
	}
	view := authDialog(t)
	if strings.Contains(view, "Remember password") {
		t.Error("offered to remember a password with nowhere to put it")
	}
	if strings.Contains(view, "Space: toggle") {
		t.Error("the hint mentions Space, but there is no box to toggle")
	}
}

// Tab must not reach a field that is not drawn: cycling onto an invisible line
// gives a dialog that silently swallows keystrokes.
func TestAuthFieldCountMatchesTheDialog(t *testing.T) {
	n := authFieldCount()
	if credsStorable && n != 3 {
		t.Errorf("authFieldCount() = %d, want 3 when the box is shown", n)
	}
	if !credsStorable && n != 2 {
		t.Errorf("authFieldCount() = %d, want 2 when the box is hidden", n)
	}

	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	for i := 0; i < 2*n; i++ {
		model, _ := m.handleAuthKey(tea.KeyPressMsg{Code: tea.KeyTab})
		m = model.(Model)
		if m.authField >= n {
			t.Fatalf("Tab reached field %d, but the dialog only draws %d", m.authField, n)
		}
	}
}
