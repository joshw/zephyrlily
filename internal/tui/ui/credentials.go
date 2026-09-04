package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/joshw/zephyrlily/internal/cmdarg"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/creds"
)

// Saved credentials, from the TUI's side. The stores themselves live in
// internal/tui/creds; what is here is when zlily reads them (once, while the
// login dialog is opening), when it writes them (only when the user says so),
// and what it tells the user about where their password went.
//
// This is client-side on purpose: the proxy is multi-user, and in "zlily
// client" mode the credential belongs to the person at the terminal, not to the
// machine holding the session.

// credsLoadedMsg carries what the stores had for this server. It arrives while
// the dialog is up, so everything in it is a suggestion the user can overtype.
type credsLoadedMsg struct {
	host     string
	username string
	password string
	from     creds.Location
	warn     string
}

// credsSavedMsg reports where a password ended up. where is the whole point of
// the message: "saved to your keyring" and "saved to a file anyone running as
// you can read" are different promises, and the user gets told which they got.
type credsSavedMsg struct {
	where creds.Location
	err   error
}

// credsForgotMsg reports a removal. rejected marks the automatic one that
// follows a stored password Lily turned down, which reads differently from a
// removal the user asked for.
type credsForgotMsg struct {
	account  string
	removed  []creds.Location
	rejected bool
	err      error
}

// loadCredsCmd asks the proxy which Lily server it fronts, then looks up who
// last logged in there and whether a password was saved for them.
//
// The Lily address has to come from the proxy: in combined mode zlily's own
// proxy takes a fresh ephemeral port every run, so the proxy address is not a
// stable key for anything. A proxy too old to answer /info simply yields no
// prefill — the dialog then behaves exactly as it always did.
func loadCredsCmd(c *client.Client) tea.Cmd {
	return func() tea.Msg {
		info, err := c.Info()
		if err != nil || info.LilyAddr == "" {
			return credsLoadedMsg{}
		}
		user := creds.LastUser(info.LilyAddr)
		if user == "" {
			return credsLoadedMsg{host: info.LilyAddr}
		}
		password, from, warn := creds.Lookup(info.LilyAddr, user)
		return credsLoadedMsg{
			host:     info.LilyAddr,
			username: user,
			password: password,
			from:     from,
			warn:     warn,
		}
	}
}

// rememberUserCmd records the username that just worked. A username is not a
// secret, so this happens on every successful login without being asked for: it
// is what puts the cursor on the password field next time instead of an empty
// username field.
func rememberUserCmd(host, user string) tea.Cmd {
	return func() tea.Msg {
		_ = creds.RememberUser(host, user)
		return nil
	}
}

// saveCredsCmd stores a password. It runs as a command rather than inline
// because the keyring is a D-Bus round trip on Linux and a Keychain call on
// macOS; neither belongs on the path that draws the next frame.
func saveCredsCmd(host, user, password string) tea.Cmd {
	return func() tea.Msg {
		where, err := creds.Save(host, user, password)
		return credsSavedMsg{where: where, err: err}
	}
}

// forgetCredsCmd removes a stored password from both stores. rejected is
// carried through so the resulting line can explain itself.
func forgetCredsCmd(host, user string, rejected bool) tea.Cmd {
	return func() tea.Msg {
		removed, err := creds.Forget(host, user)
		return credsForgotMsg{
			account:  accountName(host, user),
			removed:  removed,
			rejected: rejected,
			err:      err,
		}
	}
}

func accountName(host, user string) string {
	if host == "" {
		return user
	}
	return user + "@" + host
}

// applyCredsLoaded prefills the dialog from the stores. It only ever fills a
// field the user has left empty: the lookup can finish after someone has
// started typing, and overwriting what they typed would be worse than not
// helping at all.
func (m Model) applyCredsLoaded(msg credsLoadedMsg) Model {
	m.credsHost = msg.host
	if msg.warn != "" {
		m = m.noteCreds(msg.warn, true)
	}
	if !m.authMode || m.authInProgress || msg.username == "" {
		return m
	}

	if m.usernameInput.Value() == "" {
		m.usernameInput.SetValue(msg.username)
		m.authUsername = msg.username
		// Nothing left to type in the username field, so start on the password.
		m = m.focusAuthField(1)
	}
	if msg.password != "" && m.passwordInput.Value() == "" {
		m.passwordInput.SetValue(msg.password)
		m.credsFrom = msg.from
		m.credsPrefilled = msg.password
		// The password is already stored; the box reflects that, and clearing it
		// is how the user asks for it to be forgotten.
		m.authRemember = true
	}
	return m
}

// credsAfterLogin is the credential bookkeeping that follows a successful
// login: remember the username, and honour the "remember password" box —
// storing the password when it is ticked and something changed, removing a
// stored one when it has been cleared.
func (m Model) credsAfterLogin(username, password string) tea.Cmd {
	if m.credsHost == "" || username == "" {
		return nil
	}
	cmds := []tea.Cmd{rememberUserCmd(m.credsHost, username)}
	switch {
	case m.authRemember && (password != m.credsPrefilled || m.credsFrom == creds.LocationNone):
		cmds = append(cmds, saveCredsCmd(m.credsHost, username, password))
	case !m.authRemember && m.credsFrom != creds.LocationNone:
		cmds = append(cmds, forgetCredsCmd(m.credsHost, username, false))
	}
	return tea.Batch(cmds...)
}

// credsAfterRejection handles a stored password Lily turned down. It is dropped
// rather than kept: it cannot be right, and leaving it would re-prefill the
// same dead password at every launch.
func (m Model) credsAfterRejection(username, password string) (Model, tea.Cmd) {
	if m.credsFrom == creds.LocationNone || password == "" || password != m.credsPrefilled {
		return m, nil
	}
	m.credsFrom = creds.LocationNone
	m.credsPrefilled = ""
	m.authRemember = false
	return m, forgetCredsCmd(m.credsHost, username, true)
}

// applyCredsSaved and applyCredsForgot turn a finished store operation into
// something the user can read.
func (m Model) applyCredsSaved(msg credsSavedMsg) Model {
	if msg.err != nil {
		return m.noteCreds("Could not save the password: "+msg.err.Error(), true)
	}
	m.credsFrom = msg.where
	m.credsPrefilled = m.authPassword
	return m.noteCreds("Password saved to "+msg.where.Describe()+".", false)
}

func (m Model) applyCredsForgot(msg credsForgotMsg) Model {
	if msg.err != nil {
		return m.noteCreds("Could not remove the saved password: "+msg.err.Error(), true)
	}
	if len(msg.removed) == 0 {
		if msg.rejected {
			return m
		}
		return m.noteCreds("No password was saved for "+msg.account+".", false)
	}
	where := make([]string, 0, len(msg.removed))
	for _, loc := range msg.removed {
		where = append(where, loc.String())
	}
	list := strings.Join(where, " and ")
	if msg.rejected {
		return m.noteCreds(fmt.Sprintf(
			"Lily rejected the saved password for %s, so it has been removed from the %s. Type it below, or tick the box to save the new one.",
			msg.account, list), true)
	}
	return m.noteCreds(fmt.Sprintf("Removed the saved password for %s from the %s.", msg.account, list), false)
}

// noteCreds puts a line where the user is actually looking: in the login dialog
// while it is up, in the scrollback once past it.
func (m Model) noteCreds(line string, isErr bool) Model {
	if m.authMode {
		m.authNotice = line
		return m
	}
	kind := "text"
	if isErr {
		kind = "error"
	}
	m.output = append(m.output, OutputItem{Type: kind, Data: line})
	return m.syncViewportContent()
}

// handleCredsCommand runs %save-password and %forget-password. Both are
// client-side: the proxy never sees them, because it is not where the
// credential lives.
func (m Model) handleCredsCommand(fields []string) (Model, []string, tea.Cmd, bool) {
	if len(fields) == 0 {
		return m, nil, nil, false
	}
	switch {
	case cmdarg.Is(fields[0], "%save-password"):
		if m.credsHost == "" {
			return m, []string{"Cannot save a password: this session did not learn which Lily server it is on."}, nil, true
		}
		if m.authUsername == "" || m.authPassword == "" {
			return m, []string{"Cannot save a password: this session has none to save (it was resumed with an existing token)."}, nil, true
		}
		return m, []string{"Saving the password for " + accountName(m.credsHost, m.authUsername) + "…"},
			saveCredsCmd(m.credsHost, m.authUsername, m.authPassword), true

	case cmdarg.Is(fields[0], "%forget-password"):
		user := m.authUsername
		if len(fields) > 1 {
			user = fields[1]
		}
		if m.credsHost == "" || user == "" {
			return m, []string{"Usage: %forget-password [username]"}, nil, true
		}
		return m, nil, forgetCredsCmd(m.credsHost, user, false), true
	}
	return m, nil, nil, false
}
