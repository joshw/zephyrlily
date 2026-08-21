package commands

import (
	"reflect"
	"strings"
	"testing"
)

// Command names and the fixed keyword vocabularies of their arguments match
// case-insensitively; the free-form values that follow those keywords do not.
// See internal/cmdarg.

// TestOnKeywordsIgnoreCase is the regression test for the reported bug: an
// uppercase LIKE was not recognised as a filter keyword, so it and its regexp
// were silently swallowed into the action text instead.
func TestOnKeywordsIgnoreCase(t *testing.T) {
	want := onConfirm(t, `public like "ping (.*)" "$sender;pong $1"`)
	for _, spec := range []string{
		`public LIKE "ping (.*)" "$sender;pong $1"`,
		`PUBLIC Like "ping (.*)" "$sender;pong $1"`,
	} {
		got := onConfirm(t, spec)
		if got != want {
			t.Errorf("%q registered as\n  %s\nwant\n  %s", spec, got, want)
		}
		if !strings.Contains(got, `like "ping (.*)"`) {
			t.Errorf("%q did not parse the regexp as a like filter: %s", spec, got)
		}
	}
}

// The like regexp is a free-form value: lowercasing it would turn \S, \W and \B
// into their inverses and collapse character classes, so it is stored verbatim.
func TestOnFilterValuesKeepCase(t *testing.T) {
	got := onConfirm(t, `public LIKE "[A-Z]+\S" VALUE "MixedCase" "$sender;Reply Here"`)
	// The confirmation renders values with %q, so the regexp's backslash is
	// doubled there; what matters is that neither [A-Z] nor \S was folded.
	for _, want := range []string{`like "[A-Z]+\\S"`, `with value "MixedCase"`, `"$sender;Reply Here"`} {
		if !strings.Contains(got, want) {
			t.Errorf("confirm = %s, want it to contain %s", got, want)
		}
	}
}

func TestOnSubcommandsIgnoreCase(t *testing.T) {
	o, _ := newOn()
	var reg []string
	o.HandleCommand(`public like "x" ";a"`, onState(), collect(&reg))

	var listed []string
	o.HandleCommand("LIST", onState(), collect(&listed))
	if len(listed) == 0 || strings.Contains(strings.Join(listed, "\n"), "no %on handlers") {
		t.Fatalf("LIST did not list handlers: %v", listed)
	}

	var cleared, after []string
	o.HandleCommand("CLEAR 0", onState(), collect(&cleared))
	o.HandleCommand("list", onState(), collect(&after))
	if len(after) != 1 || !strings.Contains(after[0], "no %on handlers") {
		t.Fatalf("CLEAR 0 did not remove the handler (%v): %v", cleared, after)
	}
}

func TestOnOnceKeywordIgnoresCase(t *testing.T) {
	want := onConfirm(t, `public once every 5m ";a"`)
	for _, spec := range []string{
		`public ONCE EVERY 5m ";a"`,
		`public once A 5m ";a"`,
		`public ONCE a 5M ";a"`,
	} {
		if got := onConfirm(t, spec); got != want {
			t.Errorf("%q registered as\n  %s\nwant\n  %s", spec, got, want)
		}
	}
}

// onConfirm registers one %on handler in a fresh table and returns the
// confirmation line, which spells out how the spec was parsed.
func onConfirm(t *testing.T, raw string) string {
	t.Helper()
	o, _ := newOn()
	var out []string
	o.HandleCommand(raw, onState(), collect(&out))
	if len(out) != 1 {
		t.Fatalf("HandleCommand(%q) returned %d lines: %v", raw, len(out), out)
	}
	return out[0]
}

func TestParseIntervalIgnoresUnitCase(t *testing.T) {
	for _, tc := range []struct{ lower, upper string }{
		{"30s", "30S"}, {"5m", "5M"}, {"2h", "2H"}, {"1d", "1D"},
	} {
		want, ok := parseInterval(tc.lower)
		if !ok {
			t.Fatalf("parseInterval(%q) failed", tc.lower)
		}
		got, ok := parseInterval(tc.upper)
		if !ok || got != want {
			t.Errorf("parseInterval(%q) = (%v, %v), want (%v, true)", tc.upper, got, ok, want)
		}
	}
}

func TestCronKeywordsIgnoreCase(t *testing.T) {
	r := &recorder{}
	c := NewCronTable(r.fire, r.announce)

	// The after/every keyword folds; the command text after it keeps its case.
	var out []string
	c.HandleCommand("cron", []string{"AFTER", "1h", ";Send This"}, collect(&out))
	if len(out) != 1 || strings.Contains(out[0], "usage") {
		t.Fatalf("AFTER not recognised: %v", out)
	}
	if listed := strings.Join(c.list(), "\n"); !strings.Contains(listed, ";Send This") {
		t.Errorf("command text lost its case: %s", listed)
	}

	var cancelled []string
	c.HandleCommand("cron", []string{"CANCEL", "0"}, collect(&cancelled))
	if got := c.list(); len(got) != 1 || !strings.Contains(got[0], "no scheduled tasks") {
		t.Errorf("CANCEL did not cancel the job (%v): %v", cancelled, got)
	}
}

func TestAliasNamesIgnoreCase(t *testing.T) {
	a := NewAliasTable()

	// A mixed-case name is accepted but stored folded, so the confirmation
	// echoes the spelling that will actually resolve.
	var defined []string
	a.HandleCommand([]string{"Greet", "/who", "Beener"}, collect(&defined))
	if !reflect.DeepEqual(defined, []string{"(%greet is now aliased to '/who Beener')"}) {
		t.Fatalf("define = %q", defined)
	}

	for _, line := range []string{"%greet", "%Greet", "%GREET"} {
		got, ok := a.Expand(line)
		if !ok {
			t.Errorf("Expand(%q) did not resolve", line)
			continue
		}
		// The expansion template keeps its case.
		if !reflect.DeepEqual(got, []string{"/who Beener"}) {
			t.Errorf("Expand(%q) = %q", line, got)
		}
	}

	// %alias itself is never expanded, in any case.
	if _, ok := a.Expand("%ALIAS foo bar"); ok {
		t.Error("%ALIAS was expanded as an alias")
	}

	var listed []string
	a.HandleCommand([]string{"LIST", "GREET"}, collect(&listed))
	if !reflect.DeepEqual(listed, []string{"greet: /who Beener"}) {
		t.Fatalf("LIST GREET = %q", listed)
	}

	var cleared []string
	a.HandleCommand([]string{"CLEAR", "GREET"}, collect(&cleared))
	if _, ok := a.Expand("%greet"); ok {
		t.Errorf("alias still defined after CLEAR GREET (%v)", cleared)
	}
}

func TestRegisteredCommandNamesIgnoreCase(t *testing.T) {
	for _, name := range []string{"%version", "%VERSION", "%Version", "%HELP"} {
		if !IsRegistered(name) {
			t.Errorf("IsRegistered(%q) = false", name)
		}
	}
	if IsRegistered("%nosuchcommand") {
		t.Error("IsRegistered reported an unknown command as registered")
	}

	// %help topics are a closed set too.
	var lower, upper []string
	Execute(onState(), "%help on", collect(&lower))
	Execute(onState(), "%HELP ON", collect(&upper))
	if len(lower) == 0 {
		t.Fatal("%help on returned nothing")
	}
	if !reflect.DeepEqual(lower, upper) {
		t.Errorf("%%HELP ON = %v, want same as %%help on", upper)
	}

	// An unknown command is echoed back as the user spelled it.
	var unknown []string
	Execute(onState(), "%NoSuchThing", collect(&unknown))
	if len(unknown) != 1 || !strings.Contains(unknown[0], "%NoSuchThing") {
		t.Errorf("unknown-command message = %v, want it to quote the original spelling", unknown)
	}
}

func TestDebugSubcommandsIgnoreCase(t *testing.T) {
	var lower, upper []string
	handleDebug(onState(), []string{"users"}, collect(&lower))
	handleDebug(onState(), []string{"USERS"}, collect(&upper))
	if len(lower) == 0 {
		t.Fatalf("%%debug users returned nothing")
	}
	if !reflect.DeepEqual(lower, upper) {
		t.Errorf("%%debug USERS = %v, want same as %%debug users", upper)
	}

	var bad []string
	handleDebug(onState(), []string{"nope"}, collect(&bad))
	if len(bad) == 0 || !strings.Contains(bad[0], "Unknown subcommand") {
		t.Errorf("unknown subcommand = %v", bad)
	}
}
