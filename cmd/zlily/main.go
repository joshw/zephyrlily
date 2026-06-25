// zlily is the ZephyrLily Lily chat client.
//
// Usage:
//
//	zlily [flags]                 Run TUI + embedded proxy in the same process (default)
//	zlily combined [flags]        Same as above
//	zlily server  [flags]         Run the proxy only (no TUI)
//	zlily client  [flags]         Run the TUI connecting to an existing proxy
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/pflag"

	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/inputguard"
	"github.com/joshw/zephyrlily/internal/tui/ui"
	"github.com/joshw/zephyrlily/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "server":
			cmdServer(os.Args[2:])
			return
		case "client":
			cmdClient(os.Args[2:])
			return
		case "combined":
			cmdCombined(os.Args[2:])
			return
		case "version", "-v", "--version":
			fmt.Println("zlily", version.String())
			return
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}
	// Default: combined mode
	cmdCombined(os.Args[1:])
}

// ── subcommands ───────────────────────────────────────────────────────────────

// ensureTmuxColor works around tmux not forwarding COLORTERM and reporting a
// conservative TERM, which makes termenv downgrade the color profile so color
// renders poorly. tmux passes truecolor escapes through and downsamples to the
// host terminal's real capability, so forcing it here is safe. We only set it
// when unset, never overriding a value the user already exported.
// See https://github.com/charmbracelet/bubbletea/issues/825
func ensureTmuxColor() {
	if os.Getenv("TMUX") != "" && os.Getenv("COLORTERM") == "" {
		_ = os.Setenv("COLORTERM", "truecolor")
	}
}

func isCmdExe() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	// cmd.exe doesn't set TERM; modern terminals (Windows Terminal, ConEmu, etc.) do
	return os.Getenv("TERM") == ""
}

func cmdServer(args []string) {
	fs := pflag.NewFlagSet("server", pflag.ExitOnError)
	listen := fs.String("listen", ":7888", "proxy API listen address (used by TUI clients)")
	lily := fs.String("lily", "rpi.lily.org:7777", "Lily server address")
	tlsFlag := fs.Bool("tls", false, "connect to Lily over TLS")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS certificate verification (use with caution)")
	webUI := fs.Bool("web", false, "serve the web UI")
	webTLS := fs.Bool("web-tls", false, "serve the web UI over HTTPS")
	webCert := fs.String("web-cert", "", "TLS certificate PEM for web UI (default: self-signed)")
	webKey := fs.String("web-key", "", "TLS private key PEM for web UI (default: self-signed)")
	logLevel := fs.String("log-level", "info", "minimum log level to display (debug, info, warn, error)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: zlily server [flags]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseSlogLevel(*logLevel),
	})))

	cfg := api.Config{
		ListenAddr: *listen, LilyAddr: *lily,
		LilyTLS: *tlsFlag, LilyTLSInsecure: *tlsInsecure,
		ServeWeb: *webUI, WebTLS: *webTLS,
		WebCertFile: *webCert, WebKeyFile: *webKey,
	}
	if *webUI {
		fmt.Fprintln(os.Stderr, "Web UI:", webURL(*listen, *webTLS))
	}
	srv := api.New(cfg)
	ctx := signalCtx()
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("proxy: %v", err)
	}
}

func cmdClient(args []string) {
	fs := pflag.NewFlagSet("client", pflag.ExitOnError)
	proxy := fs.String("proxy", "localhost:7888", "proxy address")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: zlily client [flags]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	var startupMsgs []string
	if isCmdExe() {
		startupMsgs = append(startupMsgs, "⚠ Warning: cmd.exe has limited key support (PgUp/PgDn won't work)")
		startupMsgs = append(startupMsgs, "  Try Windows Terminal or ConEmu for full key binding support")
	}
	runTUI(*proxy, startupMsgs...)
}

func cmdCombined(args []string) {
	fs := pflag.NewFlagSet("combined", pflag.ExitOnError)
	lily := fs.String("lily", "rpi.lily.org:7777", "Lily server address")
	port := fs.Int("port", 0, "embedded proxy port (0 = OS-assigned ephemeral)")
	tlsFlag := fs.Bool("tls", false, "connect to Lily over TLS")
	tlsInsecure := fs.Bool("tls-insecure", false, "skip TLS certificate verification (use with caution)")
	webUI := fs.Bool("web", false, "serve the web UI")
	webTLS := fs.Bool("web-tls", false, "serve the web UI over HTTPS")
	webCert := fs.String("web-cert", "", "TLS certificate PEM for web UI (default: self-signed)")
	webKey := fs.String("web-key", "", "TLS private key PEM for web UI (default: self-signed)")
	logLevel := fs.String("log-level", "info", "minimum log level to display (debug, info, warn, error)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: zlily [combined] [flags]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(args)

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: parseSlogLevel(*logLevel),
	})))

	// Bind to an ephemeral port (or the one requested) on loopback only.
	listenAddr := fmt.Sprintf("127.0.0.1:%d", *port)
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("proxy listen: %v", err)
	}
	proxyAddr := l.Addr().String()

	cfg := api.Config{
		ListenAddr: proxyAddr, LilyAddr: *lily,
		LilyTLS: *tlsFlag, LilyTLSInsecure: *tlsInsecure,
		ServeWeb: *webUI, WebTLS: *webTLS,
		WebCertFile: *webCert, WebKeyFile: *webKey,
	}
	srv := api.New(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		cancel()
	}()

	// Start the proxy in the background.
	proxyDone := make(chan error, 1)
	go func() {
		proxyDone <- srv.RunWithListener(ctx, l)
	}()

	// Run the TUI in the foreground; cancel the proxy when it exits.
	// Pass the web URL as a startup message so it appears after the splash screen.
	var startupMsgs []string
	if isCmdExe() {
		startupMsgs = append(startupMsgs, "⚠ Warning: cmd.exe has limited key support (PgUp/PgDn won't work)")
		startupMsgs = append(startupMsgs, "  Try Windows Terminal or ConEmu for full key binding support")
	}
	if *webUI {
		startupMsgs = append(startupMsgs, "Web UI: "+webURL(proxyAddr, *webTLS))
	}
	runTUI(proxyAddr, startupMsgs...)
	cancel()
	<-proxyDone
}

// ── shared helpers ────────────────────────────────────────────────────────────

// runTUI creates the client and starts the Bubble Tea event loop.
// The TUI handles authentication interactively via modal dialog.
// startupMsgs are displayed below the logo on first render.
func runTUI(proxyAddr string, startupMsgs ...string) {
	ensureTmuxColor()

	c := client.New(proxyAddr)
	defer c.Close()

	logChan, logger := ui.NewLogger()
	slog.SetDefault(logger)

	m := ui.New(c, logChan, startupMsgs...)
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	// Interpose a sequence-boundary guard on stdin so Bubble Tea never sees an
	// escape sequence split across read boundaries (which leaks mouse reports
	// into the input during fast wheel scrolling). Only do this for a real
	// terminal; otherwise leave input to Bubble Tea's default TTY handling.
	// This is a workaround for a Bubble Tea parser bug — see package inputguard;
	// remove this line and the package together once it's fixed upstream.
	if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		opts = append(opts, tea.WithInput(inputguard.New(os.Stdin)))
	}
	p := tea.NewProgram(m, opts...)
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}

// parseSlogLevel maps a string level name to slog.Level, defaulting to Info.
func parseSlogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// webURL builds the browser-facing URL for the web UI from a listener address.
func webURL(addr string, tlsEnabled bool) string {
	scheme := "http"
	if tlsEnabled {
		scheme = "https"
	}
	// addr may be "127.0.0.1:PORT" — replace host with "localhost" for readability.
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return scheme + "://" + addr
	}
	if host == "127.0.0.1" || host == "::1" || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s:%s", scheme, host, port)
}

// signalCtx returns a context cancelled on SIGINT or SIGTERM.
func signalCtx() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	_ = stop // stop is called automatically when ctx is cancelled
	return ctx
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "zlily %s — ZephyrLily Lily chat client\n\n", version.String())
	fmt.Fprint(os.Stderr, `Usage:
  zlily [flags]                 Combined mode (TUI + embedded proxy, default)
  zlily combined [flags]        Same as above
  zlily server  [flags]         Proxy only (no TUI) — for headless/server use
  zlily client  [flags]         TUI only (connect to a running proxy)
  zlily version                 Print the version and exit
  zlily help                    Show this help and exit

Other flags:
  --version, -v         Print the version and exit
  --help, -h            Show this help and exit

Web interface:
  The web UI is disabled by default. Enable it with --web; the proxy and web UI
  share the same port (--listen). Open the printed URL in a browser.

  TUI + web UI:
    zlily --web

  Proxy-only server with web UI:
    zlily server --listen :7888 --web

  Proxy-only server, web UI over HTTPS (self-signed cert auto-generated):
    zlily server --listen :7888 --web --web-tls

  Proxy-only server, web UI with your own certificate:
    zlily server --listen :7888 --web --web-tls \
                 --web-cert cert.pem --web-key key.pem

Combined flags (zlily / zlily combined):
  --lily         addr   Lily server address     (default: rpi.lily.org:7777)
  --port         n      Embedded proxy port     (default: OS-assigned ephemeral)
  --tls                 Connect to Lily over TLS
  --tls-insecure        Skip TLS certificate verification (use with caution)
  --web                 Serve the web UI        (default: off)
  --web-tls             Serve the web UI over HTTPS
  --web-cert     file   TLS certificate PEM     (default: auto-generated self-signed)
  --web-key      file   TLS private key PEM     (default: auto-generated self-signed)
  --log-level    level  Log verbosity: debug, info, warn, error (default: info)

Server flags (zlily server):
  --lily         addr   Lily server address     (default: rpi.lily.org:7777)
  --listen       addr   Proxy listen address    (default: :7888)
  --tls                 Connect to Lily over TLS
  --tls-insecure        Skip TLS certificate verification (use with caution)
  --web                 Serve the web UI        (default: off)
  --web-tls             Serve the web UI over HTTPS
  --web-cert     file   TLS certificate PEM     (default: auto-generated self-signed)
  --web-key      file   TLS private key PEM     (default: auto-generated self-signed)
  --log-level    level  Log verbosity: debug, info, warn, error (default: info)

Client flags (zlily client):
  --proxy        addr   Proxy address  (default: localhost:7888)
`)
}
