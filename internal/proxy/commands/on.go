package commands

import (
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/joshw/zephyrlily/internal/lily"
	"github.com/joshw/zephyrlily/internal/slcp"
)

func init() {
	RegisterHelp(HelpTopic{
		Name: "on",
		Text: []string{
			"Run a command when a matching event occurs",
			"",
			"Usage:",
			"  %on list",
			"  %on clear <id>",
			"  %on <event> [filters...] <action>",
			"",
			"<event> is an event type. Useful ones: public, private, emote.",
			"",
			"Filters limit which events trigger the action:",
			"  from <user>      events from a given user",
			"  to <dest>        events sent to a given user or discussion",
			"  value <string>   the event value equals <string> exactly",
			"  like <regexp>    the event value matches <regexp> (case-insensitive)",
			"  random <N>       act on roughly one in N matches",
			"  once <interval>  act at most once per interval (see %help interval)",
			"                   (\"once a 5m\" and \"once every 5m\" also work)",
			"",
			"The action may use these substitutions:",
			"  $sender   the name of the event's sender",
			"  $target   where the event was sent",
			"  $value    the event's value",
			"  $1 .. $9  capture groups from the \"like\" regexp",
			"",
			"Quote arguments containing spaces, e.g. like \"ping (.*)\".",
			"Events you send yourself are ignored unless a \"from\" filter names you.",
			"",
			"Examples:",
			"  %on emote to bar like \"yawn\" once 5m \"bar;yawns.\"",
			"  %on public like \"ping (.*)\" \"$sender;pong $1\"",
			"",
			"(tlily's %eval and %attr actions are not supported.)",
		},
	})
	RegisterHelp(HelpTopic{Name: "attr", Text: []string{"see %help on"}})
}

// onHandler is a single registered %on trigger.
type onHandler struct {
	id    int
	event string // lowercased event type

	fromName, fromHandle string // "from" filter, resolved ("" if unset)
	toName, toHandle     string // "to" filter, resolved ("" if unset)

	likeSrc string         // original "like" pattern, for listing
	like    *regexp.Regexp // compiled "like" pattern ("(?i)"-anchored), nil if unset

	value    string
	hasValue bool

	random int           // act on ~1-in-N matches; 0 = always
	once   time.Duration // minimum spacing between fires; 0 = no throttle

	action    string
	lastFired time.Time
}

// OnTable holds a session's %on event triggers. It is safe for concurrent use:
// the command handler runs on the dispatch goroutine while Dispatch runs on the
// fanOut goroutine.
type OnTable struct {
	mu       sync.Mutex
	next     int
	handlers map[int]*onHandler

	fire     func(line string)
	announce func(lines []string)
}

// NewOnTable returns an empty OnTable. fire re-dispatches a matched action;
// announce publishes informational output.
func NewOnTable(fire func(line string), announce func(lines []string)) *OnTable {
	return &OnTable{
		handlers: make(map[int]*onHandler),
		fire:     fire,
		announce: announce,
	}
}

const onUsage = "(usage: %on list | clear <id> | <event> [filters...] <action>; see %help on)"

// onKeywords are the recognised filter keywords in an %on spec.
var onKeywords = map[string]bool{
	"from": true, "to": true, "value": true,
	"like": true, "random": true, "once": true,
}

// HandleCommand implements the %on command. raw is the text following "%on";
// state resolves names to handles for from/to filters.
func (o *OnTable) HandleCommand(raw string, state *lily.State, respond func(lines []string)) {
	args := tokenize(raw)

	// %on / %on list — list current handlers.
	if len(args) == 0 || (len(args) == 1 && args[0] == "list") {
		respond(o.list())
		return
	}

	// %on clear <id>
	if args[0] == "clear" {
		if len(args) != 2 {
			respond([]string{onUsage})
			return
		}
		respond(o.clear(args[1]))
		return
	}

	if len(args) < 2 {
		respond([]string{onUsage})
		return
	}

	h := &onHandler{event: strings.ToLower(args[0])}
	i := 1
	for i < len(args) && onKeywords[args[i]] {
		kw := args[i]
		i++
		if i >= len(args) {
			respond([]string{onUsage})
			return
		}
		switch kw {
		case "from":
			e := state.LookupName(args[i])
			if e == nil {
				respond([]string{fmt.Sprintf("(%s not found)", args[i])})
				return
			}
			h.fromName, h.fromHandle = e.Name, e.Handle
		case "to":
			e := state.LookupName(args[i])
			if e == nil {
				respond([]string{fmt.Sprintf("(%s not found)", args[i])})
				return
			}
			h.toName, h.toHandle = e.Name, e.Handle
		case "value":
			h.value, h.hasValue = args[i], true
		case "like":
			re, err := regexp.Compile("(?i)" + args[i])
			if err != nil {
				respond([]string{fmt.Sprintf("(bad regexp %q: %s)", args[i], err)})
				return
			}
			h.likeSrc, h.like = args[i], re
		case "random":
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				respond([]string{fmt.Sprintf("(random needs a positive number, got %q)", args[i])})
				return
			}
			h.random = n
		case "once":
			// Allow "once a <interval>" and "once every <interval>".
			if args[i] == "a" || args[i] == "every" {
				i++
				if i >= len(args) {
					respond([]string{onUsage})
					return
				}
			}
			d, ok := parseInterval(args[i])
			if !ok {
				respond([]string{fmt.Sprintf("(bad interval %q; see %%help interval)", args[i])})
				return
			}
			h.once = d
		}
		i++
	}

	action := strings.TrimSpace(strings.Join(args[i:], " "))
	if action == "" {
		respond([]string{onUsage})
		return
	}
	h.action = action

	o.mu.Lock()
	h.id = o.next
	o.next++
	o.handlers[h.id] = h
	o.mu.Unlock()

	respond([]string{o.confirm(h)})
}

// confirm returns the registration acknowledgement line.
func (o *OnTable) confirm(h *onHandler) string {
	var b strings.Builder
	fmt.Fprintf(&b, "(on %s events", h.event)
	if h.fromName != "" {
		fmt.Fprintf(&b, " from %s", h.fromName)
	}
	if h.toName != "" {
		fmt.Fprintf(&b, " to %s", h.toName)
	}
	if h.like != nil {
		fmt.Fprintf(&b, " like %q", h.likeSrc)
	}
	if h.hasValue {
		fmt.Fprintf(&b, " with value %q", h.value)
	}
	if h.once > 0 {
		fmt.Fprintf(&b, " no more than once per %s", h.once)
	}
	if h.random > 0 {
		fmt.Fprintf(&b, " randomly")
	}
	fmt.Fprintf(&b, ", I will run %q [id %d].)", h.action, h.id)
	return b.String()
}

// list returns a description of every registered handler.
func (o *OnTable) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.handlers) == 0 {
		return []string{"(no %on handlers are registered)"}
	}

	ids := make([]int, 0, len(o.handlers))
	for id := range o.handlers {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	out := []string{fmt.Sprintf("%3s  %s", "Id", "Trigger")}
	for _, id := range ids {
		h := o.handlers[id]
		desc := "TYPE " + h.event
		if h.fromName != "" {
			desc += " FROM " + h.fromName
		}
		if h.toName != "" {
			desc += " TO " + h.toName
		}
		if h.like != nil {
			desc += fmt.Sprintf(" LIKE %q", h.likeSrc)
		}
		if h.hasValue {
			desc += fmt.Sprintf(" VALUE %q", h.value)
		}
		if h.random > 0 {
			desc += fmt.Sprintf(" RANDOM %d", h.random)
		}
		if h.once > 0 {
			desc += " ONCE " + h.once.String()
		}
		out = append(out, fmt.Sprintf("%3d  %s", id, desc))
		out = append(out, "     => "+h.action)
	}
	return out
}

// clear removes the handler named by the id string.
func (o *OnTable) clear(idStr string) []string {
	id, err := strconv.Atoi(idStr)
	if err != nil {
		return []string{fmt.Sprintf("(not a handler id: %s)", idStr)}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.handlers[id]; !ok {
		return []string{fmt.Sprintf("(%%on handler id %d not found)", id)}
	}
	delete(o.handlers, id)
	return []string{fmt.Sprintf("(%%on handler id %d removed)", id)}
}

// Dispatch evaluates every handler against an incoming event and fires the
// actions of those that match. Matching (including random/once side effects) is
// done under the lock; fire/announce run unlocked.
func (o *OnTable) Dispatch(ev *slcp.NotifyEvent, state *lily.State) {
	now := time.Now()

	o.mu.Lock()
	ids := make([]int, 0, len(o.handlers))
	for id := range o.handlers {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	var actions []string
	for _, id := range ids {
		h := o.handlers[id]
		if !h.matches(ev, state, now) {
			continue
		}
		actions = append(actions, h.expand(ev, state))
	}
	o.mu.Unlock()

	for _, a := range actions {
		o.announce([]string{"[%on] " + a})
		o.fire(a)
	}
}

// matches reports whether ev triggers h, applying random/once side effects. The
// caller must hold o.mu.
func (h *onHandler) matches(ev *slcp.NotifyEvent, state *lily.State, now time.Time) bool {
	if h.event != ev.Event {
		return false
	}
	// Ignore our own sends unless a "from" filter explicitly names us — this
	// prevents an action from re-triggering its own handler in a loop.
	if h.fromHandle == "" && ev.Source == state.Whoami {
		return false
	}
	if h.fromHandle != "" && ev.Source != h.fromHandle {
		return false
	}
	if h.toHandle != "" && !containsStr(ev.Recips, h.toHandle) {
		return false
	}
	if h.hasValue && ev.Value != h.value {
		return false
	}
	if h.like != nil && !h.like.MatchString(ev.Value) {
		return false
	}
	if h.random > 0 && rand.Intn(h.random) != 0 {
		return false
	}
	if h.once > 0 && !h.lastFired.IsZero() && now.Sub(h.lastFired) < h.once {
		return false
	}
	h.lastFired = now
	return true
}

// expand substitutes $sender/$target/$value/$1..$9 into the action.
func (h *onHandler) expand(ev *slcp.NotifyEvent, state *lily.State) string {
	action := h.action

	if h.like != nil {
		groups := h.like.FindStringSubmatch(ev.Value)
		for n := 1; n <= 9; n++ {
			val := ""
			if n < len(groups) {
				val = groups[n]
			}
			action = strings.ReplaceAll(action, "$"+strconv.Itoa(n), val)
		}
	}

	action = strings.ReplaceAll(action, "$sender", nameFor(state, ev.Source))
	action = strings.ReplaceAll(action, "$target", namesFor(state, ev.Recips))
	action = strings.ReplaceAll(action, "$value", ev.Value)
	return action
}

// nameFor returns a send-safe name for a handle (spaces become underscores),
// falling back to the handle itself when it is unknown.
func nameFor(state *lily.State, handle string) string {
	name := handle
	if e := state.LookupHandle(handle); e != nil && e.Name != "" {
		name = e.Name
	}
	return strings.ReplaceAll(name, " ", "_")
}

// namesFor maps a list of handles to a comma-separated list of send-safe names.
func namesFor(state *lily.State, handles []string) string {
	names := make([]string, 0, len(handles))
	for _, h := range handles {
		names = append(names, nameFor(state, h))
	}
	return strings.Join(names, ",")
}

// Count returns the number of registered handlers (for teardown logging).
func (o *OnTable) Count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.handlers)
}

func containsStr(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// tokenize splits a command line into words, honouring single and double
// quotes (which are stripped). It is a small substitute for the quotewords
// parsing tlily's %on relies on for quoted regexps and actions.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inToken := false
	var quote rune // 0 when not inside a quoted section

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t':
			if inToken {
				out = append(out, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if inToken {
		out = append(out, cur.String())
	}
	return out
}
