package commands

import (
	"strings"
	"testing"

	"github.com/joshw/zephyrlily/internal/lily"
	"github.com/joshw/zephyrlily/internal/slcp"
)

// onState returns a populated state: we are "#me", with users Alice/Bob and a
// "bar" entity to use as a recipient.
func onState() *lily.State {
	s := lily.NewState()
	s.Whoami = "#me"
	s.ApplyUser(&slcp.UserRecord{Handle: "#me", Name: "me"})
	s.ApplyUser(&slcp.UserRecord{Handle: "#1", Name: "Alice"})
	s.ApplyUser(&slcp.UserRecord{Handle: "#2", Name: "Bob"})
	s.ApplyUser(&slcp.UserRecord{Handle: "#3", Name: "bar"})
	return s
}

func newOn() (*OnTable, *recorder) {
	r := &recorder{}
	return NewOnTable(r.fire, r.announce), r
}

func TestOnRegisterAndList(t *testing.T) {
	o, _ := newOn()
	var out []string
	o.HandleCommand(`public like "ping (.*)" "$sender;pong $1"`, onState(), collect(&out))
	if len(out) != 1 || !strings.Contains(out[0], "id 0") {
		t.Fatalf("unexpected register output: %v", out)
	}

	var list []string
	o.HandleCommand("list", onState(), collect(&list))
	joined := strings.Join(list, "\n")
	if !strings.Contains(joined, "TYPE public") || !strings.Contains(joined, "$sender;pong $1") {
		t.Fatalf("list missing handler details: %v", list)
	}
}

func TestOnMatchAndSubstitute(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public like "ping (.*)" "$sender;pong $1"`, onState(), collect(&out))

	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "ping there"}, onState())

	fired := r.firedCopy()
	if len(fired) != 1 || fired[0] != "Alice;pong there" {
		t.Fatalf("expected substituted action, got %v", fired)
	}
	if len(r.announced) != 1 || !strings.Contains(r.announced[0], "[%on] Alice;pong there") {
		t.Fatalf("expected announcement, got %v", r.announced)
	}
}

func TestOnNoMatchOnValue(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public like "^ping$" ";nope"`, onState(), collect(&out))
	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "pinging"}, onState())
	if got := len(r.firedCopy()); got != 0 {
		t.Fatalf("expected no fire, got %d", got)
	}
}

func TestOnSelfSendSuppressed(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public like "x" ";loop"`, onState(), collect(&out))

	// Our own send must not trigger the handler.
	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#me", Value: "x"}, onState())
	if got := len(r.firedCopy()); got != 0 {
		t.Fatalf("self-send should be suppressed, fired %d", got)
	}

	// But an explicit "from me" filter opts back in.
	o2, r2 := newOn()
	o2.HandleCommand(`public from me like "x" ";ok"`, onState(), collect(&out))
	o2.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#me", Value: "x"}, onState())
	if got := len(r2.firedCopy()); got != 1 {
		t.Fatalf("explicit from-self should fire, fired %d", got)
	}
}

func TestOnFromFilter(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public from Alice ";hi"`, onState(), collect(&out))

	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#2", Value: "yo"}, onState())
	if got := len(r.firedCopy()); got != 0 {
		t.Fatalf("from Bob should not match, fired %d", got)
	}
	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "yo"}, onState())
	if got := len(r.firedCopy()); got != 1 {
		t.Fatalf("from Alice should match, fired %d", got)
	}
}

func TestOnToFilter(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public to bar ";hi"`, onState(), collect(&out))

	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "yo", Recips: []string{"#2"}}, onState())
	if got := len(r.firedCopy()); got != 0 {
		t.Fatalf("to someone-else should not match, fired %d", got)
	}
	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "yo", Recips: []string{"#3"}}, onState())
	if got := len(r.firedCopy()); got != 1 {
		t.Fatalf("to bar should match, fired %d", got)
	}
}

func TestOnValueExact(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public value foo ";hi"`, onState(), collect(&out))
	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "foobar"}, onState())
	if got := len(r.firedCopy()); got != 0 {
		t.Fatalf("non-exact value should not match, fired %d", got)
	}
	o.Dispatch(&slcp.NotifyEvent{Event: "public", Source: "#1", Value: "foo"}, onState())
	if got := len(r.firedCopy()); got != 1 {
		t.Fatalf("exact value should match, fired %d", got)
	}
}

func TestOnOnceThrottle(t *testing.T) {
	o, r := newOn()
	var out []string
	o.HandleCommand(`public once 1h like "x" ";hi"`, onState(), collect(&out))
	ev := &slcp.NotifyEvent{Event: "public", Source: "#1", Value: "x"}
	o.Dispatch(ev, onState())
	o.Dispatch(ev, onState())
	if got := len(r.firedCopy()); got != 1 {
		t.Fatalf("once should throttle to a single fire, got %d", got)
	}
}

func TestOnClear(t *testing.T) {
	o, _ := newOn()
	var out []string
	o.HandleCommand(`public like "x" ";hi"`, onState(), collect(&out))

	var clr []string
	o.HandleCommand("clear 0", onState(), collect(&clr))
	if len(clr) != 1 || !strings.Contains(clr[0], "removed") {
		t.Fatalf("unexpected clear output: %v", clr)
	}
	var list []string
	o.HandleCommand("list", onState(), collect(&list))
	if len(list) != 1 || !strings.Contains(list[0], "no %on handlers") {
		t.Fatalf("expected empty handler list, got %v", list)
	}
}

func TestOnUnknownFromName(t *testing.T) {
	o, _ := newOn()
	var out []string
	o.HandleCommand(`public from Nobody ";hi"`, onState(), collect(&out))
	if len(out) != 1 || !strings.Contains(out[0], "not found") {
		t.Fatalf("expected not-found error, got %v", out)
	}
}

func TestTokenize(t *testing.T) {
	got := tokenize(`public like "ping (.*)" "$sender;pong $1"`)
	want := []string{"public", "like", "ping (.*)", "$sender;pong $1"}
	if len(got) != len(want) {
		t.Fatalf("tokenize len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokenize[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
