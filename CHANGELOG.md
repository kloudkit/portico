# Changelog

## [0.1.1] — Unreleased

### Added

- 📁 Start a `run` service in another directory with `dir`
- 🔗 Show the public URL Portico is reached on, in its output and in `portico status`
- ✳️ Allow CORS origins with a leading `*.` that matches any subdomain
- 🧩 Ship a JSON schema for `portico.yaml`, for autocompletion in editors
- 🔇 Skip writing log files with `log: false` or `--no-logs`

### Changed

- 🎨 Match the CLI colors to the logo and use the tagline in `--help`
- 🔙 Retract v0.1.0 for `go install`

### Fixed

- 🔌 Stop every service when the terminal closes, instead of leaving them running
- ⏱️ Route to a service as soon as it's up, not after the next readiness check
- 🏷️ Reject service names that can't be used as file or container names, and the reserved `portico` and `nginx`
- 🐳 Reject a compose `port` without `service`, or one the compose service doesn't publish
- 📊 Keep progress bars and very long lines from growing memory
- 🗂️ Rotate each log at 10 MB instead of letting it grow without limit
- 🔖 Report the real version from `go install` builds
## [0.1.0] — 2026-10-13

### Added

- 🚪 Serve every local service through one proxy port with `portico up`
- 🏃 Start `run` commands, `image` containers and `compose` files, and stop them all on `Ctrl-C`
- 🎲 Give `run` services without a port a free one, through `{port}` and `PORT`
- 👷 Run background workers without a route using `port: none`
- 📜 Stream every service's output under its name, and write it to `.portico/logs`
- 💥 Report a crashed service's exit code and last lines in its `503`
- 🙈 Keep noisy services off the screen with `quiet` or `--hide`, and add `--timestamps` and `--raw`
- 🧭 Print the resolved route table with `portico routes`
- 🐳 Discover routes from the published ports of a compose file
- 🔌 Proxy websockets and server-sent events without buffering or idle timeouts
- 🏷️ Forward `X-Forwarded-Host`, `-Proto` and `-Prefix`, and keep the ones set by the gateway in front
- 🧾 Answer with JSON when a service is down, unreachable or not routed
- 🔍 Find `portico.yaml` in parent directories up to the git root
- 🚩 Run without a config file using `--run`, `--route`, `--port`, `--host` and `--base`
- 📦 Publish linux amd64 and arm64 binaries on every `v*` tag
- 🌐 Answer CORS for any origin by default, or only for those listed in `proxy.cors`
- 🟩 Put nginx in front with `engine: nginx`, from `PATH` or a container, with the same routing and errors as the builtin engine
- 🩺 Check on a running Portico from another terminal with `status`, `logs`, `restart` and `down`
- 🧹 Clean up services left running by a crashed Portico with `portico down`
- 🏠 Forward the browser's `Host` for services with `host: preserve`

[0.1.0]: https://github.com/kloudkit/portico/releases/tag/v0.1.0
