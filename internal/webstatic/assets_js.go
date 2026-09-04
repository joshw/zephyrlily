//go:build js

package webstatic

import (
	"io/fs"
	"testing/fstest"
)

// The browser build embeds nothing.
//
// It reaches this package only incidentally — internal/tui/client imports
// internal/proxy/api for its message types, and that package serves web assets
// — and it has no use for them: a client running *in* a browser never serves a
// browser client to anyone.
//
// Embedding them anyway is not merely wasteful, it compounds. term/ holds
// zlily.wasm, so every wasm build would embed the previous one: 20 MB, then
// 39 MB, then 60 MB, growing with each rebuild until a release binary is
// hundreds of megabytes.

// FS returns an empty filesystem.
func FS() (fs.FS, error) { return fstest.MapFS{}, nil }

// TermFS returns an empty filesystem.
func TermFS() (fs.FS, error) { return fstest.MapFS{}, nil }
