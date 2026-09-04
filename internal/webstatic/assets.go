// Package webstatic embeds the browser build of the TUI, which the proxy
// serves at /term/.
//
// Build it with:
//
//	GOOS=js GOARCH=wasm go build -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm
//
// zlily.wasm is deliberately not committed (see .gitignore): it is a 20 MB
// build artifact.
//
// The Svelte web UI in web/ is no longer built or embedded. It was an
// experiment that the browser TUI overtook, and carrying it meant every build
// of this project needed Node. Its source is still in the tree; see
// docs/webui.md for what it would take to serve it again.
package webstatic
