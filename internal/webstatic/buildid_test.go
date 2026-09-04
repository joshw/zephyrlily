package webstatic

import (
	"testing"
	"testing/fstest"
)

// The build ID is what makes the browser client's assets cacheable, so the
// property that matters is not that it is stable but that it *moves* whenever
// the served bytes do. If it ever stopped changing, every browser would keep
// serving a stale client from cache and no amount of reloading would help.

func TestBuildIDChangesWithContent(t *testing.T) {
	base := fstest.MapFS{
		"index.html": {Data: []byte("<html>")},
		"term.js":    {Data: []byte("console.log(1)")},
	}
	edited := fstest.MapFS{
		"index.html": {Data: []byte("<html>")},
		"term.js":    {Data: []byte("console.log(2)")}, // one byte different
	}

	if buildIDOf(base) == buildIDOf(edited) {
		t.Fatal("editing an asset did not change the build ID; caches would never break")
	}
}

func TestBuildIDChangesWithFilenames(t *testing.T) {
	a := fstest.MapFS{"a.js": {Data: []byte("x")}}
	b := fstest.MapFS{"b.js": {Data: []byte("x")}}
	if buildIDOf(a) == buildIDOf(b) {
		t.Error("renaming an asset did not change the build ID")
	}

	// Path and content must not run together: without a delimiter, a file
	// "ab" holding "c" would digest the same as "a" holding "bc".
	x := fstest.MapFS{"ab": {Data: []byte("c")}}
	y := fstest.MapFS{"a": {Data: []byte("bc")}}
	if buildIDOf(x) == buildIDOf(y) {
		t.Error("path and content are concatenated ambiguously")
	}
}

func TestBuildIDIsDeterministic(t *testing.T) {
	f := fstest.MapFS{
		"index.html":   {Data: []byte("<html>")},
		"vendor/x.js":  {Data: []byte("x")},
		"vendor/y.js":  {Data: []byte("y")},
		"zlily.wasm":   {Data: []byte("\x00asm")},
		"term.js":      {Data: []byte("t")},
		"vendor/sub/z": {Data: []byte("z")},
	}
	first := buildIDOf(f)
	for i := 0; i < 5; i++ {
		if got := buildIDOf(f); got != first {
			t.Fatalf("run %d gave %q, want %q — the walk order is not stable", i, got, first)
		}
	}
}

func TestBuildIDIsURLSafe(t *testing.T) {
	id := buildIDOf(fstest.MapFS{"a": {Data: []byte("x")}})
	if len(id) != 12 {
		t.Errorf("build ID = %q, want 12 characters", id)
	}
	for _, r := range id {
		hex := ('0' <= r && r <= '9') || ('a' <= r && r <= 'f')
		if !hex {
			t.Errorf("build ID %q contains %q, which is not hex", id, r)
		}
	}
}
