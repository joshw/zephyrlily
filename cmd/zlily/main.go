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
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/joshw/zephyrlily/internal/proxy/api"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/ui"
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
		case "help", "-h", "--help":
			printUsage()
			return
		}
	}
	// Default: combined mode
	cmdCombined(os.Args[1:])
}

// ── subcommands ───────────────────────────────────────────────────────────────

func cmdServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	listen := fs.String("listen", ":7888", "proxy listen address")
	lily := fs.String("lily", "rpi.lily.org:7777", "Lily server address")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: zlily server [flags]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	cfg := api.Config{ListenAddr: *listen, LilyAddr: *lily}
	srv := api.New(cfg)
	ctx := signalCtx()
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("proxy: %v", err)
	}
}

func cmdClient(args []string) {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	proxy := fs.String("proxy", "localhost:7888", "proxy address")
	user := fs.String("user", "", "Lily username")
	pass := fs.String("pass", "", "Lily password (prompted if omitted)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: zlily client [flags]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	username, password := resolveCredentials(*user, *pass)
	runTUI(*proxy, username, password)
}

func cmdCombined(args []string) {
	fs := flag.NewFlagSet("combined", flag.ExitOnError)
	lily := fs.String("lily", "rpi.lily.org:7777", "Lily server address")
	user := fs.String("user", "", "Lily username")
	pass := fs.String("pass", "", "Lily password (prompted if omitted)")
	port := fs.Int("port", 0, "embedded proxy port (0 = OS-assigned ephemeral)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: zlily [combined] [flags]")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	username, password := resolveCredentials(*user, *pass)

	// Bind to an ephemeral port (or the one requested) on loopback only.
	listenAddr := fmt.Sprintf("127.0.0.1:%d", *port)
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("proxy listen: %v", err)
	}
	proxyAddr := l.Addr().String()

	cfg := api.Config{ListenAddr: proxyAddr, LilyAddr: *lily}
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
	runTUI(proxyAddr, username, password)
	cancel()
	<-proxyDone
}

// ── shared helpers ────────────────────────────────────────────────────────────

// runTUI connects to the proxy and starts the Bubble Tea event loop.
func runTUI(proxyAddr, username, password string) {
	c := client.New(proxyAddr)

	if err := c.Auth(username, password); err != nil {
		log.Fatalf("auth: %v", err)
	}
	if err := c.Connect(); err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer c.Close()

	logChan, logger := ui.NewLogger()
	slog.SetDefault(logger)

	m := ui.New(c, logChan)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}

// resolveCredentials returns username and password, prompting for any that are
// empty.  Username is shown; password input is hidden.
func resolveCredentials(username, password string) (string, string) {
	if username == "" {
		username = promptLine("Username: ")
	}
	if username == "" {
		fmt.Fprintln(os.Stderr, "username is required")
		os.Exit(1)
	}
	if password == "" {
		password = promptPassword("Password: ")
	}
	return username, password
}

// promptLine prints msg to stderr and reads a line from stdin.
func promptLine(msg string) string {
	fmt.Fprint(os.Stderr, msg)
	r := bufio.NewReader(os.Stdin)
	s, _ := r.ReadString('\n')
	return strings.TrimRight(s, "\r\n")
}

// promptPassword prints msg to stderr and reads a password without echo.
func promptPassword(msg string) string {
	fmt.Fprint(os.Stderr, msg)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr) // newline after the hidden input
	if err != nil {
		// Fall back to visible input (e.g. piped stdin).
		r := bufio.NewReader(os.Stdin)
		s, _ := r.ReadString('\n')
		return strings.TrimRight(s, "\r\n")
	}
	return string(b)
}

// signalCtx returns a context cancelled on SIGINT or SIGTERM.
func signalCtx() context.Context {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	_ = stop // stop is called automatically when ctx is cancelled
	return ctx
}

func printUsage() {
	fmt.Fprint(os.Stderr, `zlily — ZephyrLily Lily chat client

Usage:
  zlily [flags]                 Combined mode (TUI + embedded proxy, default)
  zlily combined [flags]        Same as above
  zlily server  [flags]         Proxy only (no TUI)
  zlily client  [flags]         TUI only (connect to a running proxy)

Combined flags:
  --lily   addr   Lily server address (default: rpi.lily.org:7777)
  --user   name   Lily username       (prompted if not provided)
  --pass   secret Lily password       (prompted if not provided)
  --port   n      Embedded proxy port (default: OS-assigned ephemeral)

Server flags:
  --lily   addr   Lily server address (default: rpi.lily.org:7777)
  --listen addr   Proxy listen address (default: :7888)

Client flags:
  --proxy  addr   Proxy address  (default: localhost:7888)
  --user   name   Lily username  (prompted if not provided)
  --pass   secret Lily password  (prompted if not provided)
`)
}
