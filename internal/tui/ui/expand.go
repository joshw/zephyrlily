package ui

import (
	"log/slog"
	"strings"

	"github.com/joshw/zephyrlily/internal/proxy/api"
)

const pastSendsMax = 5

// nameFromEntities looks up a display name for a handle inside the raw
// map[string]interface{} entities block that arrives with event payloads.
// Falls back to the handle itself when no name is found.
func nameFromEntities(entities map[string]interface{}, handle string) string {
	if entities != nil {
		if e, ok := entities[handle].(map[string]interface{}); ok {
			if name, ok := e["name"].(string); ok && name != "" {
				return name
			}
		}
	}
	return handle
}

// parseDestination extracts the destination portion of a Lily send line
// (the text before the first ';' or ':'). Returns "" for commands and
// bare lines with no separator.
func parseDestination(line string) string {
	trimmed := strings.TrimSpace(line)
	for _, pfx := range []string{"/", "$", "?", "%", "#"} {
		if strings.HasPrefix(trimmed, pfx) {
			return ""
		}
	}
	idx := strings.IndexAny(trimmed, ";:")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(trimmed[:idx])
}

// pushPastSend prepends dest to pastSends, removes any earlier duplicate,
// and caps the ring at pastSendsMax.
func (m Model) pushPastSend(dest string) Model {
	if dest == "" {
		return m
	}
	out := make([]string, 0, len(m.pastSends)+1)
	for _, s := range m.pastSends {
		if s != dest {
			out = append(out, s)
		}
	}
	out = append([]string{dest}, out...)
	if len(out) > pastSendsMax {
		out = out[:pastSendsMax]
	}
	m.pastSends = out
	return m
}

// expandName calls the proxy to resolve a partial name.
// Returns empty string if no match or ambiguous (for ambiguous, use expandNameMatches).
func (m Model) expandName(partial string) string {
	if m.client == nil || partial == "" {
		return ""
	}
	q := strings.TrimPrefix(partial, "-")
	q = strings.ReplaceAll(q, "_", " ")
	slog.Debug("expand query: " + q)
	matches, err := m.client.Expand(q, true)
	if err != nil {
		slog.Debug("expand error: " + err.Error())
		return ""
	}
	if len(matches) == 0 {
		slog.Debug("expand: no matches for " + q)
		return ""
	}
	if len(matches) > 1 {
		names := make([]string, len(matches))
		for i, e := range matches {
			names[i] = e.Name + " (" + e.Kind + ")"
		}
		slog.Debug("expand: ambiguous: " + strings.Join(names, ", "))
		return ""
	}
	name := strings.ReplaceAll(matches[0].Name, " ", "_")
	if matches[0].Kind == "disc" {
		name = "-" + name
	}
	slog.Debug("expand: " + q + " → " + name + " (" + matches[0].Kind + ")")
	return name
}

// expandNameMatches returns all matches for a partial name.
func (m Model) expandNameMatches(partial string) []api.EntityJSON {
	if m.client == nil || partial == "" {
		return nil
	}
	q := strings.TrimPrefix(partial, "-")
	q = strings.ReplaceAll(q, "_", " ")
	matches, err := m.client.Expand(q, true)
	if err != nil {
		return nil
	}
	return matches
}

// handleExpandKey handles the intelligent-expand keys (',', ':', ';', '=').
func (m Model) handleExpandKey(key string) Model {
	if m.pasteMode {
		return m.insertString(key)
	}

	cursor := m.inputCursor

	if cursor == 0 {
		var recall string
		switch key {
		case ":":
			recall = m.expandSender
		case ";":
			recall = m.expandRecips
		case "=":
			recall = m.expandSendgroup
			if recall != "" {
				key = ";"
			}
		}
		if recall != "" {
			insert := recall + key
			m.inputValue = insert + m.inputValue
			m.inputCursor = len(insert)
			return m
		}
		return m.insertString(key)
	}

	if key == "=" {
		return m.insertString(key)
	}

	fore := m.inputValue[:cursor]

	if strings.ContainsAny(fore, ":;/") {
		return m.insertString(key)
	}
	trimmedFore := strings.TrimLeft(fore, " \t")
	if strings.HasPrefix(trimmedFore, "$") ||
		strings.HasPrefix(trimmedFore, "?") ||
		strings.HasPrefix(trimmedFore, "%") {
		return m.insertString(key)
	}

	dests := strings.Split(fore, ",")
	for i, d := range dests {
		token := strings.TrimSpace(d)
		if expanded := m.expandName(token); expanded != "" {
			dests[i] = expanded
		}
	}
	newFore := strings.Join(dests, ",")
	aft := m.inputValue[cursor:]
	m.inputValue = newFore + key + aft
	m.inputCursor = len(newFore) + len(key)
	return m
}

// tabComplete implements Tab / Ctrl-I completion.
func (m Model) tabComplete() Model {
	if m.pasteMode {
		return m
	}

	cursor := m.inputCursor

	if cursor == 0 {
		if len(m.pastSends) == 0 {
			return m
		}
		dest := m.pastSends[0] + ";"
		m.inputValue = dest + m.inputValue
		m.inputCursor = len(dest)
		return m
	}

	partial := m.inputValue[:cursor]
	const specialChars = "[];:=\"?\t "

	// Branch 1: no special chars — name completion
	if !strings.ContainsAny(partial, specialChars) {
		lastComma := strings.LastIndex(partial, ",")
		var fore, token string
		if lastComma >= 0 {
			fore = partial[:lastComma+1]
			token = partial[lastComma+1:]
		} else {
			fore = ""
			token = partial
		}

		matches := m.expandNameMatches(token)
		if len(matches) == 0 {
			return m
		}
		if len(matches) == 1 {
			// Single match - expand directly
			name := strings.ReplaceAll(matches[0].Name, " ", "_")
			if matches[0].Kind == "disc" {
				name = "-" + name
			}
			newPartial := fore + name
			m.inputValue = newPartial + m.inputValue[cursor:]
			m.inputCursor = len(newPartial)
			return m
		}
		// Multiple matches - show popup
		return m.showCompletionPopup(matches, token, fore)
	}

	// Branch 2: everything except last char has no special chars — cycle past-send ring
	if len(partial) > 1 && !strings.ContainsAny(partial[:len(partial)-1], specialChars) {
		base := partial[:len(partial)-1]
		if len(m.pastSends) == 0 {
			return m
		}
		next := m.pastSends[0]
		for i, s := range m.pastSends {
			if s == base {
				next = m.pastSends[(i+1)%len(m.pastSends)]
				break
			}
		}
		full := next + ";"
		m.inputValue = full + m.inputValue[cursor:]
		m.inputCursor = len(full)
		return m
	}

	// Branch 3: slash command with a name argument
	if cursor == len(m.inputValue) {
		m = m.tabCompleteCommand()
	}
	return m
}

// tabCompleteCommand handles Tab completion for "/command partial_name" patterns.
func (m Model) tabCompleteCommand() Model {
	line := m.inputValue
	if len(line) == 0 || line[0] != '/' {
		return m
	}
	spaceIdx := strings.Index(line, " ")
	if spaceIdx <= 0 {
		return m
	}
	cmd := strings.ToLower(line[1:spaceIdx])
	switch cmd {
	case "who", "ignore", "unignore", "finger", "also", "oops",
		"join", "quit", "where", "what", "block", "destroy":
	default:
		return m
	}
	rest := strings.TrimLeft(line[spaceIdx:], " ")
	if strings.ContainsAny(rest, " \t") {
		return m
	}

	matches := m.expandNameMatches(rest)
	if len(matches) == 0 {
		return m
	}
	if len(matches) == 1 {
		name := strings.ReplaceAll(matches[0].Name, " ", "_")
		if matches[0].Kind == "disc" {
			name = "-" + name
		}
		newLine := line[:spaceIdx+1] + name
		m.inputValue = newLine
		m.inputCursor = len(newLine)
		return m
	}
	// Multiple matches - show popup
	fore := line[:spaceIdx+1]
	return m.showCompletionPopup(matches, rest, fore)
}

// trackIncomingPrivate updates expand state when a private send arrives. Only
// private sends count: they are the ones ':' recalls and the cursor-0 Tab
// default fills in. Public traffic (regular sends and emotes, both of which go
// to a discussion) must not touch this state, so the caller gates on the event
// type before calling here.
func (m Model) trackIncomingPrivate(d map[string]interface{}) Model {
	if m.state == nil {
		return m
	}
	source, _ := d["source"].(string)
	if source == "" || source == m.state.Whoami {
		return m
	}

	entities, _ := d["entities"].(map[string]interface{})
	senderName := nameFromEntities(entities, source)
	senderDest := strings.ReplaceAll(senderName, " ", "_")

	m.expandSender = senderDest
	m = m.pushPastSend(senderDest)

	recips, _ := d["recips"].([]interface{})
	if len(recips) > 1 {
		group := []string{senderDest}
		for _, r := range recips {
			handle, _ := r.(string)
			if handle == "" || handle == m.state.Whoami {
				continue
			}
			n := nameFromEntities(entities, handle)
			group = append(group, strings.ReplaceAll(n, " ", "_"))
		}
		m.expandSendgroup = strings.Join(group, ",")
	}

	return m
}

// trackOutgoingSend updates expand state when the user sends a line.
func (m Model) trackOutgoingSend(line string) Model {
	dest := parseDestination(line)
	if dest == "" {
		return m
	}
	m.expandRecips = dest
	m = m.pushPastSend(dest)
	return m
}
