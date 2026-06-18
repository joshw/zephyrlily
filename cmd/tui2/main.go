package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui2/ui"
)

func main() {
	proxy := flag.String("proxy", "localhost:7888", "zlily-proxy address")
	user := flag.String("user", "", "Lily username")
	pass := flag.String("pass", "", "Lily password")
	flag.Parse()

	if *user == "" {
		fmt.Fprintln(os.Stderr, "usage: zlily-tui2 -user <username> -pass <password> [-proxy <addr>]")
		os.Exit(1)
	}

	c := client.New(*proxy)

	if err := c.Auth(*user, *pass); err != nil {
		log.Fatalf("auth: %v", err)
	}

	// Connect the WebSocket before starting the TUI
	if err := c.Connect(); err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer c.Close()

	// Logger must be set up before the TUI starts
	logChan, logger := ui.NewLogger()
	slog.SetDefault(logger)

	m := ui.New(c, logChan)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
