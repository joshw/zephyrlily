// Package version holds the application version string.
// The value is injected at build time with:
//
//	go build -ldflags "-X github.com/joshw/zephyrlily/internal/version.Version=1.2.3" ./cmd/zlily
package version

// Version is the application version.  Defaults to "dev" when not injected.
var Version = "dev"
