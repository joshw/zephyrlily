// Package webstatic embeds the browser-facing assets the proxy serves: the
// compiled Svelte web application, and the browser build of the TUI.
//
// Build the Svelte app first with: cd web && npm install && npm run build
// Vite outputs to internal/webstatic/dist/ (configured in web/vite.config.js).
//
// The browser TUI lives in term/ rather than in dist/, because Vite owns dist/
// and clears it on every build (emptyOutDir in web/vite.config.js) —
// zlily.wasm would not survive. Build it with:
//
//	GOOS=js GOARCH=wasm go build -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm
//
// zlily.wasm is deliberately not committed (see .gitignore): it is a 20 MB
// build artifact.
package webstatic
