//go:build js

package ui

import (
	"errors"
	"path"
	"strings"
	"syscall/js"
)

// writeSnapshot hands a %debug snapshot to the browser as a download, since
// there is no filesystem to write it to. The host page provides the sink:
//
//	globalThis.zlilySaveFile(filename, text)
//
// The snapshot holds the input line and recent keystrokes, so it goes to the
// user's own downloads and nowhere else — in particular it is never sent to
// the proxy.
func writeSnapshot(p, content string) error {
	save := js.Global().Get("zlilySaveFile")
	if save.Type() != js.TypeFunction {
		return errors.New("this page cannot save files")
	}
	save.Invoke(downloadName(p), content)
	return nil
}

// downloadName reduces a snapshot path to a bare filename. A browser download
// cannot honour a directory, and %debug snapshot ~/somewhere/x.txt should not
// silently produce a file called something else.
func downloadName(p string) string {
	name := path.Base(strings.ReplaceAll(p, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		return "zlily-debug.txt"
	}
	return name
}
