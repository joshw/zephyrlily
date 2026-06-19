# ZephyrLily Web Interface (Experimental)

The ZephyrLily project includes an experimental web-based client for the Lily server, built with [Svelte](https://svelte.dev/) and [Vite](https://vitejs.dev/). This document describes the web UI architecture, how to run it, and its current capabilities.

## Overview

The web UI provides a browser-based interface to Lily, with a Discord-like dark theme and responsive layout. It connects to the same `zlily-proxy` backend as the terminal client, making it possible to have multiple clients (web + terminal, web + web, etc.) connected to the same session.

```
Browser UI ──── HTTP/WS ──── zlily-proxy ──── TCP ──── Lily server
```

## Architecture

### Stack

- **Frontend framework:** Svelte 5 (reactive components)
- **Build tool:** Vite (fast dev server and production bundler)
- **Styling:** Scoped CSS in Svelte components
- **State management:** Svelte stores (reactive state)
- **Networking:** Fetch API (HTTP requests) + WebSocket (live updates)

### Components

| Component | Purpose |
|-----------|---------|
| `Login.svelte` | Authentication form; handles username/password entry and token acquisition |
| `Chat.svelte` | Main layout container; orchestrates sidebar and message list |
| `Sidebar.svelte` | Discussion/user list; filters and displays entities; supports search |
| `MessageList.svelte` | Displays chat messages with timestamps, formatted text, and threading |
| `Compose.svelte` | Message input field; handles send, enter key binding, placeholder text |

### Libraries

- **`lib/api.js`** — REST client for the proxy (login, state, expand, fetch, store endpoints)
- **`lib/ws.js`** — WebSocket client for live event streaming
- **`lib/store.js`** — Svelte stores for shared state (token, whoami, entities, messages, etc.)
- **`lib/events.js`** — Event parsing and formatting (converts raw proxy events to display text)

## Getting Started

### Prerequisites

- Node.js 18 or later
- A running `zlily-proxy` instance (default: `http://localhost:7888`)

### Development

From the `web/` directory:

```bash
# Install dependencies
npm install

# Start the dev server (hot-reload on file changes)
npm run dev
```

The dev server runs on `http://localhost:5173` by default and proxies API calls to `http://localhost:7888` (configurable in `vite.config.js`).

### Production Build

```bash
# Build optimized bundles
npm run build

# Outputs to web/dist/
```

Serve the contents of `dist/` with any static web server:

```bash
python3 -m http.server 8000 -d web/dist
```

Then visit `http://localhost:8000` in your browser.

## Features

### Implemented

- **Authentication** — Login form with username/password; tokens stored in session storage
- **Entity list** — Sidebar displaying users (with presence state: here/away/idle) and discussions
- **Message display** — Chronological message list with sender, timestamp, and formatted body
- **Message sending** — Input compose box with Enter to send
- **Live updates** — WebSocket event stream for real-time messages and presence changes
- **Responsive layout** — Adapts to different screen widths (sidebar collapsible on mobile)
- **Theme** — Discord-like dark theme (customizable CSS variables)

### Not Yet Implemented

- **Intelligent expand** — Tab completion and `;`, `:`, `,`, `=` key bindings (terminal UI only)
- **History navigation** — Arrow-key history in input (terminal UI only)
- **Paste mode** — Newline normalization for pasted text
- **Search** — Full-text search in message history
- **Memos/Info editing** — UI for fetching and storing `/info` and memo content
- **Discussion creation** — UI to create new discussions
- **User actions** — Blurb editing, presence toggling, etc.
- **Permission management** — UI for discussion permissions and roles

## API Usage

The web UI uses the same proxy API as the terminal client. See [client-api.md](client-api.md) for full endpoint documentation. Key calls:

- **`POST /auth`** — Login (username, password) → token
- **`GET /state`** — Initial state snapshot (entities, whoami, version)
- **`GET /expand?q=...`** — Name completion (for future search UI)
- **`WebSocket /ws?token=...`** — Live event stream
- **`POST /seen`** — Report scroll position

The web UI's `lib/api.js` wraps these endpoints with authentication and error handling.

## Development Notes

### Adding a New Component

1. Create a `.svelte` file in `src/components/`
2. Import it in the parent component (e.g., `App.svelte`)
3. Pass props for data and events (callbacks)
4. Use reactive declarations (`$:`) for computed state

Example:

```svelte
<script>
  import { entities } from '../lib/store.js';
  
  export let selectedUser = null;
  export let onSelect = () => {};
  
  $: filteredUsers = Object.values($entities).filter(e => e.kind === 'user');
</script>

{#each filteredUsers as user (user.handle)}
  <div on:click={() => onSelect(user)}>
    {user.name}
  </div>
{/each}
```

### Styling

Svelte components use scoped CSS by default:

```svelte
<style>
  div { color: red; }  /* Only applies within this component */
</style>
```

Global styles are defined in `App.svelte` within `:global()` selectors and CSS variables:

```svelte
<style>
  :global(body) {
    --bg-primary: #36393f;
    --text-normal: #dcddde;
  }
</style>
```

### Debugging

- Open the browser's Developer Tools (F12)
- Enable Svelte DevTools browser extension for reactive inspection
- Check `localStorage` and `sessionStorage` for token persistence
- Network tab shows API requests and WebSocket frames

## Configuration

### Proxy URL

Edit `vite.config.js` to change the proxy target:

```javascript
server: {
  proxy: {
    '/': 'http://your-proxy-host:7888'
  }
}
```

Or set the target in `lib/api.js` if the API is on a different origin.

### Theme

Edit CSS variables in `App.svelte` to customize colors:

```css
--bg-primary:   #36393f;
--text-normal:  #dcddde;
--accent:       #7289da;
```

## Future Enhancements

- **Service Worker** — Offline caching and sync
- **PWA manifest** — Install as standalone app on mobile
- **Message search** — Full-text search with filters
- **Rich text editor** — Markdown support in compose
- **Thread support** — Reply threads and nested conversations
- **User profiles** — Click-to-view blurbs, info, memos
- **Dark/light theme toggle** — User preference
- **Mobile-first redesign** — Optimize layout for small screens

## Current Status

The web UI is **experimental** and feature-complete enough for basic chat, but lacks advanced functionality present in the terminal client (intelligent expand, paste mode, complex editing commands). It serves as a proof of concept that the proxy API is generic enough for multiple clients.

The codebase is well-structured for contributors: Svelte components are isolated, state is centralized in stores, and the API layer is separated from UI logic.
