package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestE2E_CommandNamesIgnoreCase drives mixed-case %commands through the real
// proxy dispatcher. It guards the trap in Session.dispatchLine: the command name
// is folded to pick the handler, but %on's raw remainder must still be trimmed
// with the command as the user spelled it, or "%ON public …" would leave "ON" in
// the spec.
func TestE2E_CommandNamesIgnoreCase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping end-to-end test in -short mode")
	}
	c, _ := startStack(t)

	// nextResult returns the lines of the next commandresult event.
	nextResult := func(t *testing.T) []string {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case msg, ok := <-c.Events:
				require.True(t, ok, "events channel closed")
				if msg.Type != "commandresult" {
					continue
				}
				d, _ := msg.Data.(map[string]interface{})
				raw, _ := d["lines"].([]interface{})
				lines := make([]string, 0, len(raw))
				for _, l := range raw {
					lines = append(lines, l.(string))
				}
				if len(lines) == 0 {
					continue
				}
				return lines
			case <-deadline:
				t.Fatal("timed out waiting for a command result")
				return nil
			}
		}
	}

	// A registered proxy command resolves regardless of case.
	require.NoError(t, c.Send("%VERSION"))
	require.NotEmpty(t, nextResult(t))

	// %ON reaches the %on handler, and the remainder after the command name is
	// parsed as a spec — not as "ON public like …".
	require.NoError(t, c.Send(`%ON public LIKE "PingToken (.*)" "$sender;pong $1"`))
	confirm := strings.Join(nextResult(t), "\n")
	require.Contains(t, confirm, "on public events", "%ON did not parse as an event spec")
	require.Contains(t, confirm, `like "PingToken (.*)"`,
		"LIKE was not recognised as a filter keyword, or the regexp lost its case")

	// The handler is really registered and listable through the folded name.
	require.NoError(t, c.Send("%On LIST"))
	listed := strings.Join(nextResult(t), "\n")
	require.Contains(t, listed, `LIKE "PingToken (.*)"`)
}
