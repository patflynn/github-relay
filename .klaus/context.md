# Repository context

## Purpose
`github-relay` is a small Go HTTP server, shipped as a NixOS module, that receives GitHub webhooks (exposed publicly via Tailscale Funnel) and dispatches matching events to local consumers: systemd units, HTTP endpoints, or shell commands. It is used to drive things like NixOS auto-rebuilds and feeding events to other local services without port-forwarding or a public DNS record (see `README.md`).

## Tech stack
- Go 1.24 (`go.mod`). Module path `github.com/patflynn/github-relay`.
- Standard library only — `go.mod` declares no `require` directives, and `flake.nix` sets `vendorHash = null` (`flake.nix:23`).
- Logging via `log/slog` JSON handler (`cmd/relay/main.go:25`).
- HMAC-SHA256 webhook signature validation via `crypto/hmac` + `crypto/sha256` (`internal/webhook/webhook.go`).
- Build: Nix flake using `pkgs.buildGoModule` (`flake.nix:19`). Plain `go build` also works.
- Deployment: NixOS module in `module.nix`, exported as `nixosModules.default` (`flake.nix:33`).

## Entry points
- `cmd/relay/main.go` — single binary `github-relay`. Parses `-config` flag (default `/etc/github-relay/config.json`), serves `POST /hooks/github`, validates signatures, and dispatches matched events. Graceful shutdown on SIGINT/SIGTERM.
- `module.nix` — NixOS module exposing `services.github-relay.*` options; generates the JSON config file, defines the systemd service, and (optionally) a `github-relay-funnel` oneshot that runs `tailscale funnel`.
- `flake.nix` — Nix flake providing the `default` package (the Go binary) and `nixosModules.default`.

## Layout
- `cmd/relay/` — main package; HTTP server, request handler, signal handling.
- `internal/config/` — `Config`/`Consumer` structs, JSON loading, `Match`, `ExtractRepo`, `ExtractBranch`.
- `internal/dispatch/` — `Dispatch` plus `systemd`/`http`/`command` dispatcher implementations.
- `internal/webhook/` — `ValidateSignature` (HMAC-SHA256 against secret read from a file path).
- `module.nix` — NixOS module options + systemd unit definitions.
- `flake.nix` / `flake.lock` — Nix build inputs.
- `README.md` — user-facing docs and design overview.

## Build, test, run
- Compile: `go build ./...`
- Tests: `go test ./...` (each `internal/*` package and `cmd/relay` has a `_test.go`).
- Nix build: `nix build` (produces `result/bin/github-relay`; renamed from `relay` via `postInstall` in `flake.nix:26`).
- Run locally: `./github-relay -config /path/to/config.json`.

There is no Makefile and no `scripts/` directory.

## Conventions
- Errors are wrapped with `%w` (e.g. `internal/config/config.go:31`, `internal/dispatch/dispatch.go:44`). Follow this when adding new error returns.
- All logging goes through `slog` with structured key/value pairs (`cmd/relay/main.go`, `internal/dispatch/dispatch.go`). No `fmt.Println` / `log.Printf`.
- Tests live next to the code in the same package (`internal/<pkg>/<pkg>_test.go`).
- Dispatcher failures are logged but the handler still returns `200 OK` to GitHub to avoid GitHub's retry storms (`cmd/relay/main.go:124-132`, also called out in `README.md` Reliability section).
- The webhook secret is **always** read from a file path (`webhook_secret_file`), never inlined in config. `ValidateSignature` re-reads it per request and trims whitespace (`internal/webhook/webhook.go:24-30`).
- Module options for new consumer fields should round-trip through `consumerToJSON` in `module.nix` so they reach the Go config.

## Gotchas
- HTTP route is `POST /hooks/github` exactly (`cmd/relay/main.go:37`). Tailscale Funnel previously stripped paths, causing 404s — fixed in commit `be82c7f`. If you change the path, re-verify Funnel routing.
- `dispatchSystemd` shells out to `systemctl start <unit>` (`internal/dispatch/dispatch.go:39`), so the relay process must have permission to start the configured unit. Because the module runs the service under `DynamicUser = true` (`module.nix:108`), `systemctl start` will fail with a permission error out-of-the-box — either configure a Polkit rule allowing the dynamic user to control the unit, or override `DynamicUser` to run as root (or another privileged user).
- `dispatchCommand` invokes `sh -c <command>` (`internal/dispatch/dispatch.go:90`); the `command` string is **not** sandboxed by the Go code, but it inherits the relay's systemd sandbox (`DynamicUser = true`, `ProtectSystem = "strict"`, `ProtectHome = true`, `ReadOnlyPaths = [ "/" ]`, `PrivateTmp = true`; `module.nix:108-114`). Treat consumer config as trusted, and expect filesystem writes to fail unless `ReadWritePaths` is extended.
- `Match` is case-insensitive on repo (`strings.EqualFold` at `internal/config/config.go:68`) but case-sensitive on events and branches.
- `ExtractBranch` only handles `push`, `pull_request`, and `pull_request_review` (`internal/config/config.go:105-123`); other events get an empty branch, so a consumer with a non-empty `branches` filter will never match them.
- Funnel oneshot uses `tailscale funnel --bg --yes <port>` and `ExecStop = ... funnel <port> off` (`module.nix:133-134`). Toggling `funnel.enable` off does **not** automatically remove an existing Funnel rule unless the unit stops cleanly.

## External dependencies
- **GitHub** — sends webhooks signed with `X-Hub-Signature-256`. Retries failed deliveries for up to 3 days (per `README.md`).
- **Tailscale Funnel** (optional, `services.github-relay.funnel.enable`) — public HTTPS ingress; uses `services.tailscale.package`’s `tailscale` binary and requires `services.tailscale.permitCertUid` (`module.nix:121`).
- **systemd** — required at runtime; `dispatchSystemd` shells out to `systemctl`, and the NixOS module installs a systemd service.
- Consumer-side HTTP endpoints — whatever URLs are configured under `consumers.*.url` receive `POST` requests with the raw webhook body, `Content-Type: application/json`, plus `X-GitHub-Event` / `X-GitHub-Delivery` headers (`internal/dispatch/dispatch.go:64-68`).
