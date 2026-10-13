# Changelog

## [0.1.0] — Unreleased

### Added

- 🚪 Serve every local service through one proxy port with `portico up`
- 🧭 Print the resolved route table with `portico routes`
- 🐳 Discover routes from the published ports of a compose file
- 🔌 Proxy websockets and server-sent events without buffering or idle timeouts
- 🏷️ Forward `X-Forwarded-Host`, `-Proto` and `-Prefix`, and keep the ones set by the gateway in front
- 🧾 Answer with JSON when a service is down, unreachable or not routed
- 🔍 Find `portico.yaml` in parent directories up to the git root
- 🚩 Run without a config file using `--route`, `--port`, `--host` and `--base`
- 📦 Publish linux amd64 and arm64 binaries on every `v*` tag
- 🌐 Answer CORS for any origin by default, or only for those listed in `proxy.cors`
- 🏠 Forward the browser's `Host` for services with `host: preserve`
