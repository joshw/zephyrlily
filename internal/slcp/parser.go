package slcp

import (
	"fmt"
	"strconv"
	"strings"
)

// Parse converts a single line received from the Lily server into a Message.
// The trailing newline (and optional carriage return) should be stripped before calling.
func Parse(line string) (*Message, error) {
	msg := &Message{Raw: line}

	switch {
	case line == "login:":
		msg.Type = MsgLoginPrompt
		return msg, nil

	case line == "password:":
		msg.Type = MsgPassPrompt
		return msg, nil

	case strings.HasPrefix(line, "%connected "):
		msg.Type = MsgConnected
		return msg, nil

	case line == "%pong":
		msg.Type = MsgPong
		return msg, nil

	case line == "%SLCP-SYNC beginning":
		msg.Type = MsgSyncBegin
		return msg, nil

	case line == "%SLCP-SYNC ending":
		msg.Type = MsgSyncEnd
		return msg, nil

	case strings.HasPrefix(line, "%prompt "):
		msg.Type = MsgPrompt
		msg.Text = strings.TrimPrefix(line, "%prompt ")
		return msg, nil

	case strings.HasPrefix(line, "%begin ["):
		return parseCmdBound(line, MsgCmdBegin)

	case strings.HasPrefix(line, "%end ["):
		return parseCmdBound(line, MsgCmdEnd)

	case strings.HasPrefix(line, "%options "):
		msg.Type = MsgOptions
		msg.Text = strings.TrimPrefix(line, "%options ")
		return msg, nil

	case strings.HasPrefix(line, "%export_file "):
		msg.Type = MsgExportFile
		msg.Text = strings.TrimPrefix(line, "%export_file ")
		return msg, nil

	case strings.HasPrefix(line, "%NOTIFY "):
		return parseParams(line[len("%NOTIFY "):], MsgNotify)

	case strings.HasPrefix(line, "%USER "):
		return parseParams(line[len("%USER "):], MsgUser)

	case strings.HasPrefix(line, "%DISC "):
		return parseParams(line[len("%DISC "):], MsgDisc)

	case strings.HasPrefix(line, "%GROUP "):
		return parseParams(line[len("%GROUP "):], MsgGroup)

	case strings.HasPrefix(line, "%DATA "):
		return parseParams(line[len("%DATA "):], MsgData)

	case strings.HasPrefix(line, "%server "):
		return parseParams(line[len("%server "):], MsgServer)

	default:
		msg.Type = MsgRaw
		msg.Text = line
		return msg, nil
	}
}

// parseCmdBound handles %begin [42] /cmd and %end [42].
func parseCmdBound(line string, t MsgType) (*Message, error) {
	// format: %begin [id] rest   or   %end [id]
	close := strings.Index(line, "]")
	if close == -1 {
		return nil, fmt.Errorf("malformed cmd bound: %q", line)
	}
	open := strings.Index(line, "[")
	idStr := line[open+1 : close]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return nil, fmt.Errorf("bad cmd id %q: %w", idStr, err)
	}
	text := ""
	if rest := strings.TrimSpace(line[close+1:]); rest != "" {
		text = rest
	}
	return &Message{Type: t, Raw: line, CmdID: id, Text: text}, nil
}

// parseParams parses the SLCP key=value parameter string after the message type token.
// Parameters may be:
//
//	NAME=VALUE          simple string value
//	NAME=LEN=DATA       length-prefixed binary value (DATA is LEN bytes, may contain spaces/newlines)
//	NAME                boolean flag (value is "1")
func parseParams(s string, t MsgType) (*Message, error) {
	params := make(map[string]string)
	pos := 0

	for pos < len(s) {
		// skip leading whitespace
		for pos < len(s) && s[pos] == ' ' {
			pos++
		}
		if pos >= len(s) {
			break
		}

		// read key
		keyStart := pos
		for pos < len(s) && s[pos] != '=' && s[pos] != ' ' {
			pos++
		}
		key := s[keyStart:pos]
		if key == "" {
			break
		}

		if pos >= len(s) || s[pos] == ' ' {
			// boolean flag
			params[key] = "1"
			continue
		}

		// consume '='
		pos++

		// peek: is this LEN=DATA or plain VALUE?
		// Try to read a decimal number followed by '='.
		numEnd := pos
		for numEnd < len(s) && s[numEnd] >= '0' && s[numEnd] <= '9' {
			numEnd++
		}
		if numEnd > pos && numEnd < len(s) && s[numEnd] == '=' {
			// length-prefixed
			length, err := strconv.Atoi(s[pos:numEnd])
			if err != nil {
				return nil, fmt.Errorf("bad length in param %q: %w", key, err)
			}
			pos = numEnd + 1 // skip past second '='
			if pos+length > len(s) {
				return nil, fmt.Errorf("param %q data truncated (need %d bytes, have %d)", key, length, len(s)-pos)
			}
			params[key] = s[pos : pos+length]
			pos += length
		} else {
			// plain value: read until next space
			valStart := pos
			for pos < len(s) && s[pos] != ' ' {
				pos++
			}
			params[key] = s[valStart:pos]
		}
	}

	return &Message{Type: t, Raw: s, Params: params}, nil
}

// ParseNotify extracts a NotifyEvent from a MsgNotify message.
func ParseNotify(m *Message) (*NotifyEvent, error) {
	if m.Type != MsgNotify {
		return nil, fmt.Errorf("not a NOTIFY message")
	}
	p := m.Params
	ev := &NotifyEvent{
		Event:  strings.ToLower(p["EVENT"]),
		Source: p["SOURCE"],
		Value:  p["VALUE"],
		SubEvt: p["SUBEVT"],
	}
	if _, ok := p["EMPTY"]; ok {
		ev.Empty = true
	}
	if ts, ok := p["TIME"]; ok {
		ev.Time, _ = strconv.ParseInt(ts, 10, 64)
	}
	if r, ok := p["RECIPS"]; ok && r != "" {
		ev.Recips = strings.Split(r, ",")
	}
	if t, ok := p["TARGETS"]; ok && t != "" {
		ev.Targets = strings.Split(t, ",")
	}
	return ev, nil
}

// ParseUser extracts a UserRecord from a MsgUser message.
func ParseUser(m *Message) (*UserRecord, error) {
	if m.Type != MsgUser {
		return nil, fmt.Errorf("not a USER message")
	}
	p := m.Params
	return &UserRecord{
		Handle:  p["HANDLE"],
		Name:    p["NAME"],
		Blurb:   p["BLURB"],
		State:   p["STATE"],
		Pronoun: p["PRONOUN"],
	}, nil
}

// ParseDisc extracts a DiscRecord from a MsgDisc message.
func ParseDisc(m *Message) (*DiscRecord, error) {
	if m.Type != MsgDisc {
		return nil, fmt.Errorf("not a DISC message")
	}
	p := m.Params
	dr := &DiscRecord{
		Handle: p["HANDLE"],
		Name:   p["NAME"],
		Title:  p["TITLE"],
		Attrib: p["ATTRIB"],
	}
	if ts, ok := p["CREATION"]; ok {
		dr.Creation, _ = strconv.ParseInt(ts, 10, 64)
	}
	return dr, nil
}

// ParseGroup extracts a GroupRecord from a MsgGroup message.
func ParseGroup(m *Message) (*GroupRecord, error) {
	if m.Type != MsgGroup {
		return nil, fmt.Errorf("not a GROUP message")
	}
	p := m.Params
	gr := &GroupRecord{Name: p["NAME"]}
	if members, ok := p["MEMBERS"]; ok && members != "" {
		gr.Members = strings.Split(members, ",")
	}
	return gr, nil
}
