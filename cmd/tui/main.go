package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/joshw/zephyrlily/internal/tui/client"
	"github.com/joshw/zephyrlily/internal/tui/ui"
)

func main() {
	proxy := flag.String("proxy", "localhost:7888", "zlily-proxy address")
	user := flag.String("user", "", "Lily username")
	pass := flag.String("pass", "", "Lily password")
	flag.Parse()

	if *user == "" {
		fmt.Fprintln(os.Stderr, "usage: zlily -user <username> -pass <password> [-proxy <addr>]")
		os.Exit(1)
	}

	c := client.New(*proxy)

	if err := c.Auth(*user, *pass); err != nil {
		log.Fatalf("auth: %v", err)
	}

	state, err := c.FetchState()
	if err != nil {
		log.Fatalf("state: %v", err)
	}

	if err := c.Connect(); err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer c.Close()

	m := ui.New(c, state)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
