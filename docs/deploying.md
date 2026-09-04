# Deploying the browser TUI

`zlily deploy` generates a Docker deployment that serves the browser TUI over
HTTPS, with [Traefik](https://traefik.io) terminating TLS using a Let's Encrypt
certificate, and starts it.

```sh
zlily deploy --domain lily.example.org --email you@example.org
```

That writes `./zlily-deploy/` and runs `docker compose up -d --build`. Add
`--no-start` to stop after generating so you can read the files first.

## Prerequisites

- **DNS for the domain already points at this host**, and **ports 80 and 443
  reach it**. Port 80 is not only there for the HTTPS redirect — the ACME HTTP
  challenge is answered on it, so a certificate cannot be issued without it.
- Docker, with Compose available either way: as the CLI plugin (`docker
  compose`) or as the older standalone binary (`docker-compose`). `zlily deploy`
  probes for both and uses whichever runs.

  The plugin is worth preferring. docker-compose 1.x is end-of-life and fails
  against a current Docker daemon whenever it *replaces* an existing container,
  because it reads a field (`ContainerConfig`) that recent Docker Engine no
  longer returns:

      container.image_config['ContainerConfig'].get('Volumes') or {}
      KeyError: 'ContainerConfig'

  So the first deploy works and the second dies in a Python traceback. Nothing
  in the deployment causes it or can avoid it. `docker-compose down` first, and
  it creates rather than replaces:

      cd zlily-deploy && docker-compose down && docker-compose up -d --build
- Run it **on the Linux host you are deploying to** (see below).

Use `--staging` for the first run against a new domain. It requests from Let's
Encrypt's staging CA, which browsers do not trust but whose rate limits are
generous — worth it while DNS or firewall rules are still being sorted out.
Re-run without it for a real certificate.

## Where the binary comes from

The image ships **the running zlily binary**, copied into the build context.
No toolchain, no source checkout, no download — and what runs in the container
is exactly the binary you tested.

That only works when the running binary can execute in a Linux container, so
run `zlily deploy` on the host itself. From a Mac it refuses rather than
building an image that cannot start, and tells you to cross-compile:

```sh
GOOS=js GOARCH=wasm go build -ldflags="-s -w" \
  -o internal/webstatic/term/zlily.wasm ./cmd/zlily-wasm
GOOS=linux GOARCH=amd64 go build -o zlily.linux ./cmd/zlily
zlily deploy --binary zlily.linux --domain lily.example.org --email you@example.org
```

Two things it checks so they fail here rather than in production:

- **The binary embeds `zlily.wasm`.** The page is served from the binary's own
  embedded filesystem, so one built without the wasm step serves a `/term/`
  that 404s on its main asset.
- **Whether the binary is statically linked**, by reading its ELF headers. A
  cgo-linked binary on a distroless static base fails with "no such file or
  directory" pointing at an entrypoint that plainly exists — one of the more
  confusing Docker errors. Static binaries get `distroless/static`, dynamic
  ones get `debian:stable-slim`.

## Pinned images

The generated `docker-compose.yml` pins Traefik to an exact version rather than
using the `traefik` tag. A floating tag resolves to whatever the host has
already cached, and a host that has run Traefik before may have one old enough
to speak Docker API 1.24, which a current daemon refuses:

    Provider connection error ... client version 1.24 is too old.
    Minimum supported API version is 1.44

Traefik then registers no routers and answers every request with 404, which
reads as a routing mistake rather than a stale image. Edit the pin in the
generated file to move it.

## Generated files

| File | |
|---|---|
| `zlily` | The binary. |
| `Dockerfile` | Wraps it in the base image chosen above. |
| `docker-compose.yml` | The zlily service, and Traefik in front of it. |
| `letsencrypt/acme.json` | Issued certificates, mode 0600. **Back this up.** |
| `README.md` | Operating notes for whoever finds the directory later. |

Only Traefik publishes ports; zlily is reachable only on the Compose network.
Re-running `zlily deploy` restages the binary and regenerates the files but
never truncates `acme.json`, so no new certificate is requested.

## Exposure

The deployment talks to **exactly one Lily server**, fixed by `--lily` in the
generated `command:` block. This was never client-controllable — `handleAuth`
has always used the proxy's own configured address — so no client can point the
deployment at a different server.

What a public URL *does* expose is a login form for that Lily server. Two
limits apply, both visible in the compose file:

- **`--auth-max-failures`** (default 5 per 5 minutes, then a 5-minute lockout).
  Keyed on the client address, which past a reverse proxy means
  `X-Forwarded-For` — hence `--behind-proxy` in the generated command. Do not
  set that flag when nothing trusted sits in front, or clients can forge the
  header and mint themselves a fresh allowance.
- **`--max-sessions`** (default 64) caps concurrent sessions, and so the number
  of Lily connections a stranger can cause this host to open.

Neither makes the URL private. If it should not be a login form open to the
whole internet, add Traefik basic auth in front of the router, or restrict
access at the firewall.

## Connecting a terminal client to it

The deployment is a proxy like any other, so the native TUI can use it — the
browser client is not the only way in:

```sh
zlily client --proxy https://lily.example.org
```

The scheme matters. A bare `host:port` is spoken over plain HTTP, so
`--proxy lily.example.org:443` sends unencrypted HTTP at the TLS port; Traefik
finds no non-TLS router and answers 404, without the request ever reaching
zlily. The error says nothing about the cause, so pass a URL.

## Operating

```sh
cd zlily-deploy
docker compose logs -f zlily     # follow the proxy log
docker compose restart zlily     # after a config change
docker compose down              # stop
```

To upgrade: rebuild zlily, then re-run `zlily deploy` with the same arguments.
