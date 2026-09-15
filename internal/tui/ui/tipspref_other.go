//go:build !js

package ui

import "github.com/joshw/zephyrlily/internal/tui/creds"

// Natively the %tips setting lives in the per-user settings file alongside the
// remembered usernames; see internal/tui/creds/config.go.

func loadTipsOff() bool { return creds.TipsOff() }

func saveTipsOff(off bool) error { return creds.SetTipsOff(off) }
