//go:build !js

package ui

import "os"

// writeSnapshot saves a %debug snapshot to disk. Mode 0600: the snapshot
// quotes the input line and recent keystrokes, so it can contain anything the
// user was in the middle of typing.
func writeSnapshot(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}
