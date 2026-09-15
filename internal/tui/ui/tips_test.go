package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/joshw/zephyrlily/internal/proxy/commands"
)

func TestTipPoolIntegrity(t *testing.T) {
	require.NotEmpty(t, tips)

	seen := map[string]bool{}
	for _, tp := range tips {
		t.Run(tp.name, func(t *testing.T) {
			assert.NotEmpty(t, tp.name)
			assert.NotEmpty(t, tp.heading, "the listing shows headings, so there must be one")
			assert.NotEmpty(t, tp.short, "something has to print in-session")
			assert.NotEmpty(t, tp.long, "%help tips <name> has to have something to say")

			// Names are what someone types after '%help tips', and lookupTip
			// folds case; a name that is not already folded could never match.
			assert.Equal(t, strings.ToLower(tp.name), tp.name, "names are lowercase")
			assert.NotContains(t, tp.name, " ", "names are one word")
			assert.False(t, seen[tp.name], "duplicate tip name")
			seen[tp.name] = true

			// The in-session notice is appended as a single output item, and
			// syncViewportContent pauses at -- MORE -- when one append exceeds a
			// viewport height. A tip is not worth a pager stop.
			assert.LessOrEqual(t, len(tp.short), 4,
				"keep the in-session part short; put the rest in long")
			for _, l := range tp.short {
				assert.LessOrEqual(t, len(l), 76,
					"short lines are pre-wrapped, so they must fit a narrow window")
			}
		})
	}
}

// tipTextRefs finds the '%help <topic>' and '%<command>' mentions in a tip.
var (
	helpRefPattern = regexp.MustCompile(`%help ([a-z]+)`)
	cmdRefPattern  = regexp.MustCompile(`%([a-z-]+)`)
)

// TestTipsDoNotDriftFromReality is the test that keeps the pool honest. Tips
// are prose about features, and prose does not fail to compile when the feature
// it describes is renamed or removed.
func TestTipsDoNotDriftFromReality(t *testing.T) {
	// Commands the TUI answers itself, which are a hand-written chain in
	// applyLocalCommand/handleLocalCommand rather than anything enumerable.
	tuiCommands := map[string]bool{
		"help": true, "debug": true, "set": true, "mouse": true, "page": true,
		"linkpreview": true, "shorten": true, "tips": true, "style": true,
		"spell": true, "info": true, "memo": true,
		"save-password": true, "forget-password": true,
		// Owned by the proxy's session managers rather than its command
		// registry, so IsRegistered does not know them (see dispatchLine).
		"startup": true, "sync": true, "alias": true, "on": true,
		"after": true, "every": true, "cron": true, "echo": true,
	}

	// Mentions that are not claims about a command existing: prose, and the
	// example aliases a reader would define for themselves. Listed one by one
	// rather than loosened out of the pattern, so that a real typo still fails.
	notCommands := map[string]bool{
		"commands": true, // "Lily's '/' commands ... zlily's '%' commands"
		"inbeener": true, // the examples in the alias tip
		"hi":       true, // ditto
		"greet":    true, // and its case-insensitivity example
	}

	for _, tp := range tips {
		t.Run(tp.name, func(t *testing.T) {
			text := strings.Join(append(append([]string{tp.heading}, tp.short...), tp.long...), "\n")

			for _, mt := range helpRefPattern.FindAllStringSubmatch(text, -1) {
				topic := mt[1]
				if topic == "tips" || topic == "keys" {
					continue // generated, not registry entries
				}
				_, inTUI := tuiHelp[topic]
				inProxy := commands.GetHelp(topic) != nil
				assert.True(t, inTUI || inProxy,
					"%s points at '%%help %s', which no registry answers", tp.name, topic)
			}

			for _, mt := range cmdRefPattern.FindAllStringSubmatch(text, -1) {
				name := mt[1]
				if notCommands[name] || tuiCommands[name] || commands.IsRegistered("%"+name) {
					continue
				}
				assert.Fail(t, "unknown command in tip",
					"%s mentions '%%%s', which nothing dispatches", tp.name, name)
			}
		})
	}
}

// specimen is a tip to assert against, taken from the pool rather than named,
// so that removing a tip does not break tests that were only using it as an
// example.
func specimen(t *testing.T) tip {
	t.Helper()
	require.NotEmpty(t, tips)
	return tips[0]
}

func TestTipLookup(t *testing.T) {
	t.Run("case-insensitive like every other argument", func(t *testing.T) {
		want := specimen(t).name
		for _, name := range []string{
			want,
			strings.ToUpper(want),
			strings.ToUpper(want[:1]) + want[1:],
		} {
			got, ok := lookupTip(name)
			require.True(t, ok, name)
			assert.Equal(t, want, got.name)
		}
	})

	t.Run("an unknown name is not found", func(t *testing.T) {
		_, ok := lookupTip("nonesuch")
		assert.False(t, ok)
	})
}

func TestTipsListing(t *testing.T) {
	out := strings.Join(tipsListing(), "\n")
	for _, tp := range tips {
		assert.Contains(t, out, tp.name, "every tip is findable by name")
		assert.Contains(t, out, tp.heading, "and shows what it is about")
	}
	assert.Contains(t, out, "%help tips <name>", "the listing says how to read one")
	assert.Contains(t, out, "%tips off", "and how to stop them")
}

func TestTipHelp(t *testing.T) {
	t.Run("prints the long form", func(t *testing.T) {
		tp := specimen(t)
		out := tipHelp(strings.ToUpper(tp.name))
		assert.Equal(t, tp.heading, out[0], "headed by what it is about")
		assert.Contains(t, strings.Join(out, "\n"), tp.long[0])
	})

	t.Run("an unknown name lists the real ones", func(t *testing.T) {
		tp := specimen(t)
		out := strings.Join(tipHelp("nonesuch"), "\n")
		assert.Contains(t, out, "No such tip: nonesuch")
		// The short list of names, not the whole listing again: someone who
		// mistyped wants the spelling, not every heading.
		assert.Contains(t, out, tp.name)
		assert.NotContains(t, out, tp.heading)
	})
}

func TestTipNotice(t *testing.T) {
	tp, ok := lookupTip("mouse")
	require.True(t, ok)
	out := tipNotice(tp)
	joined := strings.Join(out, " ")
	assert.Contains(t, joined, "Tip: ")
	assert.Contains(t, joined, tp.heading)
	assert.Contains(t, joined, tp.short[0])
	assert.Contains(t, joined, "%help tips mouse",
		"a tip seen in passing has to be findable again")
}

func TestTipsCommand(t *testing.T) {
	// The setting is written through to the store, so point the store at a
	// scratch directory rather than the developer's real one.
	t.Setenv("ZLILY_CONFIG_DIR", t.TempDir())

	t.Run("off then on round-trips", func(t *testing.T) {
		m := Model{tipsEnabled: true}
		m, out := m.handleTipsCommand([]string{"%tips", "off"})
		assert.False(t, m.tipsEnabled)
		assert.Contains(t, strings.Join(out, " "), "off")
		assert.True(t, loadTipsOff(), "and it is remembered")

		m, out = m.handleTipsCommand([]string{"%tips", "on"})
		assert.True(t, m.tipsEnabled)
		assert.Contains(t, strings.Join(out, " "), "on")
		assert.False(t, loadTipsOff())
	})

	t.Run("bare %tips shows one", func(t *testing.T) {
		m := Model{tipsEnabled: true}
		_, out := m.handleTipsCommand([]string{"%tips"})
		assert.Contains(t, strings.Join(out, " "), "Tip: ")
	})

	t.Run("asking for one does not spend the session's tip", func(t *testing.T) {
		m := Model{tipsEnabled: true}
		m, _ = m.handleTipsCommand([]string{"%tips"})
		assert.False(t, m.tipShown, "asking is not the same as being told")
	})

	t.Run("and works with tips off", func(t *testing.T) {
		m := Model{tipsEnabled: false}
		_, out := m.handleTipsCommand([]string{"%tips"})
		assert.Contains(t, strings.Join(out, " "), "Tip: ",
			"off stops the automatic one, not the command")
	})

	t.Run("rejects nonsense", func(t *testing.T) {
		m := Model{tipsEnabled: true}
		for _, args := range [][]string{
			{"%tips", "maybe"},
			{"%tips", "on", "off"},
		} {
			_, out := m.handleTipsCommand(args)
			assert.Contains(t, strings.Join(out, " "), "Usage:", args)
		}
	})
}

func TestHelpTipsDispatch(t *testing.T) {
	m := Model{keys: NewKeyMap()}

	t.Run("%help tips lists them and is not forwarded", func(t *testing.T) {
		out, handled, _ := m.handleLocalCommand("%help tips")
		assert.True(t, handled,
			"the proxy knows nothing about tips and would answer 'no help available'")
		assert.Contains(t, strings.Join(out, "\n"), specimen(t).name)
	})

	t.Run("%help tips <name> reads one", func(t *testing.T) {
		tp := specimen(t)
		out, handled, _ := m.handleLocalCommand("%HELP TIPS " + strings.ToUpper(tp.name))
		require.True(t, handled)
		assert.Contains(t, strings.Join(out, "\n"), tp.heading)
	})

	t.Run("the summary points at them", func(t *testing.T) {
		out, _, _ := m.handleLocalCommand("%help")
		assert.Contains(t, strings.Join(out, "\n"), "tips - ")
	})
}

// %help twice in a row printed the TUI topics in a different order each time,
// because the listing ranged a map.
func TestHelpSummaryIsStable(t *testing.T) {
	first := tuiHelpSummary()
	for range 5 {
		assert.Equal(t, first, tuiHelpSummary())
	}
}
