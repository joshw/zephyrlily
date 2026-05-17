package commands

import (
	"fmt"
	"sort"

	"github.com/joshw/zephyrlily/internal/lily"
)

func init() {
	Registry["%debug"] = handleDebug
	RegisterHelp(HelpTopic{
		Name: "debug",
		Text: []string{
			"Inspect proxy-side state",
			"",
			"Usage: %debug [subcommand]",
			"",
			"Subcommands:",
			"  discs   - list all known discussions and your membership status (default)",
			"  users   - list all known users",
			"  groups  - list all known groups",
			"  all     - show everything",
			"",
			"Examples:",
			"  %debug",
			"  %debug discs",
			"  %debug users",
		},
	})
}

func handleDebug(state *lily.State, args []string, respond func(lines []string)) {
	sub := "discs"
	if len(args) > 0 {
		sub = args[0]
	}

	entities := state.AllEntities()

	// Partition by kind and sort each group by name.
	var discs, users, groups []*lily.Entity
	for _, e := range entities {
		switch e.Kind {
		case lily.KindDisc:
			discs = append(discs, e)
		case lily.KindUser:
			users = append(users, e)
		case lily.KindGroup:
			groups = append(groups, e)
		}
	}
	sortByName := func(es []*lily.Entity) {
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })
	}
	sortByName(discs)
	sortByName(users)
	sortByName(groups)

	var out []string

	showDiscs := sub == "discs" || sub == "all"
	showUsers := sub == "users" || sub == "all"
	showGroups := sub == "groups" || sub == "all"

	if !showDiscs && !showUsers && !showGroups {
		respond([]string{
			"Unknown subcommand: " + sub,
			"Try: %debug discs | users | groups | all",
		})
		return
	}

	out = append(out, fmt.Sprintf("Whoami: %s", state.Whoami))
	out = append(out, "")

	if showDiscs {
		memberCount := 0
		for _, e := range discs {
			if state.IsDiscMember(e.Handle) {
				memberCount++
			}
		}
		out = append(out, fmt.Sprintf("Discussions (%d total, member of %d):", len(discs), memberCount))
		if len(discs) == 0 {
			out = append(out, "  (none)")
		}
		for _, e := range discs {
			status := "non-member"
			if state.IsDiscMember(e.Handle) {
				status = "member"
			}
			out = append(out, fmt.Sprintf("  %-20s  %-12s  %s", e.Name, e.Handle, status))
		}
		out = append(out, "")
	}

	if showUsers {
		out = append(out, fmt.Sprintf("Users (%d):", len(users)))
		if len(users) == 0 {
			out = append(out, "  (none)")
		}
		for _, e := range users {
			line := fmt.Sprintf("  %-20s  %-12s  %s", e.Name, e.Handle, e.State)
			if e.Blurb != "" {
				line += "  [" + e.Blurb + "]"
			}
			out = append(out, line)
		}
		out = append(out, "")
	}

	if showGroups {
		out = append(out, fmt.Sprintf("Groups (%d):", len(groups)))
		if len(groups) == 0 {
			out = append(out, "  (none)")
		}
		for _, e := range groups {
			out = append(out, fmt.Sprintf("  %-20s  %d members", e.Name, len(e.Members)))
		}
		out = append(out, "")
	}

	respond(out)
}
