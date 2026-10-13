# KloudKIT Portico

A web app in development is rarely one process. There's a frontend dev server, an
API, maybe a config server and a websocket service, each on its own port. That
works on a laptop. It breaks behind a gateway that gives every port its own
origin (Cloud Workstations, Codespaces, a remote dev box): the browser sees
cross-origin calls, CORS and auth redirects get in the way, and `localhost` points
at the wrong machine.

Portico puts one port in front of all of them. The browser only ever talks to
Portico, and Portico routes each path to the right service.

## Features

- **One origin:** `/` goes to the frontend, `/api` to the API, `/ws` to the
  websocket server. No CORS, no per-port auth.
- **Websockets and streaming:** upgrades, server-sent events and chunked
  responses pass straight through, with no idle timeout.
- **Dev-server friendly:** the upstream sees `Host: localhost:<port>`, so Vite,
  webpack and Angular host checks pass, and `X-Forwarded-Host`, `-Proto` and
  `-Prefix` tell the app where it really lives.
- **Compose aware:** point at a `compose.yaml` and its published ports become
  routes.
- **JSON errors:** a service that is down answers `503` with JSON, not an HTML
  page.
- **No config needed:** flags cover the simple cases; `portico.yaml` covers the
  rest.

## Requirements

- Linux.
- Docker or Podman, only for compose services.

## Installation

```sh
go install github.com/kloudkit/portico@latest
```

## Quick start

Start your services as usual, then put Portico in front of them:

```sh
portico up --route /=5173 --route /api=3200
```

Open `http://localhost:4400` (or the forwarded URL for port `4400`). Calls to
`/api/users` reach the API as `/users`.

## Configuration

Portico looks for `portico.yaml` or `.portico.yaml` in the current directory and
its parents, up to the git root. Use `-c` to point elsewhere.

```yaml
proxy:
  port: 4400

services:
  web:
    port: 5173
    route: /
  api:
    port: 3200
    route: /api
  stack:
    compose: ./compose.yaml
    route: /stack
```

Check what that resolves to without starting anything:

```sh
portico routes
```

### Proxy

| Key      | Default   | Description                         |
| -------- | --------- | ----------------------------------- |
| `port`   | `4400`    | Port Portico listens on.            |
| `host`   | `0.0.0.0` | Address Portico listens on.         |
| `base`   | `/`       | Prefix mounted in front of routes.  |
| `engine` | `builtin` | Proxy engine.                       |

### Services

| Key       | Description                                                          |
| --------- | -------------------------------------------------------------------- |
| `port`    | Port the service listens on.                                         |
| `route`   | Path to mount it at *(default: `/<name>`)*.                          |
| `strip`   | Remove the route prefix before forwarding *(default: `true`)*.       |
| `compose` | Compose file; every service with published ports gets a route.      |
| `service` | With `compose`, route a single compose service at `route`.          |
| `routes`  | With `compose`, override the path of individual compose services.   |

A compose service with several published ports is mounted at
`<route>/<port>` for each one. With `service`, set `port` to pick one.

### Flags

Flags override the file, and work without one:

```sh
portico up --port 8000 --route /=5173 --route /api=3200
```

- **`--route /path=port`:** route a path to a port.
- **`--route /path=name`:** route a path to a service from the file.
- **`--port`, `--host`, `--base`:** override the proxy settings.

## Status

Portico is early. Routing, compose discovery and the builtin proxy work today.
Starting services for you (`run`, `image`, `compose`), the nginx engine, and the
`status`, `logs`, `restart` and `down` commands are next.
