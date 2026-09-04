//go:build !js

package ui

// credsStorable reports whether this build has somewhere to keep a password.
// Natively there are two candidates (see internal/tui/creds); either can fail
// at runtime, and the dialog reports that when it happens.
const credsStorable = true
