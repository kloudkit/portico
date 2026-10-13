# Changelog

## [0.1.0] — Unreleased

### Added

- 🚪 Serve every local service through one proxy port with `portico up`
- 🏃 Start `run` commands, `image` containers and `compose` files, and stop them all on `Ctrl-C`
- 🎲 Give `run` services without a port a free one, through `{port}` and `PORT`
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
