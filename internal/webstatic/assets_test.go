package webstatic

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The browser build must not embed the browser build.
//
// cmd/zlily-wasm reaches this package by accident: internal/tui/client imports
// internal/proxy/api for its message types, and that package serves web
// assets. If the embeds are not gated off js/wasm, every build of the browser
// client embeds the previous one — 20 MB, then 39 MB, then 60 MB, compounding
// silently with each rebuild until a release binary is hundreds of megabytes.
// Nothing else notices: the build succeeds, the tests pass, and the page still
// works.
func TestBrowserBuildEmbedsNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the compiler")
	}
	// Compile the browser client's dependency list for js/wasm and confirm the
	// embed-bearing files are not part of it.
	cmd := exec.Command("go", "list", "-f", "{{range .EmbedFiles}}{{.}}\n{{end}}", "./")
	cmd.Env = append(cmd.Environ(), "GOOS=js", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("the js build of this package embeds files, so the browser client embeds itself:\n%s", got)
	}
}

// The native build must still embed them, or the proxy serves nothing.
func TestNativeBuildEmbedsAssets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the compiler")
	}
	cmd := exec.Command("go", "list", "-f", "{{range .EmbedFiles}}{{.}}\n{{end}}", "./")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	files := string(out)
	for _, want := range []string{"term/index.html", "term/term.js", "dist/index.html"} {
		if !strings.Contains(files, want) {
			t.Errorf("the native build does not embed %s:\n%s", want, files)
		}
	}
}

// The update banner must not be toggled with the `hidden` attribute.
//
// `hidden` works through the user-agent rule [hidden]{display:none}, which any
// id selector setting `display` outranks. A banner styled `#update{display:flex}`
// is therefore visible from first paint whatever `hidden` says, and
// `el.hidden = true` silently does nothing — so it appeared on every load,
// before the update check had even run, and neither button seemed to work.
//
// It is toggled with a class instead, which does not depend on losing a
// specificity contest. This checks the shape of that, because the failure is
// invisible in the markup and only shows in a browser.
func TestUpdateBannerIsToggledByClassNotTheHiddenAttribute(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("term", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	// Strip CSS and HTML comments before matching: the stylesheet explains this
	// very trap by quoting the broken rule, and a naive search finds that.
	html := regexp.MustCompile(`(?s)/\*.*?\*/|<!--.*?-->`).ReplaceAllString(string(b), "")

	if regexp.MustCompile(`id="update"[^>]*\bhidden\b`).MatchString(html) {
		t.Error(`the banner carries the hidden attribute; an id rule setting display will override it`)
	}
	if !strings.Contains(html, "#update.show") {
		t.Error("no #update.show rule: the banner has no way to be shown")
	}
	// The default must be hidden, or it shows before anything decides it should.
	rule := regexp.MustCompile(`#update\s*\{[^}]*\}`).FindString(html)
	if rule == "" {
		t.Fatal("no #update rule found")
	}
	if !strings.Contains(rule, "display: none") {
		t.Errorf("the banner is not hidden by default:\n%s", rule)
	}
}
