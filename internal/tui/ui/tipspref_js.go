//go:build js

package ui

import (
	"errors"

	"syscall/js"
)

// In the browser there is no settings file to keep the %tips setting in, so the
// host page keeps it - the same arrangement as the session token, and for the
// same reason (see sessiontoken_js.go). Unlike the token it is not even
// slightly sensitive, so localStorage is simply where it belongs.
//
// This is load-bearing for more than tidiness. The other way to persist a
// client setting is a zlilyStartup memo, and that cannot work here: the memo is
// replayed by the proxy only on a fresh login, while a browser reload resumes
// the existing session instead, so a '%tips off' left in a memo would be
// silently skipped on every reload and tips would come back forever.
//
// The host functions are zlilySavePref and zlilyLoadPref in
// internal/webstatic/term/term.js.

const tipsOffPrefKey = "tips_off"

func loadTipsOff() bool {
	load := js.Global().Get("zlilyLoadPref")
	if load.Type() != js.TypeFunction {
		return false
	}
	v := load.Invoke(tipsOffPrefKey)
	// An older page without the functions, or a browser refusing storage,
	// answers with null or undefined: no setting, so tips stay on.
	if v.Type() != js.TypeString {
		return false
	}
	return v.String() == "1"
}

func saveTipsOff(off bool) error {
	save := js.Global().Get("zlilySavePref")
	if save.Type() != js.TypeFunction {
		return errors.New("this page cannot remember settings")
	}
	if off {
		save.Invoke(tipsOffPrefKey, "1")
	} else {
		save.Invoke(tipsOffPrefKey, js.Null())
	}
	return nil
}
