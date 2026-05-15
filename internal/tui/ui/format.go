package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// formatEvent produces a human-readable line for a notify event.
// Formatting is based on tigerlily's slcp_output.pl message templates.
// width is the maximum line width for wrapping messages.
func formatEvent(d map[string]interface{}, width int) string {
	event, _ := d["event"].(string)
	source, _ := d["source"].(string)
	value, _ := d["value"].(string)

	// Extract timestamp
	var timestamp string
	if timeVal, ok := d["time"].(float64); ok && timeVal > 0 {
		t := time.Unix(int64(timeVal), 0)
		timestamp = fmt.Sprintf("(%02d:%02d) ", t.Hour(), t.Minute())
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
	formatUser := func(handle string, senderStyle lipgloss.Style) string {
		if entity, ok := entities[handle]; ok {
			name, _ := entity["name"].(string)
			blurb, _ := entity["blurb"].(string)
			if name != "" {
				if blurb != "" {
					return senderStyle.Render(name) + " " + blurbStyle.Render(fmt.Sprintf("[%s]", blurb))
				}
				return senderStyle.Render(name)
			}
		}
		return senderStyle.Render(handle)
	}

	// Helper to format just the user name (without blurb)
	formatUserName := func(handle string, senderStyle lipgloss.Style) string {
		if entity, ok := entities[handle]; ok {
			if name, ok := entity["name"].(string); ok && name != "" {
				return senderStyle.Render(name)
			}
		}
		return senderStyle.Render(handle)
	}

	// Helper to wrap message text with prefix on each line
	wrapMessage := func(prefix, msg string, width int) string {
		if msg == "" {
			return ""
		}

		var lines []string
		words := strings.Fields(msg)
		if len(words) == 0 {
			return prefix
		}

		currentLine := prefix + words[0]
		for _, word := range words[1:] {
			if len(currentLine)+1+len(word) <= width {
				currentLine += " " + word
			} else {
				lines = append(lines, currentLine)
				currentLine = prefix + word
			}
		}
		lines = append(lines, currentLine)
		return strings.Join(lines, "\n")
	}

	switch event {
	case "public":
		// Format: " -> (timestamp) From user [blurb], to target1, target2:\n - message"
		var header strings.Builder
		header.WriteString(publicHeaderStyle.Render(" -> "))
		header.WriteString(timestampStyle.Render(timestamp))
		header.WriteString("From ")
		header.WriteString(formatUser(source, publicSenderStyle))

		// Add recipients
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			header.WriteString(", to ")
			recipNames := make([]string, 0, len(recips))
			for _, r := range recips {
				if recipStr, ok := r.(string); ok {
					recipNames = append(recipNames, recipStr)
				}
			}
			header.WriteString(strings.Join(recipNames, ", "))
		}
		header.WriteString(":")

		// Wrap message body with " - " prefix
		body := wrapMessage(" - ", value, width)
		return header.String() + "\n" + publicBodyStyle.Render(body)

	case "private":
		// Format: " >> (timestamp) Private message from user [blurb]:\n - message"
		var header strings.Builder
		header.WriteString(privateHeaderStyle.Render(" >> "))
		header.WriteString(timestampStyle.Render(timestamp))
		header.WriteString("Private message from ")
		header.WriteString(formatUser(source, privateSenderStyle))
		header.WriteString(":")

		// Wrap message body with " - " prefix
		body := wrapMessage(" - ", value, width)
		return header.String() + "\n" + privateBodyStyle.Render(body)

	case "emote":
		return emoteBodyStyle.Render(fmt.Sprintf("* %s %s", formatUserName(source, emoteSenderStyle), value))

	case "connect":
		return fmt.Sprintf("*** %s has entered lily ***", formatUser(source, publicSenderStyle))

	case "disconnect":
		if value != "" {
			return fmt.Sprintf("*** %s has left lily (%s) ***", formatUser(source, publicSenderStyle), value)
		}
		return fmt.Sprintf("*** %s has left lily ***", formatUser(source, publicSenderStyle))

	case "attach":
		return fmt.Sprintf("*** %s has reattached ***", formatUser(source, publicSenderStyle))

	case "detach":
		if value != "" {
			return fmt.Sprintf("*** %s has been detached %s ***", formatUser(source, publicSenderStyle), value)
		}
		return fmt.Sprintf("*** %s has detached ***", formatUser(source, publicSenderStyle))

	case "here":
		return fmt.Sprintf("*** %s is now \"here\" ***", formatUser(source, publicSenderStyle))

	case "away":
		return fmt.Sprintf("*** %s is now \"away\" ***", formatUser(source, publicSenderStyle))

	case "rename":
		return fmt.Sprintf("*** %s is now named %s ***", formatUserName(source, publicSenderStyle), value)

	case "blurb":
		if value != "" {
			return fmt.Sprintf("*** %s has changed their blurb to [%s] ***", formatUserName(source, publicSenderStyle), value)
		}
		return fmt.Sprintf("*** %s has turned their blurb off ***", formatUserName(source, publicSenderStyle))

	case "unidle":
		return fmt.Sprintf("*** %s is now unidle ***", formatUser(source, publicSenderStyle))

	case "create":
		// For discussion creation, RECIPS holds the discussion handle
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			if disc, ok := recips[0].(string); ok {
				return fmt.Sprintf("*** %s has created discussion %s ***", formatUserName(source, publicSenderStyle), disc)
			}
		}
		return fmt.Sprintf("*** %s has created a discussion ***", formatUserName(source, publicSenderStyle))

	case "destroy":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			if disc, ok := recips[0].(string); ok {
				return fmt.Sprintf("*** %s has destroyed discussion %s ***", formatUserName(source, publicSenderStyle), disc)
			}
		}
		return fmt.Sprintf("*** %s has destroyed a discussion ***", formatUserName(source, publicSenderStyle))

	case "join":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			if disc, ok := recips[0].(string); ok {
				return fmt.Sprintf("*** %s is now a member of %s ***", formatUserName(source, publicSenderStyle), disc)
			}
		}
		return fmt.Sprintf("*** %s has joined a discussion ***", formatUserName(source, publicSenderStyle))

	case "quit":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			if disc, ok := recips[0].(string); ok {
				return fmt.Sprintf("*** %s is no longer a member of %s ***", formatUserName(source, publicSenderStyle), disc)
			}
		}
		return fmt.Sprintf("*** %s has quit a discussion ***", formatUserName(source, publicSenderStyle))

	case "retitle":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			if disc, ok := recips[0].(string); ok {
				return fmt.Sprintf("*** %s has changed the title of %s to \"%s\" ***", formatUserName(source, publicSenderStyle), disc, value)
			}
		}
		return fmt.Sprintf("*** %s has changed a discussion title to \"%s\" ***", formatUserName(source, publicSenderStyle), value)

	case "drename":
		if recips, ok := d["recips"].([]interface{}); ok && len(recips) > 0 {
			if disc, ok := recips[0].(string); ok {
				return fmt.Sprintf("*** Discussion %s is now named %s ***", disc, value)
			}
		}
		return fmt.Sprintf("*** A discussion is now named %s ***", value)

	case "sysmsg":
		return fmt.Sprintf("*** %s ***", value)

	case "pa":
		return fmt.Sprintf("** Public address message from %s: %s **", formatUser(source, publicSenderStyle), value)

	default:
		// Unknown event type - show all available data
		return fmt.Sprintf("[%s] %s %s", event, source, value)
	}
}
