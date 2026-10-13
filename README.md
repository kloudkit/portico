![Portico — many services, one front door by KloudKIT](https://raw.githubusercontent.com/kloudkit/portico/main/brand/portico-banner.svg)

# KloudKIT Portico

A web app in development is rarely one process. There's a frontend dev server, an
API, maybe a config server and a websocket service, each on its own port. That
works on a laptop. It breaks behind a gateway that gives every port its own
origin (Cloud Workstations, Codespaces, a remote dev box): the browser sees
cross-origin calls, CORS and auth redirects get in the way, and `localhost` points
at the wrong machine.

Portico starts those services for you and puts one port in front of all of
them. The browser only ever talks to Portico, and Portico routes each path to
the right service.

## Features

- **Runs your services:** shell commands, container images and compose files,
  with their output in one stream and a clean stop on `Ctrl-C`.
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
  page. One that crashed includes its exit code and last lines of output.
- **No config needed:** flags cover the simple cases; `portico.yaml` covers the
  rest.

## Requirements

- Linux.
- Docker or Podman, only for `image` and `compose` services, or the nginx
  engine when nginx isn't installed.

## Installation

Grab a binary from the [releases](https://github.com/kloudkit/portico/releases):

```sh
curl -fsSL -o /usr/local/bin/portico \
  https://github.com/kloudkit/portico/releases/latest/download/portico-amd64
chmod +x /usr/local/bin/portico
```

Use `portico-arm64` on arm64. With Go 1.27 or newer you can also
`go install github.com/kloudkit/portico@latest`.

## Quick start

Let Portico start the frontend and route to an API you already run:

```sh
portico up \
  --run 'web=npm run dev -- --port {port}' --route /=web \
  --route /api=3200
```

Open `http://localhost:4400` (or the forwarded URL for port `4400`). Calls to
`/api/users` reach the API as `/users`.

## Configuration

For autocompletion and checks in your editor, start `portico.yaml` with:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/kloudkit/portico/main/portico.schema.json
```

Portico looks for `portico.yaml` or `.portico.yaml` in the current directory and
its parents, up to the git root. Use `-c` to point elsewhere.

```yaml
proxy:
  port: 4400

services:
  web:
    run: npm run dev -- --port {port}
    route: /
  api:
    port: 3200
    route: /api
  config-server:
    image: registry.example.com/config-server
    tag: "2.4"
    port: 8080
    route: /config
  stack:
    compose: ./compose.yaml
    route: /stack
```

Check what that resolves to without starting anything:

```sh
portico routes
```

### Proxy

| Key      | Default         | Description                                   |
| -------- | --------------- | --------------------------------------------- |
| `port`   | `4400`          | Port Portico listens on.                      |
| `host`   | `0.0.0.0`       | Address Portico listens on.                   |
| `base`   | `/`             | Prefix mounted in front of routes.            |
| `engine` | `builtin`       | `builtin` or `nginx`.                         |
| `image`  | `nginx`         | Image for the nginx engine.                   |
| `tag`    | `1.30.5-alpine` | Tag for the nginx engine.                     |
| `cors`   | `["*"]`         | Origins allowed to call Portico cross-origin. |

### Services

A service is one of:

- **`run`:** a shell command Portico starts. `{port}` is replaced with the
  service's port, which is also set as `PORT`. Leave `port` out and Portico
  picks a free one.
- **`image`:** a container image Portico runs with Docker or Podman. `port` is
  the container port; Portico publishes it on a free host port.
- **`compose`:** a compose file Portico brings up, and stops on exit.
- **only `port`:** something you start yourself, such as an API under a
  debugger. Portico routes to it and reports when it's up or down.

A `run` service that serves nothing, such as a queue worker, takes
`port: none`: Portico runs it but doesn't route to it. Give a `run` service
`dir: frontend` to start it in that directory, relative to `portico.yaml`.

Service names use lowercase letters, digits, `_`, `.` and `-`; `portico` and
`nginx` are reserved.

| Key       | Description                                                          |
| --------- | -------------------------------------------------------------------- |
| `run`     | Shell command to start.                                              |
| `dir`     | Directory to run it in *(default: next to `portico.yaml`)*.          |
| `image`   | Container image to run.                                              |
| `tag`     | Image tag *(default: `latest`)*.                                     |
| `port`    | Port the service listens on.                                         |
| `route`   | Path to mount it at *(default: `/<name>`)*.                          |
| `strip`   | Remove the route prefix before forwarding *(default: `true`)*.       |
| `host`    | `preserve` forwards the browser's `Host` instead of `localhost:<port>`. |
| `compose` | Compose file; every service with published ports gets a route.      |
| `service` | With `compose`, route a single compose service at `route`.          |
| `routes`  | With `compose`, override the path of individual compose services.   |
| `quiet`   | Keep its output off the screen.                                      |
| `log`     | `false` writes no log file for it.                                   |

A compose service with several published ports is mounted at
`<route>/<port>` for each one. With `service`, set `port` to pick one of them.

### Engines

The `builtin` engine is Portico itself and needs nothing else. Set
`engine: nginx` to put nginx in front instead, for parity with an nginx-based
production setup. Portico generates the nginx config from the same routes, so
both engines forward the same paths and headers. nginx runs from your `PATH`
if it's installed, otherwise in a container from `image:tag`. Requests nginx
can't serve (no route, a service that's down) fall back to Portico, so the JSON
errors are the same too.

### Output

Every service's output is prefixed with its name, and also written to
`.portico/logs/<name>.log` next to the config. A log is moved aside to
`<name>.log.1` once it reaches 10 MB, so each service keeps at most about 20 MB. A service that exits is left
down, so its error stays on screen; `portico restart <name>` brings it back.

- **`--hide api,web`:** keep these off the screen.
- **`--timestamps`:** add the time to every line.
- **`--raw`:** no prefixes or colors, for piping.
- **`--no-logs`:** write no log files; `log: false` does it for one service.

Pass service names to start only those: `portico up web api`.

### From another terminal

While `portico up` runs, these work from anywhere under the same config:

```sh
portico status              # every service, its state, routes and public URL
portico logs api -f         # follow one service (or all, without a name)
portico restart api         # stop and start a service
portico down                # stop everything
```

If Portico itself was killed and left services running, `portico down` stops
them too.

### CORS

The browser only talks to Portico, so your own frontend never needs CORS. It
matters when something on another origin calls Portico, such as a frontend on
another workstation. Portico answers those itself: preflights get a `204`, and
the app's own `Access-Control-*` headers are dropped so there's one set.

Any origin is allowed by default, which on a laptop includes any website you
visit. List origins to allow only those (a leading `*.` matches any subdomain),
or set `cors: []` to leave CORS to your app:

```yaml
proxy:
  cors:
    - https://*.cloudworkstations.dev
```

### Host and origin checks

Upstreams see `Host: localhost:<port>` so dev-server host checks pass, and the
real host arrives in `X-Forwarded-Host` and `X-Forwarded-Proto`. Apps that
compare `Origin` to the host (Django's CSRF check, for one) need to trust those
headers, e.g. `USE_X_FORWARDED_HOST` and `SECURE_PROXY_SSL_HEADER` in Django.
Set `host: preserve` on a service that needs the original `Host` instead.

### Flags

Flags override the file, and work without one:

```sh
portico up --port 8000 --route /=5173 --route /api=3200
```

- **`--run name=command`:** start a command as a service.
- **`--route /path=port`:** route a path to a port.
- **`--route /path=name`:** route a path to a service from the file.
- **`--port`, `--host`, `--base`:** override the proxy settings.

## Status

Portico is early, and not tried widely yet. Expect rough edges.

## License

Released under the [**MIT License**](LICENSE), **except** the [`brand/`](brand)
directory.

The Portico and KloudKIT logos, icons and other brand assets are proprietary and
**all rights reserved**; see [`brand/LICENSE`](brand/LICENSE).
