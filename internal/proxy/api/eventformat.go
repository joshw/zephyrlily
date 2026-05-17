package api

import (
	"fmt"
	"strings"

	"github.com/joshw/zephyrlily/internal/slcp"
)

// formatEventText produces a plain-text representation of a Lily event for
// embedding in EventData.Text.
//
// Simple events (presence, identity, membership, permissions) are returned as
// a complete display string including "*** … ***" or "(…)" markers so any
// client can render them by applying its house style directly.
//
// Message events (public, private, emote, pa) get a compact single-line
// summary.  Rich clients (e.g. the TUI) should detect those event types and
// substitute their own formatted output.
func formatEventText(ev *slcp.NotifyEvent, entities map[string]EntityJSON, whoami string) string {
	isSelf := whoami != "" && ev.Source == whoami

	name := func(handle string) string {
		if e, ok := entities[handle]; ok && e.Name != "" {
			return e.Name
		}
		return handle
	}
	nameBlurb := func(handle string) string {
		if e, ok := entities[handle]; ok && e.Name != "" {
			if e.Blurb != "" {
				return e.Name + " [" + e.Blurb + "]"
			}
			return e.Name
		}
		return handle
	}
	joinRecips := func() string {
		ns := make([]string, 0, len(ev.Recips))
		for _, h := range ev.Recips {
			ns = append(ns, name(h))
		}
		return strings.Join(ns, ", ")
	}
	joinTargets := func() string {
		ns := make([]string, 0, len(ev.Targets))
		for _, h := range ev.Targets {
			ns = append(ns, name(h))
		}
		return strings.Join(ns, ", ")
	}
	meInTargets := false
	if whoami != "" {
		for _, h := range ev.Targets {
			if h == whoami {
				meInTargets = true
				break
			}
		}
	}

	src := name(ev.Source)
	srcB := nameBlurb(ev.Source)
	blurbSuf := ""
	if e, ok := entities[ev.Source]; ok && e.Blurb != "" {
		blurbSuf = " with the blurb [" + e.Blurb + "]"
	}

	q := func(msg string) string { return "(" + msg + ")" }
	b := func(msg string) string { return "*** " + msg + " ***" }

	switch ev.Event {

	// ── Message events: compact summary; rich clients should override ─────────

	case "public":
		recip := joinRecips()
		if recip != "" {
			return "From " + srcB + " to " + recip + ": " + ev.Value
		}
		return "From " + srcB + ": " + ev.Value

	case "private":
		return "Private from " + srcB + ": " + ev.Value

	case "emote":
		return src + " " + ev.Value

	case "pa":
		return "Public address from " + srcB + ": " + ev.Value

	// ── Presence ──────────────────────────────────────────────────────────────

	case "connect":
		return b(srcB + " has entered lily")

	case "disconnect":
		if ev.Value != "" {
			return b(srcB + " has left lily (" + ev.Value + ")")
		}
		return b(srcB + " has left lily")

	case "attach":
		return b(srcB + " has reattached")

	case "detach":
		if ev.Value != "" {
			return b(srcB + " has been detached " + ev.Value)
		}
		return b(srcB + " has detached")

	case "here":
		if isSelf {
			return q("you are now here" + blurbSuf)
		}
		return b(src + " is now \"here\"")

	case "away":
		if ev.Value != "" {
			return b(src + " has idled \"away\"")
		}
		if isSelf {
			return q("you are now away" + blurbSuf)
		}
		return b(src + " is now \"away\"")

	case "unidle":
		return b(src + " is now unidle")

	// ── Identity ──────────────────────────────────────────────────────────────

	case "rename":
		if isSelf {
			return q("you are now named " + ev.Value)
		}
		return b(src + " is now named " + ev.Value)

	case "blurb":
		if isSelf {
			if ev.Value != "" {
				return q("your blurb has been set to [" + ev.Value + "]")
			}
			return q("your blurb has been turned off")
		}
		if ev.Value != "" {
			return b(src + " has changed their blurb to [" + ev.Value + "]")
		}
		return b(src + " has turned their blurb off")

	case "info":
		disc := joinRecips()
		hasDisc := len(ev.Recips) > 0
		if isSelf {
			if hasDisc {
				if ev.Value == "" {
					return q("you have cleared the info for " + disc)
				}
				return q("you have changed the info for " + disc)
			}
			if ev.Value == "" {
				return q("your info has been cleared")
			}
			return q("your info has been changed")
		}
		if hasDisc {
			if ev.Value == "" {
				return b(src + " has cleared the info for discussion " + disc)
			}
			return b(src + " has changed the info for discussion " + disc)
		}
		if ev.Value == "" {
			return b(src + " has cleared their info")
		}
		return b(src + " has changed their info")

	// ── Discussion membership ─────────────────────────────────────────────────

	case "create":
		disc := joinRecips()
		if disc == "" {
			if isSelf {
				return q("you have created a discussion")
			}
			return b(src + " has created a discussion")
		}
		titlePart := ""
		if len(ev.Recips) > 0 {
			if e, ok := entities[ev.Recips[0]]; ok && e.Title != "" {
				titlePart = " \"" + e.Title + "\""
			}
		}
		if isSelf {
			return q("you have created discussion " + disc + titlePart)
		}
		return b(src + " has created discussion " + disc + titlePart)

	case "destroy":
		disc := joinRecips()
		if disc == "" {
			if isSelf {
				return q("you have destroyed a discussion")
			}
			return b(src + " has destroyed a discussion (server didn't say which)")
		}
		if isSelf {
			return q("you have destroyed discussion " + disc)
		}
		return b(src + " has destroyed discussion " + disc)

	case "join":
		disc := joinRecips()
		if disc == "" {
			if isSelf {
				return q("you have joined a discussion")
			}
			return b(src + " has joined a discussion")
		}
		if isSelf {
			return q("you have joined " + disc)
		}
		return b(src + " is now a member of " + disc)

	case "quit":
		disc := joinRecips()
		if disc == "" {
			if isSelf {
				return q("you have quit a discussion")
			}
			return b(src + " has quit a discussion")
		}
		if isSelf {
			return q("you have quit " + disc)
		}
		return b(src + " is no longer a member of " + disc)

	case "retitle":
		disc := joinRecips()
		if isSelf {
			if disc != "" {
				return q("you have changed the title of " + disc + " to \"" + ev.Value + "\"")
			}
			return q("you have changed a discussion title to \"" + ev.Value + "\"")
		}
		if disc != "" {
			return b(src + " has changed the title of " + disc + " to \"" + ev.Value + "\"")
		}
		return b(src + " has changed a discussion title to \"" + ev.Value + "\"")

	case "drename":
		disc := joinRecips()
		if disc != "" {
			return b("Discussion -" + disc + " is now named -" + ev.Value)
		}
		return b("A discussion is now named -" + ev.Value)

	// ── Permission events ─────────────────────────────────────────────────────

	case "permit":
		if len(ev.Recips) == 0 {
			return b(src + " has changed permissions")
		}
		disc, tgt, hasT := joinRecips(), joinTargets(), len(ev.Targets) > 0
		switch {
		case isSelf && meInTargets && ev.SubEvt == "owner":
			return q("you have accepted ownership of discussion " + disc)
		case isSelf && hasT && ev.SubEvt == "owner":
			return q("you have offered " + tgt + " ownership of discussion " + disc)
		case isSelf && hasT && ev.SubEvt != "":
			return q("you have given " + tgt + " " + ev.SubEvt + " privileges to discussion " + disc)
		case isSelf && !hasT && ev.SubEvt != "":
			return q(disc + " is no longer moderated")
		case meInTargets && ev.SubEvt == "owner":
			return b(src + " has offered you ownership of discussion " + disc)
		case meInTargets && ev.SubEvt != "":
			return b(src + " has given you " + ev.SubEvt + " privileges to discussion " + disc)
		case meInTargets:
			return b(src + " has permitted you to discussion " + disc)
		case hasT && ev.SubEvt == "owner":
			return b(src + " has taken ownership of discussion " + disc)
		case hasT && ev.SubEvt != "":
			return b(src + " has given " + tgt + " " + ev.SubEvt + " privileges to discussion " + disc)
		case hasT:
			return b(src + " has permitted " + tgt + " to discussion " + disc)
		case ev.SubEvt != "":
			return b(src + " has unmoderated discussion " + disc)
		}
		return b(src + " has changed permissions for discussion " + disc)

	case "depermit":
		if len(ev.Recips) == 0 {
			return b(src + " has changed permissions")
		}
		disc, tgt, hasT := joinRecips(), joinTargets(), len(ev.Targets) > 0
		switch {
		case isSelf && hasT && ev.SubEvt == "owner":
			return q("you have rescinded your offer to " + tgt + " for ownership of discussion " + disc)
		case isSelf && hasT && ev.SubEvt != "":
			return q("you have removed " + tgt + "'s " + ev.SubEvt + " privileges on discussion " + disc)
		case isSelf && !hasT && ev.SubEvt != "":
			return q(disc + " is now moderated")
		case meInTargets && ev.SubEvt == "owner":
			return b(src + " has rescinded their ownership offer of discussion " + disc)
		case meInTargets && ev.SubEvt != "":
			return b(src + " has removed your " + ev.SubEvt + " privileges on discussion " + disc)
		case meInTargets:
			return b(src + " has depermitted you from discussion " + disc)
		case hasT && ev.SubEvt != "":
			return b(src + " has removed " + tgt + "'s " + ev.SubEvt + " privileges on discussion " + disc)
		case hasT:
			return b(src + " has depermitted " + tgt + " from discussion " + disc)
		case ev.SubEvt != "":
			return b(src + " has moderated discussion " + disc)
		}
		return b(src + " has changed permissions for discussion " + disc)

	// ── Appointment events ────────────────────────────────────────────────────

	case "appoint":
		disc, tgt, hasT := joinRecips(), joinTargets(), len(ev.Targets) > 0
		switch {
		case isSelf && meInTargets && ev.SubEvt == "owner":
			return q("you have accepted ownership of discussion " + disc)
		case isSelf && ev.SubEvt == "owner":
			return q("you have offered " + tgt + " ownership of discussion " + disc)
		case meInTargets && ev.SubEvt == "owner":
			return b(src + " has offered you ownership of discussion " + disc)
		case ev.SubEvt == "owner":
			return b(src + " is now the owner of discussion " + disc)
		case !hasT && ev.SubEvt == "speaker":
			return b("discussion " + disc + " is now moderated")
		case meInTargets && ev.SubEvt == "speaker":
			return b("you have been made a speaker for discussion " + disc)
		case ev.SubEvt == "speaker":
			return b(tgt + " is now a speaker for discussion " + disc)
		case meInTargets && ev.SubEvt == "author":
			return b("you have been made an author for discussion " + disc)
		case ev.SubEvt == "author":
			return b(tgt + " is now an author for discussion " + disc)
		case meInTargets && ev.SubEvt != "":
			return b("you are now a " + ev.SubEvt + " for " + disc)
		case ev.SubEvt != "":
			return b(tgt + " is now a " + ev.SubEvt + " for " + disc)
		}
		return b(src + " made an appointment for discussion " + disc)

	case "unappoint":
		disc, tgt, hasT := joinRecips(), joinTargets(), len(ev.Targets) > 0
		switch {
		case isSelf && ev.SubEvt == "owner":
			return q("you have rescinded your ownership offer to " + tgt + " of discussion " + disc)
		case meInTargets && ev.SubEvt == "owner":
			return b(src + " has rescinded their offer of ownership of discussion " + disc)
		case !hasT && ev.SubEvt == "speaker":
			return b("discussion " + disc + " is no longer moderated")
		case meInTargets && ev.SubEvt == "speaker":
			return b("you are no longer a speaker for discussion " + disc)
		case ev.SubEvt == "speaker":
			return b(tgt + " is no longer a speaker for discussion " + disc)
		case meInTargets && ev.SubEvt == "author":
			return b("you are no longer an author for discussion " + disc)
		case ev.SubEvt == "author":
			return b(tgt + " is no longer an author for discussion " + disc)
		case meInTargets && ev.SubEvt != "":
			return b("you are no longer a " + ev.SubEvt + " for " + disc)
		case ev.SubEvt != "":
			return b(tgt + " is no longer a " + ev.SubEvt + " for " + disc)
		}
		return b(src + " changed an appointment for discussion " + disc)

	// ── Ignore / review ───────────────────────────────────────────────────────

	case "ignore":
		if ev.Value == "" && len(ev.Targets) == 0 && ev.SubEvt == "" {
			return b(src + " is no longer ignoring you")
		}
		if ev.Value != "" {
			return b(src + " is now ignoring you " + ev.Value)
		}
		return b(src + " is now ignoring you")

	case "unignore":
		return b(src + " is no longer ignoring you")

	case "review":
		disc := joinRecips()
		if disc != "" {
			return b(src + " has cleared the review for discussion " + disc)
		}
		return b(src + " has cleared a review")

	// ── System ────────────────────────────────────────────────────────────────

	case "sysmsg":
		return "*** " + ev.Value + " ***"
	}

	return fmt.Sprintf("[%s] %s %s", ev.Event, ev.Source, ev.Value)
}
