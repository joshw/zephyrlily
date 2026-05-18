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
// whoami is the current user's handle; when source == whoami the message
// uses second-person language ("you have/are") instead of third-person.
func formatEvent(d map[string]interface{}, width int, whoami string) string {
	event, _ := d["event"].(string)
	source, _ := d["source"].(string)
	value, _ := d["value"].(string)

	// Use the proxy's pre-formatted text for all events except the ones we
	// render with rich TUI styling (public, private, emote, pa).
	if text, ok := d["text"].(string); ok && text != "" {
		switch event {
		case "public", "private", "emote", "pa":
			// fall through to rich formatting below
		default:
			// Word-wrap the text to fit the terminal width, then style each line.
			lines := wrapText("", "", text, max(width-2, 1), "")
			for i := range lines {
				lines[i] = slcpBodyStyle.Render(lines[i])
			}
			return strings.Join(lines, "\n")
		}
	}

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

	// Extract targets and sub-event fields used by permission events.
	var targetsRaw []interface{}
	if t, ok := d["targets"].([]interface{}); ok {
		targetsRaw = t
	}
	subEvt, _ := d["sub_evt"].(string)

	lookupTargets := func(handles []interface{}) string {
		names := make([]string, 0, len(handles))
		for _, h := range handles {
			if handle, ok := h.(string); ok {
				names = append(names, lookupName(handle))
			}
		}
		return strings.Join(names, ", ")
	}

	// lookupDiscTitle returns the title of a discussion entity by handle.
	lookupDiscTitle := func(handle string) string {
		if entity, ok := entities[handle]; ok {
			if title, ok := entity["title"].(string); ok {
				return title
			}
		}
		return ""
	}

	// sourceWithBlurb returns "Name [blurb]" or just "Name" — matches %U in the reference.
	sourceWithBlurb := func() string {
		name := lookupName(source)
		if entity, ok := entities[source]; ok {
			if blurb, ok := entity["blurb"].(string); ok && blurb != "" {
				return name + " [" + blurb + "]"
			}
		}
		return name
	}

	// blurbSuffix returns " with the blurb [x]" when the source has a blurb, else "".
	// Used in self here/away confirmations (%B in the reference).
	blurbSuffix := func() string {
		if entity, ok := entities[source]; ok {
			if blurb, ok := entity["blurb"].(string); ok && blurb != "" {
				return " with the blurb [" + blurb + "]"
			}
		}
		return ""
	}

	// meInTargets is true when the current user appears in the targets list (M flag).
	meInTargets := false
	if whoami != "" {
		for _, t := range targetsRaw {
			if h, ok := t.(string); ok && h == whoami {
				meInTargets = true
				break
			}
		}
	}

	isSelf := whoami != "" && source == whoami

	// quiet wraps a message in parentheses — used for self-originated confirmations.
	// banner wraps a message in *** *** — used for third-party observations.
	// Both word-wrap their content to fit the terminal width.
	renderLines := func(lines []string) string {
		for i := range lines {
			lines[i] = slcpBodyStyle.Render(lines[i])
		}
		return strings.Join(lines, "\n")
	}
	quiet := func(msg string) string {
		lines := wrapText("(", "(", msg+")", max(width-2, 1), "")
		return renderLines(lines)
	}
	banner := func(msg string) string {
		lines := wrapText("*** ", "*** ", msg+" ***", max(width-4, 1), "")
		return renderLines(lines)
	}

	switch event {
	case "public":
		var header strings.Builder
		header.WriteString(publicHeaderStyle.Render(" -> "))
		header.WriteString(publicTimestampStyle.Render(timestamp))
		header.WriteString(publicHeaderStyle.Render("From "))
		header.WriteString(formatUser(source, publicSenderStyle, publicBlurbStyle))
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			header.WriteString(publicHeaderStyle.Render(", to "))
			header.WriteString(formatRecips(recips, publicSenderStyle))
		}
		header.WriteString(publicHeaderStyle.Render(":"))
		body := wrapMessage(" - ", value, width)
		return "\n" + header.String() + "\n" + publicBodyStyle.Render(body)

	case "private":
		var header strings.Builder
		header.WriteString(privateHeaderStyle.Render(" >> "))
		header.WriteString(privateTimestampStyle.Render(timestamp))
		header.WriteString(privateHeaderStyle.Render("Private message from "))
		header.WriteString(formatUser(source, privateSenderStyle, privateBlurbStyle))
		header.WriteString(privateHeaderStyle.Render(":"))
		body := wrapMessage(" - ", value, width)
		return "\n" + header.String() + "\n" + privateBodyStyle.Render(body)

	case "emote":
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
		lines := wrapText(headerStr, "> ", strings.TrimSpace(value), width, " ")
		return emoteBodyStyle.Render(strings.Join(lines, "\n"))

	// ── Presence events ──────────────────────────────────────────────────────

	case "connect":
		// Third-person always: "%U has entered lily"
		return banner(sourceWithBlurb() + " has entered lily")

	case "disconnect":
		if value != "" {
			return banner(sourceWithBlurb() + " has left lily (" + value + ")")
		}
		return banner(sourceWithBlurb() + " has left lily")

	case "attach":
		return banner(sourceWithBlurb() + " has reattached")

	case "detach":
		if value != "" {
			return banner(sourceWithBlurb() + " has been detached " + value)
		}
		return banner(sourceWithBlurb() + " has detached")

	case "here":
		if isSelf {
			return quiet("you are now here" + blurbSuffix())
		}
		return banner(lookupName(source) + " is now \"here\"")

	case "away":
		if value != "" {
			// value defined = idled away automatically, not an explicit /away
			return banner(lookupName(source) + " has idled \"away\"")
		}
		if isSelf {
			return quiet("you are now away" + blurbSuffix())
		}
		return banner(lookupName(source) + " is now \"away\"")

	case "unidle":
		return banner(lookupName(source) + " is now unidle")

	// ── Identity events ───────────────────────────────────────────────────────

	case "rename":
		if isSelf {
			return quiet("you are now named " + value)
		}
		return banner(lookupName(source) + " is now named " + value)

	case "blurb":
		if isSelf {
			if value != "" {
				return quiet("your blurb has been set to [" + value + "]")
			}
			return quiet("your blurb has been turned off")
		}
		if value != "" {
			return banner(lookupName(source) + " has changed their blurb to [" + value + "]")
		}
		return banner(lookupName(source) + " has turned their blurb off")

	case "info":
		recips, hasRecips := d["recips"].([]interface{})
		discName := ""
		if hasRecips && len(recips) > 0 {
			discName = lookupRecips(recips)
		} else {
			hasRecips = false
		}
		if isSelf {
			if hasRecips {
				if value == "" {
					return quiet("you have cleared the info for " + discName)
				}
				return quiet("you have changed the info for " + discName)
			}
			if value == "" {
				return quiet("your info has been cleared")
			}
			return quiet("your info has been changed")
		}
		if hasRecips {
			if value == "" {
				return banner(lookupName(source) + " has cleared the info for discussion " + discName)
			}
			return banner(lookupName(source) + " has changed the info for discussion " + discName)
		}
		if value == "" {
			return banner(lookupName(source) + " has cleared their info")
		}
		return banner(lookupName(source) + " has changed their info")

	// ── Discussion membership ─────────────────────────────────────────────────

	case "create":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			discName := lookupRecips(recips)
			titlePart := ""
			if h, ok := recips[0].(string); ok {
				if t := lookupDiscTitle(h); t != "" {
					titlePart = " \"" + t + "\""
				}
			}
			if isSelf {
				return quiet("you have created discussion " + discName + titlePart)
			}
			return banner(lookupName(source) + " has created discussion " + discName + titlePart)
		}
		if isSelf {
			return quiet("you have created a discussion")
		}
		return banner(lookupName(source) + " has created a discussion")

	case "destroy":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			discName := lookupRecips(recips)
			if isSelf {
				return quiet("you have destroyed discussion " + discName)
			}
			return banner(lookupName(source) + " has destroyed discussion " + discName)
		}
		if isSelf {
			return quiet("you have destroyed a discussion")
		}
		return banner(lookupName(source) + " has destroyed a discussion (server didn't say which)")

	case "join":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			discName := lookupRecips(recips)
			if isSelf {
				return quiet("you have joined " + discName)
			}
			return banner(lookupName(source) + " is now a member of " + discName)
		}
		if isSelf {
			return quiet("you have joined a discussion")
		}
		return banner(lookupName(source) + " has joined a discussion")

	case "quit":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			discName := lookupRecips(recips)
			if isSelf {
				return quiet("you have quit " + discName)
			}
			return banner(lookupName(source) + " is no longer a member of " + discName)
		}
		if isSelf {
			return quiet("you have quit a discussion")
		}
		return banner(lookupName(source) + " has quit a discussion")

	case "retitle":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			discName := lookupRecips(recips)
			if isSelf {
				return quiet("you have changed the title of " + discName + " to \"" + value + "\"")
			}
			return banner(lookupName(source) + " has changed the title of " + discName + " to \"" + value + "\"")
		}
		if isSelf {
			return quiet("you have changed a discussion title to \"" + value + "\"")
		}
		return banner(lookupName(source) + " has changed a discussion title to \"" + value + "\"")

	case "drename":
		// Disc names are prefixed with '-' per Lily convention.
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return banner("Discussion -" + lookupRecips(recips) + " is now named -" + value)
		}
		return banner("A discussion is now named -" + value)

	// ── Permission events (permit / depermit) ─────────────────────────────────

	case "permit":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			disc := lookupRecips(recips)
			tgt := lookupTargets(targetsRaw)
			hasT := len(targetsRaw) > 0
			switch {
			case isSelf && meInTargets && subEvt == "owner":
				return quiet("you have accepted ownership of discussion " + disc)
			case isSelf && hasT && subEvt == "owner":
				return quiet("you have offered " + tgt + " ownership of discussion " + disc)
			case isSelf && hasT && subEvt != "":
				return quiet("you have given " + tgt + " " + subEvt + " privileges to discussion " + disc)
			case isSelf && !hasT && subEvt != "":
				return quiet(disc + " is no longer moderated")
			case meInTargets && subEvt == "owner":
				return banner(lookupName(source) + " has offered you ownership of discussion " + disc)
			case meInTargets && subEvt != "":
				return banner(lookupName(source) + " has given you " + subEvt + " privileges to discussion " + disc)
			case meInTargets:
				return banner(lookupName(source) + " has permitted you to discussion " + disc)
			case hasT && subEvt == "owner":
				return banner(lookupName(source) + " has taken ownership of discussion " + disc)
			case hasT && subEvt != "":
				return banner(lookupName(source) + " has given " + tgt + " " + subEvt + " privileges to discussion " + disc)
			case hasT:
				return banner(lookupName(source) + " has permitted " + tgt + " to discussion " + disc)
			case subEvt != "":
				return banner(lookupName(source) + " has unmoderated discussion " + disc)
			}
		}
		return banner(lookupName(source) + " has changed permissions")

	case "depermit":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			disc := lookupRecips(recips)
			tgt := lookupTargets(targetsRaw)
			hasT := len(targetsRaw) > 0
			switch {
			case isSelf && hasT && subEvt == "owner":
				return quiet("you have rescinded your offer to " + tgt + " for ownership of discussion " + disc)
			case isSelf && hasT && subEvt != "":
				return quiet("you have removed " + tgt + "'s " + subEvt + " privileges on discussion " + disc)
			case isSelf && !hasT && subEvt != "":
				return quiet(disc + " is now moderated")
			case meInTargets && subEvt == "owner":
				return banner(lookupName(source) + " has rescinded their ownership offer of discussion " + disc)
			case meInTargets && subEvt != "":
				return banner(lookupName(source) + " has removed your " + subEvt + " privileges on discussion " + disc)
			case meInTargets:
				return banner(lookupName(source) + " has depermitted you from discussion " + disc)
			case hasT && subEvt != "":
				return banner(lookupName(source) + " has removed " + tgt + "'s " + subEvt + " privileges on discussion " + disc)
			case hasT:
				return banner(lookupName(source) + " has depermitted " + tgt + " from discussion " + disc)
			case subEvt != "":
				return banner(lookupName(source) + " has moderated discussion " + disc)
			}
		}
		return banner(lookupName(source) + " has changed permissions")

	// ── Role appointment events (appoint / unappoint) ─────────────────────────

	case "appoint":
		disc := ""
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			disc = lookupRecips(recips)
		}
		tgt := lookupTargets(targetsRaw)
		hasT := len(targetsRaw) > 0
		switch {
		case isSelf && meInTargets && subEvt == "owner":
			return quiet("you have accepted ownership of discussion " + disc)
		case isSelf && subEvt == "owner":
			return quiet("you have offered " + tgt + " ownership of discussion " + disc)
		case meInTargets && subEvt == "owner":
			return banner(lookupName(source) + " has offered you ownership of discussion " + disc)
		case subEvt == "owner":
			return banner(lookupName(source) + " is now the owner of discussion " + disc)
		case !hasT && subEvt == "speaker":
			return banner("discussion " + disc + " is now moderated")
		case meInTargets && subEvt == "speaker":
			return banner("you have been made a speaker for discussion " + disc)
		case subEvt == "speaker":
			return banner(tgt + " is now a speaker for discussion " + disc)
		case meInTargets && subEvt == "author":
			return banner("you have been made an author for discussion " + disc)
		case subEvt == "author":
			return banner(tgt + " is now an author for discussion " + disc)
		case meInTargets && subEvt != "":
			return banner("you are now a " + subEvt + " for " + disc)
		case subEvt != "":
			return banner(tgt + " is now a " + subEvt + " for " + disc)
		}
		return banner(lookupName(source) + " made an appointment for discussion " + disc)

	case "unappoint":
		disc := ""
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			disc = lookupRecips(recips)
		}
		tgt := lookupTargets(targetsRaw)
		hasT := len(targetsRaw) > 0
		switch {
		case isSelf && subEvt == "owner":
			return quiet("you have rescinded your ownership offer to " + tgt + " of discussion " + disc)
		case meInTargets && subEvt == "owner":
			return banner(lookupName(source) + " has rescinded their offer of ownership of discussion " + disc)
		case !hasT && subEvt == "speaker":
			return banner("discussion " + disc + " is no longer moderated")
		case meInTargets && subEvt == "speaker":
			return banner("you are no longer a speaker for discussion " + disc)
		case subEvt == "speaker":
			return banner(tgt + " is no longer a speaker for discussion " + disc)
		case meInTargets && subEvt == "author":
			return banner("you are no longer an author for discussion " + disc)
		case subEvt == "author":
			return banner(tgt + " is no longer an author for discussion " + disc)
		case meInTargets && subEvt != "":
			return banner("you are no longer a " + subEvt + " for " + disc)
		case subEvt != "":
			return banner(tgt + " is no longer a " + subEvt + " for " + disc)
		}
		return banner(lookupName(source) + " changed an appointment for discussion " + disc)

	// ── Ignore events ─────────────────────────────────────────────────────────

	case "ignore":
		// 'tcE' = no targets, no subevt, value empty → no longer ignoring
		if value == "" && len(targetsRaw) == 0 && subEvt == "" {
			return banner(lookupName(source) + " is no longer ignoring you")
		}
		if value != "" {
			return banner(lookupName(source) + " is now ignoring you " + value)
		}
		return banner(lookupName(source) + " is now ignoring you")

	case "unignore":
		return banner(lookupName(source) + " is no longer ignoring you")

	// ── Review ────────────────────────────────────────────────────────────────

	case "review":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return banner(lookupName(source) + " has cleared the review for discussion " + lookupRecips(recips))
		}
		return banner(lookupName(source) + " has cleared a review")

	// ── System ────────────────────────────────────────────────────────────────

	case "sysmsg":
		return slcpBodyStyle.Render("*** " + value + " ***")

	case "pa":
		return slcpBodyStyle.Render("** Public address message from " +
			formatUser(source, publicSenderStyle, publicBlurbStyle) + ": " + value + " **")

	default:
		return fmt.Sprintf("[%s] %s %s", event, source, value)
	}
}
