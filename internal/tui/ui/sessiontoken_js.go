//go:build js

package ui

import "syscall/js"

// In the browser the proxy session outlives the page: a reload, a tab the
// browser discarded overnight, or a closed laptop all end the program while the
// session it was driving is still live on the proxy. Handing the token to the
// host lets the next load re-attach instead of asking for a password that the
// proxy would only use to hand back the very same session.
//
// The host decides where to keep it; see zlilySaveToken in
// internal/webstatic/term/term.js.

// persistSessionToken offers the token to the host page for safekeeping.
func persistSessionToken(token string) {
	if token == "" {
		return
	}
	if save := js.Global().Get("zlilySaveToken"); save.Type() == js.TypeFunction {
		save.Invoke(token)
	}
}

// forgetSessionToken tells the host to discard any stored token. Called when
// the proxy rejects one, so a dead token is not retried on every load.
func forgetSessionToken() {
	if save := js.Global().Get("zlilySaveToken"); save.Type() == js.TypeFunction {
		save.Invoke(js.Null())
	}
}
