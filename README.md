# ZephyrLily

A modern terminal UI client for the Lily chat server, inspired by TigerLily but written from scratch in Go.

ZephyrLily connects to a Lily server (such as `rpi.lily.org`) through a local proxy service, providing a feature-rich terminal interface for real-time chat, discussions, presence tracking, and more. It's designed to be fast, extensible, and pleasant to use in the terminal.

## Architecture

ZephyrLily consists of two main components:

- **`zlily-proxy`** — A local daemon that bridges the TUI to the Lily server over HTTP and WebSocket, handling authentication, connection lifecycle, and event buffering
- **`zlily`** — The terminal user interface client, built with [Bubble Tea](https://github.com/charmbracelet/bubbletea)

Communication flows: `TUI ←→ HTTP/WS ←→ proxy ←→ TCP ←→ Lily server`

The separation allows multiple clients to connect to the same proxy session without creating duplicate server connections, and makes the system resilient to client disconnects.

## Clients

### Terminal UI

The primary client is a feature-rich terminal interface built with [Bubble Tea](https://github.com/charmbracelet/bubbletea). It provides full Lily functionality including intelligent name expansion, history navigation, and emacs-style keybindings.

### Web UI (Experimental)

An experimental browser-based client is available in the `web/` directory. Built with Svelte and Vite, it provides a Discord-like interface for basic chat and presence tracking. See [Web UI Documentation](docs/webui.md) for details on building and running it.

## Getting Started

### Prerequisites
- Go 1.21 or later
- Node.js 18+ (for building web assets used by the proxy)

### Build and Run

#### Simple: Single Command

```bash
# Build the client
go build ./cmd/zlily

# Run the client (starts proxy automatically and connects)
./zlily
```

The client will start the proxy automatically and prompt for your Lily username and password on startup.

#### With Web UI

Run both the terminal UI and web UI on the same port:

```bash
go build ./cmd/zlily

# TUI + web UI (web UI accessible at http://127.0.0.1:PORT printed on startup)
./zlily --web

# With HTTPS (self-signed certificate auto-generated)
./zlily --web --web-tls
```

Or run just the proxy with web UI (no terminal UI):

```bash
# Proxy only + web UI
./zlily server --listen :7888 --web

# Proxy only + web UI with HTTPS
./zlily server --listen :7888 --web --web-tls
```

#### Advanced: Separate Proxy and Client(s)

Run the proxy in one terminal and multiple clients in others:

```bash
go build ./cmd/zlily

# Terminal 1: Start the proxy
./zlily server

# Terminal 2+: Start TUI clients connecting to the proxy
./zlily client

# Or connect with custom proxy address
./zlily client --proxy localhost:7888
```

Each client will connect to the same proxy session. The proxy persists event history and session state, so clients can disconnect and reconnect without losing message history or scroll position.

## Documentation

- **[Authentication and Login Flow](docs/auth.md)** — How the TUI and proxy authenticate with the Lily server, including token management and session persistence
- **[Client API Reference](docs/client-api.md)** — Complete HTTP and WebSocket API for the proxy; use this if building a custom client
- **[Event Types Reference](docs/events.md)** — Comprehensive guide to all Lily event types, display conventions, and how the TUI renders them
- **[Web UI (Experimental)](docs/webui.md)** — Browser-based client built with Svelte; architecture, setup, and development guide
- **[Releasing](docs/releasing.md)** — Instructions for creating and publishing new releases using GoReleaser

## Features (Terminal UI)

- **Rich text formatting** — Supports colors, timestamps, and structured layout for messages
- **Paste mode** — Intelligently handles pasted multi-line text
- **Name expansion** — Smart tab-completion and intelligent-expand (`;`, `:`, `,`, `=` key bindings)
- **History navigation** — Full command history with incremental search
- **Emacs-style keybindings** — Familiar navigation and editing commands
- **Unicode support** — Automatic conversion of Unicode input to ASCII for Lily compatibility
- **Cross-platform** — Runs on macOS, Linux, and Windows (with platform-specific adjustments)
- **Reconnection** — Seamless reconnection with history restoration on proxy disconnect

## Platform Notes

Terminal UI platform support:

- **Windows cmd.exe** — Limited key binding support due to terminal emulator limitations; Windows Terminal or similar recommended
- **macOS/Linux** — Full key binding support in standard terminals

## Project Status

This project was built rapidly as a modern replacement for TigerLily, demonstrating how a clean architecture and proper separation of concerns (proxy + TUI) can deliver a pleasant user experience for a legacy chat system.
