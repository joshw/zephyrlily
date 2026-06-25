// Package version reports the application version string.
//
// For tagged releases the value is injected at build time with:
//
//	go build -ldflags "-X github.com/joshw/zephyrlily/internal/version.Version=1.2.3" ./cmd/zlily
//
// When no release version is injected the version is derived automatically:
//
//   - go build / go install stamp the commit into the binary, so the version
//     reports as "dev+<shortsha>" (with a ".dirty" suffix when the working tree
//     had uncommitted changes at build time).
//   - go run does not stamp VCS info, so we fall back to asking git directly,
//     yielding the same "dev+<shortsha>[.dirty]" form when run inside the repo.
//   - If neither is available, the version reports as a bare "dev".
package version

import (
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
)

// Version holds an injected release version. Leave empty for dev builds; it is
// populated via -ldflags for releases. Prefer version.String() for display.
var Version = ""

var (
	once     sync.Once
	computed string
)

// String returns the human-readable version string. The result is computed once
// and cached.
func String() string {
	once.Do(func() { computed = compute() })
	return computed
}

func compute() string {
	if Version != "" {
		return Version
	}
	if rev, dirty, ok := buildVCS(); ok {
		return devString(rev, dirty)
	}
	if rev, dirty, ok := gitVCS(); ok {
		return devString(rev, dirty)
	}
	return "dev"
}

func devString(rev string, dirty bool) string {
	v := "dev+" + rev
	if dirty {
		v += ".dirty"
	}
	return v
}

// buildVCS reads the commit info Go stamps into binaries built with
// `go build`/`go install`. It is empty under `go run`.
func buildVCS() (rev string, dirty, ok bool) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false, false
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = shortSHA(s.Value)
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	return rev, dirty, rev != ""
}

// gitVCS asks git for the current commit, used as a fallback under `go run`
// where Go does not stamp VCS info. It returns ok=false when git is unavailable
// or the working directory is not a repository.
func gitVCS() (rev string, dirty, ok bool) {
	out, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", false, false
	}
	rev = shortSHA(strings.TrimSpace(string(out)))
	if rev == "" {
		return "", false, false
	}
	if st, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		dirty = strings.TrimSpace(string(st)) != ""
	}
	return rev, dirty, true
}

func shortSHA(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}
