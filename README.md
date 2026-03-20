# Eternal

Persistent terminal session manager. Run any command in a server-side PTY that survives SSH disconnections, and reconnect from the CLI or a web browser.

```
et run htop ──WS──▶ et server ──pty──▶ htop
                          │
                     WebSocket
                          │
                          ▼
                   Browser (xterm.js)
```

## Features

- **Persistent sessions** — PTY lives on the server. SSH disconnect does not kill the process.
- **Web UI** — Connect to any session from a browser via xterm.js. The terminal view follows the CLI terminal size.
- **Multiple clients** — Multiple CLI and browser clients can view the same session simultaneously.
- **Single binary** — One `et` binary for both server and client. Web UI is embedded.
- **Easy daemon install** — `et install | bash` sets up launchd (macOS) or systemd (Linux).
- **Auto-cleanup** — When the command exits (Ctrl-C, `:q`, `exit`, etc.), the session is removed automatically.
- **Session aggregation** — Connect to multiple eternal servers and view all sessions in a single Web UI.

## Quick Start

### 1. Start the server

```bash
et server
```

Or install as a system daemon:

```bash
et install | bash
```

### 2. Run a command

```bash
et run htop
```

Without arguments, your current shell (`$SHELL`) is launched:

```bash
et run
```

### 3. Disconnect and reconnect

Disconnect SSH. The process keeps running. Reconnect later:

```bash
et attach <name|id>
```

### 4. Or use the Web UI

Open `http://<host>:2840` in a browser. Click a session to connect.

## Commands

| Command | Description |
|---|---|
| `et server` | Start the server daemon (default: 0.0.0.0:2840) |
| `et server --peer host1 --peer host2:3000` | Start with peer servers for session aggregation |
| `et run [command] [args...]` | Run a command in a new persistent session |
| `et run --name work vim todo.md` | Run with a session name |
| `et attach <name\|id>` | Reattach to an existing session |
| `et ls` | List active sessions |
| `et kill <name\|id>` | Kill a session (sends SIGKILL) |
| `et install` | Output daemon install script for this platform |

## Session Lifecycle

```
et run htop
    │
    ▼
 Active (client connected, process running)
    │
    ├── SSH disconnect ──▶ Detached (no clients, process alive)
    │                          ├── et attach ──▶ Active
    │                          ├── Browser connect ──▶ Active
    │                          └── et kill ──▶ Dead
    │
    ├── Ctrl-C / exit (process exits) ──▶ Dead (auto-removed)
    │
    └── Kill from browser ──▶ Dead
```

## Web UI

The embedded Web UI provides:

- Session list in a sidebar, grouped by working directory
- xterm.js terminal that follows the CLI terminal size (scales down if the browser is smaller)
- Kill button per session
- Server hostname display

## Session Aggregation

View sessions from multiple eternal servers in a single Web UI. The browser connects directly to each peer server via its API and WebSocket endpoints.

### Usage

Specify peer servers with `--peer` flag or `ET_PEERS` environment variable:

```bash
# Via CLI flag (one per --peer)
et server --peer server-a.local --peer server-b.local:3000

# Via environment variable (comma-separated)
export ET_PEERS=server-a.local,server-b.local:3000
et server

# Both can be combined (merged)
ET_PEERS=server-a.local et server --peer server-b.local
```

Port defaults to **2840** if omitted. The scheme defaults to `http://`.

### How it works

1. The local server exposes a `GET /api/peers` endpoint that returns the configured peer addresses.
2. The Web UI fetches the peer list and queries each server's `GET /api/info` and `GET /api/sessions` endpoints in parallel.
3. Sessions are grouped by **hostname:directory** in the sidebar (when peers are configured). Local-only sessions show just the directory name.
4. WebSocket connections go directly from the browser to each server — no proxying through the local server.

### Requirements

- Peer servers must be reachable from the browser (not just from the local server).
- Peer servers have CORS enabled by default on `/api/*` endpoints.

## Build

Requirements: Go 1.21+, Node.js 18+

```bash
make build
```

This runs `npm install && npm run build` in `web/`, copies the output to `cmd/et/dist/`, then builds the Go binary.

## Architecture

```
eternal/
├── cmd/et/          # Single binary (server + client)
├── pkg/
│   ├── session/     # PTY management, lifecycle, scrollback buffer
│   ├── server/      # HTTP/WS server, REST API, embedded Web UI
│   ├── client/      # WebSocket client library
│   └── protocol/    # WebSocket message types
├── web/             # TypeScript + Vite + Tailwind CSS 4 sub-project
│   └── src/
└── Makefile
```

## Configuration

Default port: **2840** (0.0.0.0).

```bash
et server --host 0.0.0.0 --port 3000
```

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--host` | — | `0.0.0.0` | Host to bind to |
| `--port` | — | `2840` | Port to listen on |
| `--peer` | `ET_PEERS` | (none) | Peer server address (repeatable). `ET_PEERS` is comma-separated. |

## Limitations

- Sessions do not survive server restarts. All PTY processes are children of the server; stopping the server terminates them.
- No authentication in the current version. Bind to localhost or use an SSH tunnel for remote access.

## License

MIT
