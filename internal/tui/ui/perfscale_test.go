package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/stretchr/testify/assert"
)

// Scaling benchmarks behind the "sluggish after a long session" reports.
// The load harness in internal/integration ages a real session end to end;
// these isolate what per-message cost scales with, without network, proxy or
// scheduler noise in the way. They are what identified the slowdown and what
// now shows it staying fixed:
//
//	go test ./internal/tui/ui -run xxx -bench Scrollback -benchtime 200x
//
// Both benchmarks hold everything constant except how much scrollback the
// session has already accumulated.

// benchModel builds a sized model preloaded with n scrollback items.
//
// The items are appended directly and the viewport synced once, rather than
// delivered as n messages: feeding them through Update would cost a full
// content rebuild per item, which is the very thing being measured and would
// make setup for the largest size quadratic (minutes, not milliseconds).
func benchModel(tb testing.TB, n int) Model {
	tb.Helper()
	logChan, _ := NewLogger()
	m := New(client.New(""), logChan)
	m.authMode = false
	upd, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = upd.(Model)
	for i := 0; i < n; i++ {
		m.output = append(m.output, OutputItem{
			Type: "text",
			ID:   int64(i + 1),
			Data: fmt.Sprintf("preload %d lorem ipsum dolor sit amet consectetur", i),
		})
	}
	return m.syncViewportContent()
}

// BenchmarkIncomingMessageByScrollback measures handling one incoming message
// against the size of the scrollback it lands in.
func BenchmarkIncomingMessageByScrollback(b *testing.B) {
	for _, items := range []int{100, 1000, 5000, maxScrollback} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			m := benchModel(b, items)
			id := int64(items)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id++
				msg := api.WSServerMsg{
					ID:   id,
					Type: "text",
					Data: map[string]interface{}{"text": "incoming lorem ipsum dolor sit amet"},
				}
				upd, _ := m.Update(serverEventMsg{msg: &msg})
				m = upd.(Model)
			}
		})
	}
}

// BenchmarkKeystrokeByScrollback measures one keystroke against the same
// scrollback sizes. Typing does not rebuild the viewport's content, so this is
// the control: if it stays flat while the benchmark above climbs, the loop —
// not the keystroke — is what a sluggish session is waiting on.
func BenchmarkKeystrokeByScrollback(b *testing.B) {
	for _, items := range []int{100, 1000, 5000, maxScrollback} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			m := benchModel(b, items)
			keys := []tea.KeyPressMsg{
				{Code: 'a', Text: "a"},
				{Code: tea.KeyBackspace},
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				upd, _ := m.Update(keys[i%len(keys)])
				m = upd.(Model)
			}
		})
	}
}

// perMessageCost returns the average time to handle one incoming message in a
// model already holding items of scrollback.
func perMessageCost(t *testing.T, items, samples int) time.Duration {
	t.Helper()
	m := benchModel(t, items)
	id := int64(items)
	start := time.Now()
	for i := 0; i < samples; i++ {
		id++
		msg := api.WSServerMsg{
			ID:   id,
			Type: "text",
			Data: map[string]interface{}{"text": "incoming lorem ipsum dolor sit amet"},
		}
		upd, _ := m.Update(serverEventMsg{msg: &msg})
		m = upd.(Model)
	}
	return time.Since(start) / time.Duration(samples)
}

// TestIncomingMessageCostDoesNotScaleWithScrollback is the regression guard on
// the fix. Handling a message used to rebuild and re-ingest the entire
// scrollback, so its cost grew with the session: ~1ms at 100 items and ~35ms
// at the cap, which is what made a long-lived session sluggish. It is now
// bounded by the trim of one item, not the size of what is retained.
//
// The assertion is a ratio rather than an absolute figure so it means the same
// thing on a slow CI box as on a fast laptop: what must not come back is the
// dependence on scrollback size.
func TestIncomingMessageCostDoesNotScaleWithScrollback(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive scaling check in -short mode")
	}

	const samples = 200
	small := perMessageCost(t, 500, samples)
	full := perMessageCost(t, maxScrollback, samples)

	ratio := float64(full) / float64(small)
	t.Logf("per message: %s at 500 items, %s at %d items (%.2fx)",
		small.Round(time.Microsecond), full.Round(time.Microsecond), maxScrollback, ratio)

	// The old behaviour was ~35x across this range; a healthy one is ~1.5x,
	// the difference being the per-message trim once the cap is reached.
	assert.Lessf(t, ratio, 5.0,
		"handling a message at the scrollback cap costs %.2fx what it costs at 500 items - "+
			"per-message work has started scaling with the scrollback again", ratio)
}
