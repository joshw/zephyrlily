package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"

	"github.com/joshw/zephyrlily/internal/deploy"
)

// cmdDeploy generates a containerized deployment behind Traefik and starts it.
func cmdDeploy(args []string) {
	fs := pflag.NewFlagSet("deploy", pflag.ExitOnError)
	domain := fs.String("domain", "", "hostname to serve on, e.g. lily.example.org (required)")
	email := fs.String("email", "", "Let's Encrypt account email (required)")
	lily := fs.String("lily", "rpi.lily.org:7777", "the one Lily server this deployment talks to")
	dir := fs.String("dir", "zlily-deploy", "directory to generate the deployment in")
	image := fs.String("image", "zlily:local", "tag for the built image")
	binary := fs.String("binary", "", "Linux zlily binary to ship (default: the running one)")
	staging := fs.Bool("staging", false, "use the Let's Encrypt staging CA (untrusted, but generous rate limits)")
	noStart := fs.Bool("no-start", false, "generate the files but do not run docker compose")
	maxSessions := fs.Int("max-sessions", 0, "cap on concurrent sessions (0 = zlily's default)")
	authMaxFailures := fs.Int("auth-max-failures", 0, "failed logins per client before a lockout (0 = default)")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: zlily deploy --domain HOST --email ADDR [flags]

Generates a Docker deployment serving the browser TUI over HTTPS, with Traefik
terminating TLS using a Let's Encrypt certificate, and starts it.

The image ships the running zlily binary, so run this on the Linux host you are
deploying to. From anywhere else, cross-compile and pass --binary.

DNS for the domain must already point at this host, and ports 80 and 443 must
reach it: the certificate is issued over an HTTP challenge on port 80.

Example:
  zlily deploy --domain lily.example.org --email you@example.org

Flags:
`)
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	err := deploy.Run(deploy.Options{
		Domain:          *domain,
		Email:           *email,
		LilyAddr:        *lily,
		Dir:             *dir,
		Image:           *image,
		BinaryPath:      *binary,
		Staging:         *staging,
		Start:           !*noStart,
		MaxSessions:     *maxSessions,
		AuthMaxFailures: *authMaxFailures,
		Out:             os.Stdout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "deploy:", err)
		os.Exit(1)
	}
}
