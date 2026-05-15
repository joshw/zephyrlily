package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// wrapText wraps text onto lines of at most maxWidth chars.
// Word boundaries are preferred; long tokens (e.g. URLs) are hard-broken as a
// last resort.
//
// curLine is the line currently being built (may already contain a prefix).
// wordPrefix is the prefix prepended on word-boundary continuation lines.
// initialSep is the separator placed before the very first word (typically ""
// when the prefix already ends at a word boundary, or " " when the caller
// wants a space between an existing header and the message body).
//
// Hard-break continuation lines intentionally carry NO prefix so that a split
// URL is not visually interrupted by a repeated prefix marker.
func wrapText(curLine, wordPrefix, text string, maxWidth int, initialSep string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{curLine}
	}

	var lines []string
	sep := initialSep
	lineHasContent := false
	continuingWord := false

	for _, word := range words {
		for len(word) > 0 {
			avail := maxWidth - len(curLine) - len(sep)

			if avail <= 0 {
				// Current line is full — emit it and start a new one.
				lines = append(lines, curLine)
				if continuingWord {
					curLine = "" // hard-break continuation: no prefix
				} else {
					curLine = wordPrefix
				}
				lineHasContent = false
				sep = ""
				avail = maxWidth - len(curLine)
				continue
			}

			if len(word) <= avail {
				// Word fits on the current line.
				curLine += sep + word
				sep = " "
				word = ""
				lineHasContent = true
				continuingWord = false
			} else if !lineHasContent {
				// Nothing else on this line yet — hard-break the token.
				curLine += sep + word[:avail]
				word = word[avail:]
				lines = append(lines, curLine)
				curLine = ""
				lineHasContent = false
				sep = ""
				continuingWord = true
			} else {
				// Word doesn't fit and there's other content — word-boundary wrap.
				lines = append(lines, curLine)
				curLine = wordPrefix
				lineHasContent = false
				sep = ""
				continuingWord = false
			}
		}
		continuingWord = false // completed this word
	}

	lines = append(lines, curLine)
	return lines
}

// formatEvent produces a human-readable line for a notify event.
// Formatting is based on tigerlily's slcp_output.pl message templates.
// width is the maximum line width for wrapping messages.
func formatEvent(d map[string]interface{}, width int) string {
	event, _ := d["event"].(string)
	source, _ := d["source"].(string)
	value, _ := d["value"].(string)

	// Extract timestamp — only shown when STAMP=1 was present in the %NOTIFY message
	var timestamp string
	stamp, _ := d["stamp"].(bool)
	if stamp {
		if timeVal, ok := d["time"].(float64); ok && timeVal > 0 {
			t := time.Unix(int64(timeVal), 0)
			timestamp = fmt.Sprintf("(%02d:%02d) ", t.Hour(), t.Minute())
		}
	}

	// Extract entity data
	entities := make(map[string]map[string]interface{})
	if entitiesRaw, ok := d["entities"].(map[string]interface{}); ok {
		for k, v := range entitiesRaw {
			if entity, ok := v.(map[string]interface{}); ok {
				entities[k] = entity
			}
		}
	}

	// Helper to format a user/entity reference with name and optional blurb
	formatUser := func(handle string, senderStyle, blurbSty lipgloss.Style) string {
		if entity, ok := entities[handle]; ok {
			name, _ := entity["name"].(string)
			blurb, _ := entity["blurb"].(string)
			if name != "" {
				if blurb != "" {
					return senderStyle.Render(name) + " " + blurbSty.Render(fmt.Sprintf("[%s]", blurb))
				}
				return senderStyle.Render(name)
			}
		}
		return senderStyle.Render(handle)
	}

	// Plain name lookup with no styling — use inside an outer Render() call
	// to avoid inner resets breaking a uniform color across the whole line.
	lookupName := func(handle string) string {
		if entity, ok := entities[handle]; ok {
			if name, ok := entity["name"].(string); ok && name != "" {
				return name
			}
		}
		return handle
	}

	lookupRecips := func(recips []interface{}) string {
		names := make([]string, 0, len(recips))
		for _, r := range recips {
			if h, ok := r.(string); ok {
				names = append(names, lookupName(h))
			}
		}
		return strings.Join(names, ", ")
	}

	// Helper to format a list of recipient handles as a comma-separated string of names
	formatRecips := func(recips []interface{}, sty lipgloss.Style) string {
		names := make([]string, 0, len(recips))
		for _, r := range recips {
			handle, ok := r.(string)
			if !ok {
				continue
			}
			if entity, ok := entities[handle]; ok {
				if name, ok := entity["name"].(string); ok && name != "" {
					names = append(names, sty.Render(name))
					continue
				}
			}
			names = append(names, sty.Render(handle))
		}
		return strings.Join(names, ", ")
	}

	// Helper to wrap message text with prefix on each line.
	// Hard-break continuation lines (e.g. split URLs) carry no prefix.
	wrapMessage := func(prefix, msg string, width int) string {
		if msg == "" {
			return ""
		}
		return strings.Join(wrapText(prefix, prefix, strings.TrimSpace(msg), width, ""), "\n")
	}

	switch event {
	case "public":
		// Format: " -> (timestamp) From user [blurb], to target1, target2:\n - message"
		var header strings.Builder
		header.WriteString(publicHeaderStyle.Render(" -> "))
		header.WriteString(publicTimestampStyle.Render(timestamp))
		header.WriteString(publicHeaderStyle.Render("From "))
		header.WriteString(formatUser(source, publicSenderStyle, publicBlurbStyle))

		// Add recipients
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			header.WriteString(publicHeaderStyle.Render(", to "))
			header.WriteString(formatRecips(recips, publicSenderStyle))
		}
		header.WriteString(publicHeaderStyle.Render(":"))

		// Wrap message body with " - " prefix
		body := wrapMessage(" - ", value, width)
		return "\n" + header.String() + "\n" + publicBodyStyle.Render(body)

	case "private":
		// Format: " >> (timestamp) Private message from user [blurb]:\n - message"
		var header strings.Builder
		header.WriteString(privateHeaderStyle.Render(" >> "))
		header.WriteString(privateTimestampStyle.Render(timestamp))
		header.WriteString(privateHeaderStyle.Render("Private message from "))
		header.WriteString(formatUser(source, privateSenderStyle, privateBlurbStyle))
		header.WriteString(privateHeaderStyle.Render(":"))

		// Wrap message body with " - " prefix
		body := wrapMessage(" - ", value, width)
		return "\n" + header.String() + "\n" + privateBodyStyle.Render(body)

	case "emote":
		// Format: "> (HH:MM, to dest) Source message"  (timestamp only if STAMP)
		// Long messages wrap; continuation lines keep the "> " prefix.
		// Entire output is uniform emoteBodyStyle; no blurb shown.
		var header strings.Builder
		header.WriteString("> (")
		if stamp {
			if timeVal, ok := d["time"].(float64); ok && timeVal > 0 {
				t := time.Unix(int64(timeVal), 0)
				fmt.Fprintf(&header, "%02d:%02d, ", t.Hour(), t.Minute())
			}
		}
		header.WriteString("to ")
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			header.WriteString(lookupRecips(recips))
		}
		header.WriteString(") ")
		header.WriteString(lookupName(source))

		headerStr := header.String()
		if value == "" {
			return emoteBodyStyle.Render(headerStr)
		}
		// " " separator between header and first word; "> " on word-boundary wraps;
		// hard-break continuations (e.g. split URLs) carry no prefix.
		lines := wrapText(headerStr, "> ", strings.TrimSpace(value), width, " ")
		return emoteBodyStyle.Render(strings.Join(lines, "\n"))

	case "connect":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has entered lily ***", lookupName(source)))

	case "disconnect":
		if value != "" {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s has left lily (%s) ***", lookupName(source), value))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has left lily ***", lookupName(source)))

	case "attach":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has reattached ***", lookupName(source)))

	case "detach":
		if value != "" {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s has been detached %s ***", lookupName(source), value))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has detached ***", lookupName(source)))

	case "here":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s is now \"here\" ***", lookupName(source)))

	case "away":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s is now \"away\" ***", lookupName(source)))

	case "rename":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s is now named %s ***", lookupName(source), value))

	case "blurb":
		if value != "" {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s has changed their blurb to [%s] ***", lookupName(source), value))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has turned their blurb off ***", lookupName(source)))

	case "unidle":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s is now unidle ***", lookupName(source)))

	case "create":
		// For discussion creation, RECIPS holds the discussion handle
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s has created discussion %s ***", lookupName(source), lookupRecips(recips)))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has created a discussion ***", lookupName(source)))

	case "destroy":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s has destroyed discussion %s ***", lookupName(source), lookupRecips(recips)))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has destroyed a discussion ***", lookupName(source)))

	case "join":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s is now a member of %s ***", lookupName(source), lookupRecips(recips)))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has joined a discussion ***", lookupName(source)))

	case "quit":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s is no longer a member of %s ***", lookupName(source), lookupRecips(recips)))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has quit a discussion ***", lookupName(source)))

	case "retitle":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return slcpBodyStyle.Render(fmt.Sprintf("*** %s has changed the title of %s to \"%s\" ***", lookupName(source), lookupRecips(recips), value))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s has changed a discussion title to \"%s\" ***", lookupName(source), value))

	case "drename":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return slcpBodyStyle.Render(fmt.Sprintf("*** Discussion %s is now named %s ***", lookupRecips(recips), value))
		}
		return slcpBodyStyle.Render(fmt.Sprintf("*** A discussion is now named %s ***", value))

	case "sysmsg":
		return slcpBodyStyle.Render(fmt.Sprintf("*** %s ***", value))

	case "pa":
		return slcpBodyStyle.Render(fmt.Sprintf("** Public address message from %s: %s **", formatUser(source, publicSenderStyle, publicBlurbStyle), value))

	default:
		// Unknown event type - show all available data
		return fmt.Sprintf("[%s] %s %s", event, source, value)
	}
}
