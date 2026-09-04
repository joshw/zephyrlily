//go:build js

package ui

// credsStorable is false in the browser: there is no home directory for the
// credentials file and no OS keyring to reach, so nothing in internal/tui/creds
// can succeed. The dialog therefore omits its "Remember password" box rather
// than offering a promise it cannot keep.
//
// Nothing is lost by that. SLCP authenticates in-band on every connect, so a
// saved password would have to be recoverable plaintext, and the only places a
// page can put it — localStorage, IndexedDB — are readable by any script on the
// origin. What the box was for is covered instead by resuming the proxy session
// from its token (see sessiontoken_js.go), which is scoped to one session and
// dies with it.
const credsStorable = false
