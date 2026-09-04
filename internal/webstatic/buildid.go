package webstatic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sync"
)

// TermBuildID identifies exactly this build of the browser client: a hash over
// every file served under /term/, including zlily.wasm.
//
// It is what makes the assets cacheable. Without a validator of any kind — and
// embed.FS reports a zero modification time, so net/http emits no
// Last-Modified — a browser is free to reuse a cached copy indefinitely, which
// is why a new proxy binary used to need a forced reload to take effect.
// Stamping the ID into every asset URL means a new build is a new URL, so the
// old one is never reused and the new one is never missed.
//
// Hashing the content rather than using version.String() keeps this honest for
// development builds, where the version string does not change between two
// `go build`s of different code.
var TermBuildID = sync.OnceValue(func() string {
	fsys, err := TermFS()
	if err != nil {
		return unknownBuildID
	}
	return buildIDOf(fsys)
})

// unknownBuildID is what callers get when the assets cannot be read. It is a
// valid URL component, so a broken build degrades to "uncacheable" rather than
// to a malformed page.
const unknownBuildID = "unknown"

// buildIDOf digests every file in fsys: both the paths and their contents, so
// that renaming a file changes the result as surely as editing one does.
func buildIDOf(fsys fs.FS) string {
	h := sha256.New()
	// fs.WalkDir visits in lexical order, so the digest is stable across runs.
	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		// The path is hashed with a length prefix so that concatenation cannot
		// collide: "ab" + "c" must not digest the same as "a" + "bc".
		// hash.Hash is documented never to return an error from Write.
		_, _ = fmt.Fprintf(h, "%d:%s", len(path), path)
		_, _ = h.Write(b)
		return nil
	})
	if err != nil {
		return unknownBuildID
	}
	// 12 hex characters: plenty to tell builds apart, short enough to read in
	// a URL or a log line.
	return hex.EncodeToString(h.Sum(nil))[:12]
}
