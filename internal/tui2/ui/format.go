package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
)

// wrapText wraps text onto lines of at most maxWidth chars.
func wrapText(curLine, wordPrefix, text string, maxWidth int, initialSep string) []string {
	return wrapTextCore(curLine, wordPrefix, text, maxWidth, initialSep, false)
}

// wrapTextLinkify behaves like wrapText but renders any URLs as OSC8
// hyperlinks. A URL that is hard-broken across lines emits each fragment as a
// hyperlink to the full URL, all sharing one id so supporting terminals treat
// them as a single clickable link. Width is measured on the visible text, so
// the invisible escape bytes never affect wrapping.
func wrapTextLinkify(curLine, wordPrefix, text string, maxWidth int, initialSep string) []string {
	return wrapTextCore(curLine, wordPrefix, text, maxWidth, initialSep, true)
}

// wrapTextCore implements word wrapping. curLine and wordPrefix are assumed to
// be plain (no escape sequences); when linkify is true, URL words are rendered
// with OSC8 escapes while wrapping arithmetic continues to use visible length.
func wrapTextCore(curLine, wordPrefix, text string, maxWidth int, initialSep string, linkify bool) []string {
	// Guard against a non-positive width: with maxWidth <= 0 the available space
	// is always <= 0 and the consume loop can never advance, spinning forever.
	if maxWidth < 1 {
		maxWidth = 1
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{curLine}
	}

	var lines []string
	sep := initialSep
	lineHasContent := false
	continuingWord := false
	curVis := len(curLine) // visible length of curLine (may differ when linkify adds escapes)

	for _, word := range words {
		// Resolve the URL span (if any) once against the original word so it
		// stays valid as the word is consumed across hard-broken lines.
		urlStart, urlEnd, clean := -1, -1, ""
		var id int64
		if linkify {
			if urlStart, urlEnd, clean = urlSpanInWord(word); urlStart >= 0 {
				id = linkID.Add(1)
			}
		}
		// render returns word[a:b], wrapping any portion within the URL span in
		// an OSC8 hyperlink to the full URL.
		render := func(a, b int) string {
			if urlStart < 0 || b <= urlStart || a >= urlEnd {
				return word[a:b]
			}
			lo, hi := max(a, urlStart), min(b, urlEnd)
			return word[a:lo] + osc8Link(clean, word[lo:hi], id) + word[hi:b]
		}

		consumed := 0
		for consumed < len(word) {
			remaining := len(word) - consumed
			avail := maxWidth - curVis - len(sep)

			if avail <= 0 {
				lines = append(lines, curLine)
				if continuingWord {
					curLine = ""
				} else {
					curLine = wordPrefix
				}
				curVis = len(curLine)
				lineHasContent = false
				sep = ""
				continue
			}

			if remaining <= avail {
				curLine += sep + render(consumed, len(word))
				curVis += len(sep) + remaining
				consumed = len(word)
				sep = " "
				lineHasContent = true
				continuingWord = false
			} else if !lineHasContent {
				curLine += sep + render(consumed, consumed+avail)
				consumed += avail
				lines = append(lines, curLine)
				curLine = ""
				curVis = 0
				lineHasContent = false
				sep = ""
				continuingWord = true
			} else {
				lines = append(lines, curLine)
				curLine = wordPrefix
				curVis = len(wordPrefix)
				lineHasContent = false
				sep = ""
				continuingWord = false
			}
		}
		continuingWord = false
	}

	lines = append(lines, curLine)
	return lines
}

// renderOutputItem formats an OutputItem into display lines based on current width.
func (m Model) renderOutputItem(item OutputItem) []string {
	width := m.width
	if m.debugMode {
		width = m.width / 2
	}

	switch item.Type {
	case "text":
		if text, ok := item.Data.(string); ok {
			// Apply linkification to text
			text = linkifyText(text)
			return strings.Split(text, "\n")
		}

	case "command":
		if lines, ok := item.Data.([]string); ok {
			wrapWidth := width - 2
			if wrapWidth < 1 {
				wrapWidth = 1
			}
			var out []string
			for _, line := range lines {
				wrapped := strings.Split(wordwrap.String(line, wrapWidth), "\n")
				// Apply linkification after wrapping to avoid breaking URLs
				for i := range wrapped {
					wrapped[i] = linkifyText(wrapped[i])
				}
				out = append(out, wrapped...)
			}
			return out
		}

	case "event":
		if d, ok := item.Data.(map[string]interface{}); ok {
			whoami := ""
			if m.state != nil {
				whoami = m.state.Whoami
			}
			formatted := formatEvent(d, width, whoami)
			return strings.Split(formatted, "\n")
		}

	case "error":
		if e, ok := item.Data.(string); ok {
			return []string{errorStyle.Render("*** " + e + " ***")}
		}

	case "input":
		if line, ok := item.Data.(string); ok {
			w := width
			if w < 1 {
				w = 1
			}
			wrapped := wordwrap.String(line, w)
			lines := strings.Split(wrapped, "\n")
			for i := range lines {
				lines[i] = inputStyle.Render(lines[i])
			}
			return lines
		}

	case "log":
		if entry, ok := item.Data.(logMsg); ok {
			var labelStyle lipgloss.Style
			switch entry.level {
			case "ERROR":
				labelStyle = logErrorSeverityStyle
			case "WARN":
				labelStyle = logInfoSeverityStyle
			default:
				labelStyle = logPrefixStyle
			}
			label := labelStyle.Render("[" + entry.level + "]")
			return []string{label + " " + entry.text}
		}
	}

	return []string{"[unknown output type]"}
}

// formatEvent produces a human-readable line for a notify event.
func formatEvent(d map[string]interface{}, width int, whoami string) string {
	event, _ := d["event"].(string)
	source, _ := d["source"].(string)
	value, _ := d["value"].(string)

	// Use the proxy's pre-formatted text for all events except styled ones
	if text, ok := d["text"].(string); ok && text != "" {
		switch event {
		case "public", "private", "emote", "pa":
			// fall through to rich formatting below
		default:
			lines := wrapTextLinkify("", "", text, max(width-2, 1), "")
			for i := range lines {
				lines[i] = slcpBodyStyle.Render(lines[i])
			}
			return strings.Join(lines, "\n")
		}
	}

	var timestamp string
	stamp, _ := d["stamp"].(bool)
	if stamp {
		if timeVal, ok := d["time"].(float64); ok && timeVal > 0 {
			t := time.Unix(int64(timeVal), 0)
			timestamp = fmt.Sprintf("(%02d:%02d) ", t.Hour(), t.Minute())
		}
	}

	entities := make(map[string]map[string]interface{})
	if entitiesRaw, ok := d["entities"].(map[string]interface{}); ok {
		for k, v := range entitiesRaw {
			if entity, ok := v.(map[string]interface{}); ok {
				entities[k] = entity
			}
		}
	}

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

	wrapMessage := func(prefix, msg string, width int) string {
		if msg == "" {
			return ""
		}
		lines := wrapTextLinkify(prefix, prefix, strings.TrimSpace(msg), width, "")
		return strings.Join(lines, "\n")
	}

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

	lookupDiscTitle := func(handle string) string {
		if entity, ok := entities[handle]; ok {
			if title, ok := entity["title"].(string); ok {
				return title
			}
		}
		return ""
	}

	sourceWithBlurb := func() string {
		name := lookupName(source)
		if entity, ok := entities[source]; ok {
			if blurb, ok := entity["blurb"].(string); ok && blurb != "" {
				return name + " [" + blurb + "]"
			}
		}
		return name
	}

	blurbSuffix := func() string {
		if entity, ok := entities[source]; ok {
			if blurb, ok := entity["blurb"].(string); ok && blurb != "" {
				return " with the blurb [" + blurb + "]"
			}
		}
		return ""
	}

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
		lines := wrapTextLinkify(headerStr, "> ", strings.TrimSpace(value), width, " ")
		return emoteBodyStyle.Render(strings.Join(lines, "\n"))

	case "connect":
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
			return banner(lookupName(source) + " has idled \"away\"")
		}
		if isSelf {
			return quiet("you are now away" + blurbSuffix())
		}
		return banner(lookupName(source) + " is now \"away\"")

	case "unidle":
		return banner(lookupName(source) + " is now unidle")

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
		return banner(lookupName(source) + " has destroyed a discussion")

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
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return banner("Discussion -" + lookupRecips(recips) + " is now named -" + value)
		}
		return banner("A discussion is now named -" + value)

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

	case "ignore":
		if value == "" && len(targetsRaw) == 0 && subEvt == "" {
			return banner(lookupName(source) + " is no longer ignoring you")
		}
		if value != "" {
			return banner(lookupName(source) + " is now ignoring you " + value)
		}
		return banner(lookupName(source) + " is now ignoring you")

	case "unignore":
		return banner(lookupName(source) + " is no longer ignoring you")

	case "review":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			return banner(lookupName(source) + " has cleared the review for discussion " + lookupRecips(recips))
		}
		return banner(lookupName(source) + " has cleared a review")

	case "sysmsg":
		return slcpBodyStyle.Render("*** " + linkifyText(value) + " ***")

	case "pa":
		return slcpBodyStyle.Render("** Public address message from " +
			formatUser(source, publicSenderStyle, publicBlurbStyle) + ": " + linkifyText(value) + " **")

	default:
		return fmt.Sprintf("[%s] %s %s", event, source, value)
	}
}
