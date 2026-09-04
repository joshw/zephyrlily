package deploy

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubBinary stands in for a Linux zlily; --binary skips the platform check,
// so its contents do not matter.
func stubBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "zlily")
	if err := os.WriteFile(p, []byte("not really an elf"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func generate(t *testing.T, mutate func(*Options)) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "deploy")
	opts := Options{
		Domain:     "lily.example.org",
		Email:      "you@example.org",
		LilyAddr:   "rpi.lily.org:7777",
		Dir:        dir,
		BinaryPath: stubBinary(t),
		Out:        io.Discard,
	}
	if mutate != nil {
		mutate(&opts)
	}
	if err := Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return dir
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestGeneratesDeployment(t *testing.T) {
	dir := generate(t, nil)

	for _, f := range []string{"Dockerfile", "docker-compose.yml", "README.md", "zlily", "letsencrypt/acme.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}

	compose := read(t, dir, "docker-compose.yml")
	// The Host rule needs Traefik's backticks; losing them to Go's template
	// escaping would produce a router that never matches.
	if !strings.Contains(compose, "Host(`lily.example.org`)") {
		t.Errorf("Host rule missing or unquoted:\n%s", compose)
	}
	if !strings.Contains(compose, "--lily=rpi.lily.org:7777") {
		t.Error("the Lily server is not pinned in the compose command")
	}
	// Rate limiting is keyed on the client address, which is only correct
	// behind the proxy if this is set.
	if !strings.Contains(compose, "--behind-proxy") {
		t.Error("--behind-proxy missing; rate limiting would key on Traefik's address")
	}
	if !strings.Contains(compose, "docker.sock:ro") {
		t.Error("the Docker socket should be mounted read-only")
	}
}

func TestACMEFileIsPrivateAndPreserved(t *testing.T) {
	dir := generate(t, nil)
	acme := filepath.Join(dir, "letsencrypt", "acme.json")

	fi, err := os.Stat(acme)
	if err != nil {
		t.Fatal(err)
	}
	// Traefik refuses to use a world-readable acme.json.
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("acme.json mode = %o, want 600", perm)
	}

	// Re-running a deployment must not discard issued certificates: doing so
	// would re-request them and run into Let's Encrypt's rate limit.
	if err := os.WriteFile(acme, []byte(`{"certificates":"pretend"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(Options{
		Domain: "lily.example.org", Email: "you@example.org",
		LilyAddr: "rpi.lily.org:7777", Dir: dir,
		BinaryPath: stubBinary(t), Out: io.Discard,
	}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "letsencrypt/acme.json"); !strings.Contains(got, "pretend") {
		t.Error("re-running the deployment destroyed the existing certificates")
	}
}

func TestStagingSelectsTheStagingCA(t *testing.T) {
	off := read(t, generate(t, nil), "docker-compose.yml")
	if strings.Contains(off, "acme-staging") {
		t.Error("staging CA used without --staging")
	}
	on := read(t, generate(t, func(o *Options) { o.Staging = true }), "docker-compose.yml")
	if !strings.Contains(on, "acme-staging-v02.api.letsencrypt.org") {
		t.Error("--staging did not select the staging CA")
	}
}

func TestOptionalLimitsAreOmittedWhenUnset(t *testing.T) {
	off := read(t, generate(t, nil), "docker-compose.yml")
	if strings.Contains(off, "--max-sessions") || strings.Contains(off, "--auth-max-failures") {
		t.Error("unset limits should be left out so zlily's own defaults apply")
	}
	on := read(t, generate(t, func(o *Options) { o.MaxSessions = 32; o.AuthMaxFailures = 3 }), "docker-compose.yml")
	if !strings.Contains(on, "--max-sessions=32") || !strings.Contains(on, "--auth-max-failures=3") {
		t.Errorf("configured limits not passed through:\n%s", on)
	}
}

func TestValidationRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"no domain", Options{Email: "a@b.c", LilyAddr: "h:1"}, "--domain"},
		{"no email", Options{Domain: "d.example", LilyAddr: "h:1"}, "--email"},
		{"no lily", Options{Domain: "d.example", Email: "a@b.c"}, "--lily"},
		// A URL where a hostname belongs produces a router rule that silently
		// never matches, which is a miserable thing to debug through Traefik.
		{"domain is a URL", Options{Domain: "https://d.example", Email: "a@b.c", LilyAddr: "h:1"}, "bare hostname"},
		{"domain has a port", Options{Domain: "d.example:443", Email: "a@b.c", LilyAddr: "h:1"}, "bare hostname"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.Out = io.Discard
			err := Run(tc.opts)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}
