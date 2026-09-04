//go:build !js

package webstatic

import (
	"embed"
	"io/fs"
)

//go:embed term
var termFiles embed.FS

// TermFS returns the embedded filesystem for the browser TUI, rooted at the
// term directory.
func TermFS() (fs.FS, error) {
	return fs.Sub(termFiles, "term")
}
