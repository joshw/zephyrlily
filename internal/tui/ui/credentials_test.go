package ui

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/creds"
)

const testLilyHost = "rpi.lily.org:7777"

// isolateCreds points the credential stores at a scratch directory and, by
// default, a machine with no keyring — the state of the headless hosts zlily
// usually runs on, and the one that exercises the file path.
func isolateCreds(t *testing.T) {
	t.Helper()
	t.Setenv("ZLILY_CONFIG_DIR", t.TempDir())
	keyring.MockInitWithError(errors.New("no keyring on this host"))
}

func newAuthModel(t *testing.T) Model {
	t.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = true
	m.credsHost = testLilyHost
	return m
}

// runCmd executes cmd and returns every message it produced, flattening the
// batches that credsAfterLogin returns.
func runCmd(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	var out []tea.Msg
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			out = append(out, runCmd(t, c)...)
		}
	case nil:
	default:
		out = append(out, msg)
	}
	return out
}

func findMsg[T tea.Msg](msgs []tea.Msg) (T, bool) {
	for _, msg := range msgs {
		if typed, ok := msg.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

// The point of the whole feature: a returning user should be one keystroke from
// logged in, with the cursor already on the field they might want to change.
func TestSavedCredentialsPrefillTheDialog(t *testing.T) {
	m := newAuthModel(t)
	m = m.applyCredsLoaded(credsLoadedMsg{
		host:     testLilyHost,
		username: "josh",
		password: "hunter2",
		from:     creds.LocationFile,
	})

	assert.Equal(t, "josh", m.usernameInput.Value())
	assert.Equal(t, "hunter2", m.passwordInput.Value())
	assert.Equal(t, authFieldPassword, m.authField, "nothing left to type in the username field")
	assert.True(t, m.authRemember, "a password that came from a store is already remembered")

	upd, cmd := m.handleAuthKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd, "Enter on a prefilled dialog must log in")
	assert.True(t, upd.(Model).authInProgress)
	assert.Equal(t, "hunter2", upd.(Model).authPassword)
}

// The lookup can land after the user has started typing; what they typed wins.
func TestPrefillNeverOvertypesTheUser(t *testing.T) {
	m := newAuthModel(t)
	m.usernameInput.SetValue("someone-else")
	m.passwordInput.SetValue("typed")

	m = m.applyCredsLoaded(credsLoadedMsg{
		host:     testLilyHost,
		username: "josh",
		password: "hunter2",
		from:     creds.LocationFile,
	})

	assert.Equal(t, "someone-else", m.usernameInput.Value())
	assert.Equal(t, "typed", m.passwordInput.Value())
	assert.False(t, m.authRemember)
}

// A username with no saved password still saves the typing of the username, and
// still puts the cursor where the work is.
func TestRememberedUsernameAloneFocusesThePassword(t *testing.T) {
	m := newAuthModel(t)
	m = m.applyCredsLoaded(credsLoadedMsg{host: testLilyHost, username: "josh"})

	assert.Equal(t, "josh", m.usernameInput.Value())
	assert.Empty(t, m.passwordInput.Value())
	assert.Equal(t, authFieldPassword, m.authField)
	assert.False(t, m.authRemember, "nothing was stored, so the box starts clear")
}

func TestRememberBoxKeys(t *testing.T) {
	m := newAuthModel(t)
	m = m.focusAuthField(authFieldRemember)

	upd, _ := m.handleAuthKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = upd.(Model)
	assert.True(t, m.authRemember, "space ticks the box")

	upd, _ = m.handleAuthKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	m = upd.(Model)
	assert.False(t, m.authRemember, "space clears it again")

	// Space is still a space in the fields above it.
	m = m.focusAuthField(authFieldPassword)
	upd, _ = m.handleAuthKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, " ", upd.(Model).passwordInput.Value())

	// Tab wraps around all three fields rather than the original two.
	m = newAuthModel(t)
	for _, want := range []int{authFieldPassword, authFieldRemember, authFieldUsername} {
		upd, _ := m.handleAuthKey(tea.KeyPressMsg{Code: tea.KeyTab})
		m = upd.(Model)
		assert.Equal(t, want, m.authField)
	}
}

func TestTickedBoxSavesThePasswordAfterLogin(t *testing.T) {
	isolateCreds(t)
	m := newAuthModel(t)
	m.authRemember = true

	msgs := runCmd(t, m.credsAfterLogin("josh", "hunter2"))
	saved, ok := findMsg[credsSavedMsg](msgs)
	require.True(t, ok, "a ticked box must save the password")
	require.NoError(t, saved.err)
	assert.Equal(t, creds.LocationFile, saved.where, "no keyring on this host, so the file")

	pw, where, _ := creds.Lookup(testLilyHost, "josh")
	assert.Equal(t, "hunter2", pw)
	assert.Equal(t, creds.LocationFile, where)

	// The username is remembered either way, which is what prefills the dialog.
	assert.Equal(t, "josh", creds.LastUser(testLilyHost))
}

func TestClearedBoxForgetsAStoredPassword(t *testing.T) {
	isolateCreds(t)
	_, err := creds.Save(testLilyHost, "josh", "hunter2")
	require.NoError(t, err)

	m := newAuthModel(t)
	m = m.applyCredsLoaded(credsLoadedMsg{
		host: testLilyHost, username: "josh", password: "hunter2", from: creds.LocationFile,
	})
	m.authRemember = false // the user cleared the box before logging in

	msgs := runCmd(t, m.credsAfterLogin("josh", "hunter2"))
	forgot, ok := findMsg[credsForgotMsg](msgs)
	require.True(t, ok, "clearing the box removes what was stored")
	require.NoError(t, forgot.err)
	assert.False(t, forgot.rejected)

	pw, _, _ := creds.Lookup(testLilyHost, "josh")
	assert.Empty(t, pw)
}

func TestUntouchedSavedPasswordIsNotRewritten(t *testing.T) {
	isolateCreds(t)
	m := newAuthModel(t)
	m = m.applyCredsLoaded(credsLoadedMsg{
		host: testLilyHost, username: "josh", password: "hunter2", from: creds.LocationFile,
	})

	msgs := runCmd(t, m.credsAfterLogin("josh", "hunter2"))
	_, saved := findMsg[credsSavedMsg](msgs)
	_, forgot := findMsg[credsForgotMsg](msgs)
	assert.False(t, saved, "an unchanged stored password needs no write")
	assert.False(t, forgot)
}

// A stored password Lily turns down is dropped rather than offered again at
// every launch — and the user is told, in the dialog they are looking at.
func TestRejectedSavedPasswordIsDropped(t *testing.T) {
	isolateCreds(t)
	_, err := creds.Save(testLilyHost, "josh", "stale")
	require.NoError(t, err)

	m := newAuthModel(t)
	m = m.applyCredsLoaded(credsLoadedMsg{
		host: testLilyHost, username: "josh", password: "stale", from: creds.LocationFile,
	})
	m.authInProgress = true

	upd, cmd := m.update(authResultMsg{username: "josh", password: "stale", err: client.ErrAuthFailed})
	m = upd.(Model)
	assert.True(t, m.authMode, "a rejection re-prompts")
	assert.Empty(t, m.passwordInput.Value(), "the dead password is cleared out of the field")
	assert.Equal(t, creds.LocationNone, m.credsFrom)

	forgot, ok := findMsg[credsForgotMsg](runCmd(t, cmd))
	require.True(t, ok)
	assert.True(t, forgot.rejected)

	pw, _, _ := creds.Lookup(testLilyHost, "josh")
	assert.Empty(t, pw, "a password the server rejects is not worth keeping")

	upd, _ = m.update(forgot)
	assert.Contains(t, upd.(Model).authNotice, "removed",
		"the dialog explains why the prefill vanished")
}

// A password typed by hand and never saved must not be removed on rejection —
// there is nothing to remove, and the message would be a lie.
func TestRejectedTypedPasswordTouchesNoStore(t *testing.T) {
	isolateCreds(t)
	m := newAuthModel(t)
	m.authInProgress = true

	_, cmd := m.update(authResultMsg{username: "josh", password: "typo", err: client.ErrAuthFailed})
	_, ok := findMsg[credsForgotMsg](runCmd(t, cmd))
	assert.False(t, ok)
}

func TestSavePasswordCommand(t *testing.T) {
	isolateCreds(t)
	m := newAuthModel(t)
	m.authMode = false
	m.authUsername = "josh"
	m.authPassword = "hunter2"

	m, out, cmd, handled := m.handleCredsCommand([]string{"%save-password"})
	require.True(t, handled)
	assert.Contains(t, strings.Join(out, " "), "josh@"+testLilyHost)

	saved, ok := findMsg[credsSavedMsg](runCmd(t, cmd))
	require.True(t, ok)
	require.NoError(t, saved.err)

	pw, _, _ := creds.Lookup(testLilyHost, "josh")
	assert.Equal(t, "hunter2", pw)

	// And the line the user sees names the store, not just "saved".
	m = m.applyCredsSaved(saved)
	require.NotEmpty(t, m.output)
	last, _ := m.output[len(m.output)-1].Data.(string)
	assert.Contains(t, last, "credentials")
}

func TestForgetPasswordCommand(t *testing.T) {
	isolateCreds(t)
	_, err := creds.Save(testLilyHost, "josh", "hunter2")
	require.NoError(t, err)

	m := newAuthModel(t)
	m.authMode = false
	m.authUsername = "josh"

	m, _, cmd, handled := m.handleCredsCommand([]string{"%forget-password"})
	require.True(t, handled)
	forgot, ok := findMsg[credsForgotMsg](runCmd(t, cmd))
	require.True(t, ok)
	assert.Equal(t, []creds.Location{creds.LocationFile}, forgot.removed)

	pw, _, _ := creds.Lookup(testLilyHost, "josh")
	assert.Empty(t, pw)

	// Saying so beats silence: the user asked for something to happen.
	m = m.applyCredsForgot(forgot)
	last, _ := m.output[len(m.output)-1].Data.(string)
	assert.Contains(t, last, "credentials file")
}

// The whole prefill hangs off the proxy telling the client which Lily server it
// fronts: saved passwords are keyed by that, and the embedded proxy's own
// address is a fresh ephemeral port on every run.
func TestLoadCredsCmdKeysOffTheProxysLilyAddress(t *testing.T) {
	isolateCreds(t)
	_, err := creds.Save(testLilyHost, "josh", "hunter2")
	require.NoError(t, err)
	require.NoError(t, creds.RememberUser(testLilyHost, "josh"))

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/info", r.URL.Path)
		_ = json.NewEncoder(w).Encode(api.InfoResponse{LilyAddr: testLilyHost})
	}))
	defer proxy.Close()

	msg := loadCredsCmd(client.New(strings.TrimPrefix(proxy.URL, "http://")))().(credsLoadedMsg)
	assert.Equal(t, testLilyHost, msg.host)
	assert.Equal(t, "josh", msg.username)
	assert.Equal(t, "hunter2", msg.password)
	assert.Equal(t, creds.LocationFile, msg.from)
}

// A proxy too old to answer /info (or one that is not up yet) must cost nothing
// but the prefill: the dialog still works, typed by hand as it always was.
func TestLoadCredsCmdSurvivesAProxyWithoutInfo(t *testing.T) {
	isolateCreds(t)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer proxy.Close()

	msg := loadCredsCmd(client.New(strings.TrimPrefix(proxy.URL, "http://")))().(credsLoadedMsg)
	assert.Empty(t, msg.host)
	assert.Empty(t, msg.username)
}

// recordedKeys is what a %debug snapshot would print from the input-event ring.
func recordedKeys(m Model) string {
	var b strings.Builder
	for _, e := range m.inputEvents.entries() {
		b.WriteString(e.desc)
		b.WriteByte('\n')
	}
	return b.String()
}

// %debug snapshot is written to be attached to bug reports, so the password
// being typed must not be in it.
func TestSnapshotRedactsKeysTypedIntoTheLoginDialog(t *testing.T) {
	m := newAuthModel(t)
	m.recordKeyEvent(tea.KeyPressMsg{Code: 's', Text: "s"})
	m.recordKeyEvent(tea.KeyPressMsg{Code: tea.KeyTab})

	logged := recordedKeys(m)
	assert.NotContains(t, logged, `text="s"`, "a typed password character must not be recorded")
	assert.Contains(t, logged, "redacted")
	assert.Contains(t, logged, "mode=auth")

	// Outside the dialog the ring keeps its full detail, which is what makes it
	// useful for input bugs.
	m.authMode = false
	m.recordKeyEvent(tea.KeyPressMsg{Code: 's', Text: "s"})
	assert.Contains(t, recordedKeys(m), `text="s"`)
}
