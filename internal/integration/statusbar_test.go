package integration

import (
	"bytes"
	"testing"
	"time"

	"github.com/charmbracelet/x/exp/teatest/v2"
)

// statusBarSGR is the yellow-on-blue SGR the status bar renders with
// (statusBarStyle in internal/tui/ui/styles.go: bright yellow fg 93, blue bg
// 44). Nothing else in the UI uses this combination.
var statusBarSGR = []byte("[93;44")

// TestE2E_StatusBarVisibleImmediatelyAfterLogin guards the frame between
// login completing and the first server event arriving (on a live server,
// the attach-time /review playback): the status bar must already be drawn.
// Regression: after the bubbletea v2 migration the bar was reported missing
// until review output started.
func TestE2E_StatusBarVisibleImmediatelyAfterLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping teatest end-to-end test in -short mode")
	}
	c, _ := startStack(t)
	tm := startUI(t, c)

	// No events are pushed — this is the pre-review window. In the same
	// output stream that shows the post-login banner ("Connected to
	// TestServer", rendered after the state fetch), the status bar must be
	// painted too, well before the 5-second seen-report tick could force an
	// extra render. (Single WaitFor: teatest's Output reader is consumed as
	// WaitFor polls, so split waits would each see only part of the stream.)
	var acc []byte
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		acc = append(acc, b...)
		return bytes.Contains(acc, []byte("Connected to TestServer")) &&
			bytes.Contains(acc, statusBarSGR)
	}, teatest.WithDuration(4*time.Second), teatest.WithCheckInterval(25*time.Millisecond))
}
