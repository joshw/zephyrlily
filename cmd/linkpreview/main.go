// linkpreview resolves URLs to the one-line previews that
// internal/linkpreview builds from page metadata.
//
// It exists to answer the open question about that package: whether
// metadata-only previews are good enough to be worth putting in front of a
// message. Point it at a spread of real links and read the output.
//
// Usage:
//
//	linkpreview [flags] <url>...
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"

	"github.com/joshw/zephyrlily/internal/linkpreview"
)

func main() {
	var (
		maxLen  = pflag.IntP("length", "n", 160, "maximum summary length, in characters")
		verbose = pflag.BoolP("verbose", "v", false, "show every resolved field, not just the summary")
		timeout = pflag.DurationP("timeout", "t", 10*time.Second, "per-URL timeout")
		agent   = pflag.String("user-agent", "", "override the User-Agent header")
	)
	pflag.Parse()

	if pflag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: linkpreview [flags] <url>...")
		pflag.PrintDefaults()
		os.Exit(2)
	}
	if *agent != "" {
		linkpreview.UserAgent = *agent
	}

	var failed bool
	for _, raw := range pflag.Args() {
		if err := show(raw, *maxLen, *verbose, *timeout); err != nil {
			fmt.Printf("%s\n  error: %v\n\n", raw, err)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func show(raw string, maxLen int, verbose bool, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	p, err := linkpreview.Fetch(ctx, raw)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}

	summary := p.Summary(maxLen)
	fmt.Println(raw)
	if summary == "" {
		fmt.Println("  (no metadata)")
	} else {
		fmt.Printf("  %s\n", summary)
	}
	fmt.Printf("  [from %s, %s]\n", fieldLabel(p.Field), elapsed.Round(time.Millisecond))

	if verbose {
		if p.URL != raw {
			fmt.Printf("  final:     %s\n", p.URL)
		}
		printField("title", p.Title)
		printField("desc", p.Desc)
		printField("site", p.SiteName)
		printField("type", p.ContentType)
	}
	fmt.Println()
	return nil
}

func fieldLabel(f linkpreview.Field) string {
	if f == linkpreview.FieldNone {
		return "nothing"
	}
	return string(f)
}

func printField(label, val string) {
	if val != "" {
		fmt.Printf("  %-10s %s\n", label+":", val)
	}
}
