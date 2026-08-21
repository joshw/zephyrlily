package commands

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/joshw/zephyrlily/internal/cmdarg"
)

func init() {
	RegisterHelp(HelpTopic{
		Name: "alias",
		Text: []string{
			"Define client aliases",
			"",
			"Usage:",
			"  %alias <alias> <commands>",
			"  %alias clear <alias>",
			"  %alias list [<alias>]",
			"",
			"<alias> must contain only A-Z, a-z, 0-9 and _. Alias names are not",
			"case sensitive: they are stored in lower case, so %greet, %Greet and",
			"%GREET all invoke the same alias.",
			"You cannot alias \"clear\" or \"list\".",
			"",
			"Supports the following special characters in <commands>:",
			"  $1 .. $9   arguments to the command",
			"  $0         the alias name",
			"  $*         all arguments to the command",
			"  \\n         command separator",
			"",
			"Examples:",
			"  %alias hi bob;hi there\\njim;I hate you!",
			"  %alias inbeener /who beener $*",
		},
	})
}

// aliasNameRe matches valid alias names: one or more word characters.
var aliasNameRe = regexp.MustCompile(`^\w+$`)

// aliasInvokeRe matches an alias invocation: %name optionally followed by args.
var aliasInvokeRe = regexp.MustCompile(`^%(\S+)\s*(.*)$`)

// AliasTable holds per-session command aliases. It is safe for concurrent use.
//
// Alias names are stored folded to lowercase so that invoking an alias is
// case-insensitive, matching built-in command names: %greet, %Greet and %GREET
// all resolve to the same alias. Expansion templates keep their case.
type AliasTable struct {
	mu      sync.Mutex
	aliases map[string]string // folded alias name -> expansion template
}

// NewAliasTable returns an empty alias table.
func NewAliasTable() *AliasTable {
	return &AliasTable{aliases: make(map[string]string)}
}

// HandleCommand implements the %alias command. args are the tokens following
// "%alias"; respond receives the output lines.
func (a *AliasTable) HandleCommand(args []string, respond func(lines []string)) {
	// No args, or "list" alone: list everything.
	if len(args) == 0 || (len(args) == 1 && cmdarg.Is(args[0], "list")) {
		respond(a.listAll())
		return
	}

	switch {
	case cmdarg.Is(args[0], "clear"):
		if len(args) < 2 {
			respond([]string{"(Usage: %alias clear <alias>)"})
			return
		}
		respond(a.clear(args[1:]))
		return

	case cmdarg.Is(args[0], "list"):
		respond(a.listSome(args[1:]))
		return
	}

	if !aliasNameRe.MatchString(args[0]) {
		respond([]string{"(First argument to %alias must be in set [A-Za-z0-9_])"})
		return
	}
	// Mixed-case names are accepted but stored folded, so what is echoed here is
	// what will actually resolve when the alias is invoked.
	name := cmdarg.Fold(args[0])

	// Just a name with no expansion: show that one alias.
	if len(args) == 1 {
		respond(a.listSome(args))
		return
	}

	expansion := strings.Join(args[1:], " ")
	a.mu.Lock()
	a.aliases[name] = expansion
	a.mu.Unlock()
	respond([]string{"(%" + name + " is now aliased to '" + expansion + "')"})
}

// Expand checks whether line invokes a defined alias and, if so, returns the
// expanded command line(s). It returns (nil, false) when line is not an alias
// invocation. A single template may expand to multiple lines via the "\n"
// separator.
func (a *AliasTable) Expand(line string) ([]string, bool) {
	m := aliasInvokeRe.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	name := cmdarg.Fold(m[1])
	// Never expand %alias itself, so alias management is always reachable.
	if name == "alias" {
		return nil, false
	}

	a.mu.Lock()
	template, ok := a.aliases[name]
	a.mu.Unlock()
	if !ok {
		return nil, false
	}

	rest := m[2]
	// args[0] is the alias name; args[1..] are the whitespace-split arguments.
	args := append([]string{name}, strings.Fields(rest)...)

	expanded := template
	for i := 0; i <= 9; i++ {
		val := ""
		if i < len(args) {
			val = args[i]
		}
		expanded = strings.ReplaceAll(expanded, "$"+strconv.Itoa(i), val)
	}
	expanded = strings.ReplaceAll(expanded, "$*", rest)

	return strings.Split(expanded, `\n`), true
}

// listAll returns the listing for every defined alias.
func (a *AliasTable) listAll() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.aliases) == 0 {
		return []string{"(no aliases are currently defined)"}
	}
	names := make([]string, 0, len(a.aliases))
	for name := range a.aliases {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{"The following aliases are defined:"}
	for _, name := range names {
		lines = append(lines, name+": "+a.aliases[name])
	}
	return lines
}

// listSome returns listings for the named aliases.
func (a *AliasTable) listSome(names []string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var lines []string
	for _, raw := range names {
		name := cmdarg.Fold(raw)
		if expansion, ok := a.aliases[name]; ok {
			lines = append(lines, name+": "+expansion)
		} else {
			lines = append(lines, "("+name+" is not aliased)")
		}
	}
	return lines
}

// clear removes the named aliases and returns confirmation lines.
func (a *AliasTable) clear(names []string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var lines []string
	for _, raw := range names {
		name := cmdarg.Fold(raw)
		delete(a.aliases, name)
		lines = append(lines, "(%"+name+" is now unaliased.)")
	}
	return lines
}
