//go:build !js

package webstatic

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFiles embed.FS

//go:embed term
var termFiles embed.FS

// FS returns the embedded filesystem rooted at the dist directory.
func FS() (fs.FS, error) {
	return fs.Sub(distFiles, "dist")
}

// TermFS returns the embedded filesystem for the browser TUI, rooted at the
// term directory.
func TermFS() (fs.FS, error) {
	return fs.Sub(termFiles, "term")
}
