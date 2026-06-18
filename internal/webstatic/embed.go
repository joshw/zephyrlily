// Package webstatic embeds the compiled Svelte web application.
// Build it first with: cd web && npm install && npm run build
// Vite outputs to internal/webstatic/dist/ (configured in web/vite.config.js).
package webstatic

import (
	"embed"
	"io/fs"
)

//go:embed dist
var files embed.FS

// FS returns the embedded filesystem rooted at the dist directory.
func FS() (fs.FS, error) {
	return fs.Sub(files, "dist")
}
