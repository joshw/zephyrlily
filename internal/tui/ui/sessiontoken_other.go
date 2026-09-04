//go:build !js

package ui

// persistSessionToken does nothing outside the browser. A native client keeps
// its session only for as long as it runs, and what it remembers between runs
// is a credential (see internal/tui/creds), not a proxy token.
func persistSessionToken(string) {}

// forgetSessionToken is likewise a no-op.
func forgetSessionToken() {}
