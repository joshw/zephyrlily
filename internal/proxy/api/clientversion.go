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
//	proxy:0.19.1  ui:tui-0.19.1  ui:tui-web-0.19.1  bot:zlilybot-0.1.0
//
// A WebSocket client names itself on the /ws URL with ui=<name>-<version> or
// bot=<name>-<version>. One that says nothing (an older build) is still
// counted, as ui:unknown, so the report covers every connected client.

// clientGoneGrace is how long a departed client stays in the report. A client
// reconnecting after a network blip drops its socket and opens another a
// moment later; without the grace Lily would be told it left and came back.
var clientGoneGrace = 2 * time.Second

// maxClientIdent bounds the name a client may give itself.
const maxClientIdent = 64

// clientIdent returns how the client behind a /ws request identifies itself,
// as the tag it takes in the version report (e.g. "ui:tui-0.19.1").
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

// formatClientVersion builds the version report from the connected clients'
// idents: the proxy first, then UIs, then bots, each sorted, duplicates
// collapsed.
func formatClientVersion(idents []string) string {
	seen := make(map[string]bool, len(idents))
	var uis, bots []string
	for _, id := range idents {
		if seen[id] {
			continue
		}
		seen[id] = true
		if strings.HasPrefix(id, "bot:") {
			bots = append(bots, id)
		} else {
			uis = append(uis, id)
		}
	}
	sort.Strings(uis)
	sort.Strings(bots)
	parts := append([]string{"proxy:" + version.String()}, uis...)
	parts = append(parts, bots...)
	return strings.Join(parts, "  ")
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

	sess.conn.SetClientVersion(formatClientVersion(idents))
}
