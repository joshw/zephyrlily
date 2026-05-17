package ui

import (
	"log/slog"
	"strings"
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
//
// A leading '-' on partial signals a disc-only search (matching the Lily
// convention for discussion destinations); it is stripped before querying.
// Underscores are treated as spaces when querying.
//
// Returned names for discussions are prefixed with '-' (e.g. "-emacs").
// Returns "" when there is no unique match.
// Always uses valid_dest_only mode, so discussions the user is not a member of
// are excluded.
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

// handleExpandKey handles the intelligent-expand keys (',', ':', ';', '='),
// mirroring tigerlily's exp_expand:
//
//   - At cursor position 0: recalls the last sender (':', expandSender),
//     last recipients (';', expandRecips), or last send-group ('=', expandSendgroup).
//   - At cursor position > 0 and still in the destination portion of the line:
//     attempts name expansion on each comma-separated token before the cursor.
func (m Model) handleExpandKey(key string) Model {
	if m.pasteMode {
		return m.insertString(key)
	}

	if m.cursor == 0 {
		var recall string
		switch key {
		case ":":
			recall = m.expandSender
		case ";":
			recall = m.expandRecips
		case "=":
			recall = m.expandSendgroup
			if recall != "" {
				key = ";" // '=' recall acts as ';'
			}
		}
		if recall != "" {
			insert := recall + key
			m.input = insert + m.input
			m.cursor = len(insert)
			m = m.adjustInputScroll()
			return m
		}
		return m.insertString(key)
	}

	// '=' only expands at position 0; elsewhere insert it literally.
	if key == "=" {
		return m.insertString(key)
	}

	fore := m.input[:m.cursor]

	// Don't expand when already past the destination portion or inside a command.
	if strings.ContainsAny(fore, ":;/") {
		return m.insertString(key)
	}
	trimmedFore := strings.TrimLeft(fore, " \t")
	if strings.HasPrefix(trimmedFore, "$") ||
		strings.HasPrefix(trimmedFore, "?") ||
		strings.HasPrefix(trimmedFore, "%") {
		return m.insertString(key)
	}

	// Try to expand each comma-separated token; leave unexpandable ones as-is.
	dests := strings.Split(fore, ",")
	for i, d := range dests {
		token := strings.TrimSpace(d)
		if expanded := m.expandName(token); expanded != "" {
			dests[i] = expanded
		}
	}
	newFore := strings.Join(dests, ",")
	aft := m.input[m.cursor:]
	m.input = newFore + key + aft
	m.cursor = len(newFore) + len(key)
	m = m.adjustInputScroll()
	return m
}

// tabComplete implements Tab / Ctrl-I completion, mirroring tigerlily's exp_complete:
//
//   - Position 0: fill in the most recent past-send destination.
//   - Partial with no special chars before cursor: prefix-expand the last name token.
//   - All-but-last char has no special chars (e.g. "josh;"): cycle the past-send ring.
//   - Otherwise: try expanding the name argument of certain slash commands.
func (m Model) tabComplete() Model {
	if m.pasteMode {
		return m
	}

	if m.cursor == 0 {
		if len(m.pastSends) == 0 {
			return m
		}
		dest := m.pastSends[0] + ";"
		m.input = dest + m.input
		m.cursor = len(dest)
		m = m.adjustInputScroll()
		return m
	}

	partial := m.input[:m.cursor]
	const specialChars = "[];:=\"?\t "

	// Branch 1: no special chars anywhere before cursor — name completion.
	if !strings.ContainsAny(partial, specialChars) {
		lastComma := strings.LastIndex(partial, ",")
		var fore, token string
		if lastComma >= 0 {
			fore = partial[:lastComma+1]
			token = partial[lastComma+1:]
		} else {
			token = partial
		}
		if expanded := m.expandName(token); expanded != "" {
			newPartial := fore + expanded
			m.input = newPartial + m.input[m.cursor:]
			m.cursor = len(newPartial)
			m = m.adjustInputScroll()
		}
		return m
	}

	// Branch 2: everything except the last char has no special chars — cycle past-send ring.
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
		m.input = full + m.input[m.cursor:]
		m.cursor = len(full)
		m = m.adjustInputScroll()
		return m
	}

	// Branch 3: slash command with a name argument at the end of the line.
	if m.cursor == len(m.input) {
		m = m.tabCompleteCommand()
	}
	return m
}

// tabCompleteCommand handles Tab completion for "/command partial_name" patterns.
func (m Model) tabCompleteCommand() Model {
	line := m.input
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
		return m // more than one word after the command
	}
	if expanded := m.expandName(rest); expanded != "" {
		newLine := line[:spaceIdx+1] + expanded
		m.input = newLine
		m.cursor = len(newLine)
		m = m.adjustInputScroll()
	}
	return m
}

// trackIncomingPrivate updates expand state when a private or emote arrives
// from someone other than the current user.
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

	// Build sendgroup when the private has multiple recipients.
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
