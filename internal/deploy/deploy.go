// Package deploy generates and starts a containerized zlily behind Traefik,
// which terminates TLS with a Let's Encrypt certificate.
//
// The shape follows the compose file a Traefik user would write by hand: the
// Docker provider, one router labelled on the zlily service, and an ACME
// resolver doing an HTTP challenge on the web entrypoint.
package deploy

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"

	"github.com/joshw/zephyrlily/internal/webstatic"
)

// Options configures a deployment.
type Options struct {
	Domain   string // hostname Traefik gets a certificate for
	Email    string // Let's Encrypt account address
	LilyAddr string // the one Lily server this deployment will ever talk to
	Dir      string // where to write the deployment
	Image    string // built image tag

	BinaryPath string // an explicit linux binary to ship; empty means "this one"
	Staging    bool   // use the Let's Encrypt staging CA
	Start      bool   // run docker compose up after generating

	MaxSessions     int
	AuthMaxFailures int

	// Out is where progress is reported. Writes to it are deliberately
	// unchecked throughout: failing to print a line must not fail a
	// deployment that otherwise succeeded.
	Out io.Writer
}

// Run generates the deployment and, unless told otherwise, starts it.
func Run(opts Options) error {
	if opts.Out == nil {
		opts.Out = io.Discard
	}
	if err := validate(&opts); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(opts.Dir, "letsencrypt"), 0o755); err != nil {
		return fmt.Errorf("create deployment directory: %w", err)
	}

	binPath, err := stageBinary(opts)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(opts.Out, "  binary   %s\n", binPath)

	// A binary linked against glibc cannot run on a distroless static base,
	// and the failure looks like the entrypoint not existing at all. Read the
	// ELF rather than guessing: locally built binaries usually have cgo
	// enabled, while release builds do not.
	base := baseImage(binPath)

	files := map[string]string{
		"Dockerfile":         dockerfileTmpl,
		"docker-compose.yml": composeTmpl,
		"README.md":          readmeTmpl,
	}
	data := newTemplateData(opts, base)
	for name, tmpl := range files {
		if err := writeTemplate(filepath.Join(opts.Dir, name), tmpl, data); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(opts.Out, "  wrote    %s\n", filepath.Join(opts.Dir, name))
	}

	// Traefik refuses to use an acme.json that others can read.
	acme := filepath.Join(opts.Dir, "letsencrypt", "acme.json")
	if err := ensureACMEFile(acme); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(opts.Out, "  wrote    %s (mode 0600)\n", acme)

	if !opts.Start {
		_, _ = fmt.Fprintf(opts.Out, "\nReview the files, then:\n  cd %s && docker compose up -d --build\n", opts.Dir)
		return nil
	}
	return composeUp(opts)
}

func validate(opts *Options) error {
	if opts.Domain == "" {
		return errors.New("--domain is required: Traefik needs a hostname to get a certificate for")
	}
	if strings.Contains(opts.Domain, "/") || strings.Contains(opts.Domain, ":") {
		return fmt.Errorf("--domain should be a bare hostname like chat.example.com, got %q", opts.Domain)
	}
	if opts.Email == "" {
		return errors.New("--email is required: Let's Encrypt needs an account address")
	}
	if opts.LilyAddr == "" {
		return errors.New("--lily is required: the deployment talks to exactly one Lily server")
	}
	if opts.Dir == "" {
		opts.Dir = "zlily-deploy"
	}
	if opts.Image == "" {
		opts.Image = "zlily:local"
	}
	return nil
}

// stageBinary puts a Linux zlily binary in the build context and returns its
// path.
//
// Copying the running executable is the good case: it needs no toolchain, no
// source and no download, and it deploys exactly the binary that was tested.
// It only works when this process is itself a Linux binary of the right
// architecture, which is true when deploy is run on the host it deploys to.
func stageBinary(opts Options) (string, error) {
	dst := filepath.Join(opts.Dir, "zlily")

	src := opts.BinaryPath
	if src == "" {
		if runtime.GOOS != "linux" {
			return "", fmt.Errorf(
				"cannot ship the running binary: this is a %s/%s build and the container runs linux.\n"+
					"Either run `zlily deploy` on the Linux host you are deploying to, or cross-compile and pass it:\n"+
					"    GOOS=js GOARCH=wasm go build -ldflags=\"-s -w\" -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm\n"+
					"    GOOS=linux GOARCH=amd64 go build -o zlily.linux ./cmd/zlily\n"+
					"    zlily deploy --binary zlily.linux --domain HOST --email ADDR",
				runtime.GOOS, runtime.GOARCH)
		}
		// The page is served from the binary's own embedded filesystem, so a
		// binary built without the wasm step would serve a /term/ that 404s on
		// its main asset. Catch that here rather than after a certificate has
		// been issued.
		if err := checkEmbeddedWasm(); err != nil {
			return "", err
		}
		exe, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("locate the running binary: %w", err)
		}
		src = exe
	}

	if err := copyFile(src, dst, 0o755); err != nil {
		return "", fmt.Errorf("stage binary: %w", err)
	}
	return dst, nil
}

// checkEmbeddedWasm reports whether this binary carries the browser client.
func checkEmbeddedWasm() error {
	fsys, err := webstatic.TermFS()
	if err != nil {
		return fmt.Errorf("read embedded browser client: %w", err)
	}
	if _, err := fs.Stat(fsys, "zlily.wasm"); err != nil {
		return errors.New(
			"this binary has no zlily.wasm embedded, so the browser client would not load.\n" +
				"Build it and rebuild zlily, then deploy again:\n" +
				"    GOOS=js GOARCH=wasm go build -ldflags=\"-s -w\" -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm\n" +
				"    go build ./cmd/zlily")
	}
	return nil
}

// baseImage picks a runtime image that can actually execute the binary.
func baseImage(path string) string {
	static, err := isStaticELF(path)
	if err != nil || !static {
		// Unreadable or dynamically linked: take the base that has a libc.
		return "debian:stable-slim"
	}
	return "gcr.io/distroless/static-debian12"
}

// isStaticELF reports whether the binary names a dynamic loader.
func isStaticELF(path string) (bool, error) {
	f, err := elf.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return false, nil
		}
	}
	return true, nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// ensureACMEFile creates acme.json if absent, and in every case makes sure its
// mode is one Traefik will accept. An existing file is never truncated: it
// holds issued certificates, and discarding them invites Let's Encrypt's
// duplicate-certificate rate limit.
func ensureACMEFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func writeTemplate(path, tmpl string, data any) error {
	t, err := template.New(filepath.Base(path)).Parse(tmpl)
	if err != nil {
		return fmt.Errorf("parse template for %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := t.Execute(f, data); err != nil {
		_ = f.Close()
		return fmt.Errorf("render %s: %w", path, err)
	}
	return f.Close()
}

// composeCommand reports how Compose is invoked on this host.
//
// Docker ships it two ways. Modern installs have it as a CLI plugin, invoked
// as "docker compose"; before that it was a standalone "docker-compose"
// binary, which is still what plenty of hosts have. Assuming the plugin gets
// you "unknown shorthand flag: 'd'" from the docker CLI, which does not read
// as "Compose is not installed the way you expected".
//
// The plugin is probed by running it rather than by looking for a file,
// because it lives in a plugin directory rather than on PATH.
func composeCommand() ([]string, error) {
	if _, err := exec.LookPath("docker"); err == nil {
		if err := exec.Command("docker", "compose", "version").Run(); err == nil {
			return []string{"docker", "compose"}, nil
		}
	}
	if path, err := exec.LookPath("docker-compose"); err == nil {
		return []string{path}, nil
	}
	return nil, errors.New(
		"no Docker Compose found: neither `docker compose` (the CLI plugin) nor `docker-compose` " +
			"(the standalone binary) runs here.\n" +
			"The deployment was written but not started; install one and run it yourself:\n" +
			"    cd " + composeHint + " && docker compose up -d --build")
}

// composeHint names the directory in the message above; the real one is
// substituted by the caller, which knows it.
const composeHint = "<deployment directory>"

// composeUp builds and starts the stack.
func composeUp(opts Options) error {
	compose, err := composeCommand()
	if err != nil {
		return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), composeHint, opts.Dir))
	}

	shown := strings.Join(compose, " ")
	_, _ = fmt.Fprintf(opts.Out, "\n  %s up -d --build\n\n", shown)

	args := append(append([]string{}, compose[1:]...), "up", "-d", "--build")
	cmd := exec.Command(compose[0], args...)
	cmd.Dir = opts.Dir
	cmd.Stdout = opts.Out
	cmd.Stderr = opts.Out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s up: %w", shown, err)
	}

	_, _ = fmt.Fprintf(opts.Out, "\n  https://%s/term/\n", opts.Domain)
	_, _ = fmt.Fprintf(opts.Out, "\nThe certificate is issued on the first request and takes a few seconds.\n")
	if opts.Staging {
		_, _ = fmt.Fprintf(opts.Out, "This used the Let's Encrypt staging CA, so browsers will not trust it.\n"+
			"Re-run without --staging for a real certificate.\n")
	}
	return nil
}
