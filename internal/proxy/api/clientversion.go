package api

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/joshw/zephyrlily/internal/version"
)

// What the proxy reports to Lily as its version ("#$# client zlily ...") is
// not just its own build but everything driving the session through it:
//
//	0.19.1 tui tui-web-0.19.0 mechajosh-0.1.0
//
// The first word is the proxy's version, then the UIs, then the bots. A client
// built at the proxy's own version — the usual case, since the TUI and proxy
// ship as one binary — is listed by name alone, so only a mismatch spends
// characters on a version. The server truncates the string, which is why it is
// kept this terse.
//
// A WebSocket client names itself on the /ws URL with ui=<name>-<version> or
// bot=<name>-<version>. One that says nothing (an older build) is still
// counted, as "unknown", so the report covers every connected client.

// clientGoneGrace is how long a departed client stays in the report. A client
// reconnecting after a network blip drops its socket and opens another a
// moment later; without the grace Lily would be told it left and came back.
var clientGoneGrace = 2 * time.Second

// maxClientIdent bounds the name a client may give itself.
const maxClientIdent = 64

// clientIdent returns how the client behind a /ws request identifies itself,
// tagged with its kind (e.g. "ui:tui-0.19.1"); see formatClientVersion.
func clientIdent(q url.Values) string {
	if v := sanitizeIdent(q.Get("bot")); v != "" {
		return "bot:" + v
	}
	if v := sanitizeIdent(q.Get("ui")); v != "" {
		return "ui:" + v
	}
	return "ui:unknown"
}

// sanitizeIdent keeps only characters that are safe inside the quoted version
// string sent to Lily, so a client cannot end the quote or the line early.
func sanitizeIdent(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case strings.ContainsRune("._+-", r):
			return r
		}
		return -1
	}, s)
	if len(s) > maxClientIdent {
		s = s[:maxClientIdent]
	}
	return s
}

// formatClientVersion builds the version report for a proxy at proxyVer from
// the connected clients' idents: the proxy's version first, then UIs, then
// bots, each sorted, duplicates collapsed. The ui:/bot: tags only order the
// list; they are not printed, as tui and tui-web are the only UIs.
func formatClientVersion(proxyVer string, idents []string) string {
	seen := make(map[string]bool, len(idents))
	var uis, bots []string
	for _, id := range idents {
		kind, name, _ := strings.Cut(id, ":")
		name = strings.TrimSuffix(name, "-"+proxyVer)
		if seen[name] {
			continue
		}
		seen[name] = true
		if kind == "bot" {
			bots = append(bots, name)
		} else {
			uis = append(uis, name)
		}
	}
	sort.Strings(uis)
	sort.Strings(bots)
	parts := append([]string{proxyVer}, uis...)
	parts = append(parts, bots...)
	return strings.Join(parts, " ")
}

// reportClientVersion recomputes the version report from the current
// subscribers and hands it to the Lily connection, which sends it if it
// changed. verMu makes compute-and-set atomic, so a delayed report cannot
// overwrite a newer one with a stale subscriber list.
func (sess *Session) reportClientVersion() {
	sess.verMu.Lock()
	defer sess.verMu.Unlock()

	sess.subsMu.Lock()
	idents := make([]string, 0, len(sess.subscribers))
	for c := range sess.subscribers {
		idents = append(idents, c.ident)
	}
	sess.subsMu.Unlock()

	sess.conn.SetClientVersion(formatClientVersion(version.String(), idents))
}
