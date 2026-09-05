package version

import (
	"os"
	"strings"
	"testing"
)

// The browser client is built by a before-hook in .goreleaser.yaml rather than
// by the builds section, so the ldflags that stamp the native binaries do not
// reach it. Without its own -X the wasm falls back to the VCS stamp and reports
// "dev+<sha>" in its splash — in a tagged release as much as anywhere else,
// which is exactly how it shipped once.
func TestGoreleaserStampsTheBrowserBuild(t *testing.T) {
	b, err := os.ReadFile("../../.goreleaser.yaml")
	if err != nil {
		t.Skipf("no goreleaser config here: %v", err)
	}
	cfg := string(b)

	var hook string
	for _, line := range strings.Split(cfg, "\n") {
		if strings.Contains(line, "GOOS=js") && strings.Contains(line, "go build") {
			hook = line
			break
		}
	}
	if hook == "" {
		t.Fatal("no GOOS=js build hook found in .goreleaser.yaml")
	}

	const stamp = "internal/version.Version="
	if !strings.Contains(hook, stamp) {
		t.Errorf("the wasm build does not stamp a version, so the browser client\n"+
			"will report dev+<sha> even in a release:\n  %s", strings.TrimSpace(hook))
	}

	// And it must still not carry the shortener credential: a .wasm served to a
	// browser is a public download.
	if strings.Contains(hook, "u13APIKeyBuild") {
		t.Errorf("the wasm build injects the shortener key; it is publicly downloadable:\n  %s",
			strings.TrimSpace(hook))
	}
}
