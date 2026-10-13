![Portico — many services, one front door by KloudKIT](https://raw.githubusercontent.com/kloudkit/portico/main/brand/portico-banner.svg)

# KloudKIT • Portico

[![Stars](https://img.shields.io/github/stars/kloudkit/portico?style=for-the-badge&logo=git&logoColor=c6d0f5&labelColor=414559&color=f0c6c6)](https://github.com/kloudkit/portico/stargazers)
[![Release](https://img.shields.io/github/v/release/kloudkit/portico?style=for-the-badge&logo=github&logoColor=c6d0f5&label=release&labelColor=414559&color=ef9f76)](https://github.com/kloudkit/portico/releases/latest)
[![Build](https://img.shields.io/github/actions/workflow/status/kloudkit/portico/ci.yaml?style=for-the-badge&logo=githubactions&logoColor=c6d0f5&label=build&labelColor=414559&color=a6da95)](https://github.com/kloudkit/portico/actions/workflows/ci.yaml)
[![License](https://img.shields.io/github/license/kloudkit/portico?style=for-the-badge&logo=opensourceinitiative&logoColor=c6d0f5&labelColor=414559&color=8caaee)](https://github.com/kloudkit/portico/blob/main/LICENSE)

A web app in development is rarely one process: a frontend dev server, an API,
maybe a config server and a websocket service, each on its own port. That works
on a laptop. It breaks behind a gateway that gives every port its own origin
(Cloud Workstations, Codespaces, a remote dev box): the browser sees
cross-origin calls, CORS and auth redirects get in the way, and `localhost`
points at the wrong machine.

Portico starts those services and puts one port in front of all of them. The
browser only talks to Portico, and Portico routes each path to the right
service.

## Features

- **Runs your services:** shell commands, container images and compose files,
  with their output in one stream and a clean stop on `Ctrl-C`.
- **One origin:** `/` goes to the frontend, `/api` to the API. No CORS, no
  per-port auth. Websockets and server-sent events pass straight through.
- **Dev-server friendly:** upstreams see `Host: localhost:<port>`, so Vite and
  webpack host checks pass; `X-Forwarded-*` headers say where the app really lives.
- **Knows what's up:** JSON `503`s for services that are down, crashed (with
  their last lines) or still starting per Docker's healthcheck.
- **Quick to set up:** `portico init` writes the config, flags cover the rest.

## Installation

Linux only. Docker or Podman is needed only for `image` and `compose` services.

```sh
mkdir -p ~/.local/bin
curl -fsSL -o ~/.local/bin/portico \
  https://github.com/kloudkit/portico/releases/latest/download/portico-amd64
chmod +x ~/.local/bin/portico
```

Use `portico-arm64` on arm64, or `go install github.com/kloudkit/portico@latest`.

## Quick start

```sh
portico init    # finds compose files and package.json dev scripts
portico up      # starts everything on http://localhost:4400
```

Or skip the file:

```sh
portico up --run 'web=npm run dev -- --port {port}' --route /=web --route /api=3200
```

Behind a gateway, open the forwarded URL for port `4400`; Portico logs it once
the first request comes through.

## Configuration

`portico.yaml` is found in the current directory or its parents, up to the git
root. The first line gives your editor autocompletion:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/kloudkit/portico/main/portico.schema.json

services:
  web:
    run: npm run dev -- --port {port}
    dir: frontend
    route: /
  api:
    port: 3200
  config-server:
    image: registry.example.com/config-server
    tag: "2.4"
    port: 8080
    route: /config
  stack:
    compose: ./compose.yaml
```

`portico routes` shows what that resolves to without starting anything.

A service is one of:

- **`run`:** a command Portico starts in `dir`. `{port}` becomes its port, also
  set as `PORT`; leave `port` out for a free one, or set `port: none` for a
  worker that serves nothing.
- **`image`:** a container; `port` is the container port, published on a free
  host port.
- **`compose`:** a compose file; every published port gets a route, at
  `<route>/<service>`. Use `service` for just one, and `routes` to move some.
- **only `port`:** something you start yourself, such as an API under a
  debugger.

| Key     | Description                                                             |
| ------- | ----------------------------------------------------------------------- |
| `route` | Path to mount it at *(default: `/<name>`)*.                             |
| `strip` | Remove the route prefix before forwarding *(default: `true`)*.          |
| `host`  | `preserve` forwards the browser's `Host` instead of `localhost:<port>`. |
| `quiet` | Keep its output off the screen.                                         |
| `log`   | `false` writes no log file for it.                                      |

Service names use `a-z`, `0-9`, `_`, `.` and `-`; `portico` and `nginx` are
reserved. Most dev servers read `.env` themselves; for one that doesn't, use
`run: set -a; . ./.env; set +a; ./start`.

### Proxy

| Key             | Default               | Description                                   |
| --------------- | --------------------- | --------------------------------------------- |
| `port`          | `4400`                | Port Portico listens on.                      |
| `host`          | `0.0.0.0`             | Address Portico listens on.                   |
| `base`          | `/`                   | Prefix mounted in front of every route.       |
| `engine`        | `builtin`             | `builtin`, or `nginx` for production parity.  |
| `image` / `tag` | `nginx:1.30.5-alpine` | nginx image, when nginx isn't installed.      |
| `cors`          | `["*"]`               | Origins allowed to call Portico cross-origin. |

`--port`, `--host` and `--base` override the file; `--run name=command` and
`--route /path=port|name` add services without one.

## Running

```sh
portico up                  # everything, output prefixed by service
portico up web api          # only these
portico up --tui            # live dashboard: ↑/↓ pick a service, r restart, PgUp/PgDn scroll
portico up --watch          # reload when portico.yaml changes, restarting only what changed
```

`--hide`, `--timestamps`, `--raw` and `--no-logs` shape the output. Each
service's output also lands in `.portico/logs/<name>.log`, rotated at 10 MB.
A service that crashes stays down so its error stays visible.

From another terminal, under the same config:

```sh
portico status              # states, routes and the public URL
portico logs api -f         # follow one service, or all of them
portico restart api
portico down                # stop everything, even after Portico was killed
```

### Command reference

| Command                   | Description                                                   |
| ------------------------- | ------------------------------------------------------------- |
| `portico init`            | Write a starter `portico.yaml` from what's in this directory. |
| `portico up [service…]`   | Start your services and the proxy in front of them.           |
| `portico routes`          | Print the resolved route table without starting anything.     |
| `portico status`          | Show the state of every service of a running Portico.         |
| `portico logs [service…]` | Print service output from `.portico/logs`.                    |
| `portico restart <svc>…`  | Restart services of a running Portico.                        |
| `portico down`            | Stop a running Portico, or clean up what a crashed one left.  |

Every command takes `-c`, `--config FILE` to point at a `portico.yaml` instead of
searching upwards for one.

| Flag                         | Command | Description                                                       |
| ---------------------------- | ------- | ----------------------------------------------------------------- |
| `--run name=command`         | `up`    | Run a service; `{port}` is substituted.                           |
| `--route /path=port\|name`   | `up`    | Route a path to a port or a service.                              |
| `--port`, `--host`, `--base` | `up`    | Override the proxy's port, address and route prefix.              |
| `--tui`                      | `up`    | Show a live dashboard instead of the log stream.                  |
| `--watch`                    | `up`    | Reload when `portico.yaml` changes, restarting only what changed. |
| `--hide service`             | `up`    | Keep a service's output off the screen (it is still logged).      |
| `--timestamps`               | `up`    | Prefix every line with the time.                                  |
| `--raw`                      | `up`    | Print output without prefixes or colors.                          |
| `--no-logs`                  | `up`    | Don't write service output to `.portico/logs`.                    |
| `-f`, `--follow`             | `logs`  | Keep printing new output.                                         |
| `-n`, `--lines N`            | `logs`  | Lines to show from the end of each log *(default: `20`)*.         |
| `-y`, `--yes`                | `init`  | Add everything found without asking.                              |
| `--force`                    | `init`  | Replace an existing `portico.yaml`.                               |

## Behind the scenes

- **Containers:** an image or compose service with a Docker `HEALTHCHECK` is
  routed only once Docker reports it healthy (builtin engine; nginx routes it
  as soon as its port answers).
- **nginx engine:** Portico writes the nginx config from the same routes and
  runs nginx from `PATH` or a container. What nginx can't serve falls back to
  Portico, so errors look the same.
- **CORS:** your own frontend never needs it. Cross-origin callers get answers
  from Portico itself; on a laptop "any origin" includes any website you visit,
  so list them (`https://*.cloudworkstations.dev`) or set `cors: []`.
- **Origin checks:** apps that compare `Origin` to `Host`, like Django's CSRF
  check, should trust `X-Forwarded-Host` and `-Proto`, or use `host: preserve`.

## License

Released under the [**MIT License**](https://github.com/kloudkit/portico/blob/main/LICENSE),
**except** the [`brand/`](https://github.com/kloudkit/portico/tree/main/brand) directory.

The Portico and KloudKIT logos, icons and other brand assets are proprietary and
**all rights reserved**; see [`brand/LICENSE`](https://github.com/kloudkit/portico/blob/main/brand/LICENSE).
