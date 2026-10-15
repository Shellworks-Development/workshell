# Repository Guidelines

## Project Structure & Module Organization

Workshell is a self-hosted Roblox telemetry service with a Go server, Vue dashboard, and Luau SDK.

- `cmd/workshell/`: server entry point and release CLI; `cmd/workshell-sim/`: traffic simulator.
- `internal/server/`: HTTP APIs and feature logic; `internal/pipeline/`: queues and live streaming; `internal/telemetry/`: optional ClickHouse storage; `internal/db/`: SQLite schema and migrations; `internal/config/` and `internal/secure/`: configuration and cryptography.
- `web/src/`: Vue views, reusable components, TypeScript helpers in `lib/`, and styles. `web/public/` holds static assets.
- `sdk/src/`: Roblox SDK modules; `sdk/test/`: Roblox mocks and SDK assertions.
- `docs/`: screenshots and icons. `assets.go` embeds `web/dist/` and SDK sources into the server binary.

## Build, Test, and Development Commands

Use Go 1.26+, Node.js 22+ (CI uses 24), and pnpm pinned by `web/package.json`. SDK tests require Python 3 and `luau`.

- `make build`: build the dashboard and compile `bin/workshell`.
- `make web`: install dependencies with the frozen lockfile and build embedded dashboard assets; run before Go builds on a fresh checkout.
- `make dev`: run the debug server on port 8080.
- `make dev-web`: run Vite on port 5173 with `/api` proxied to the server.
- `make test`: run Go vet/tests, Vue type checking, and SDK tests.
- `make sdk`: build `bin/Workshell.rbxm` using Rojo.
- `make sim KEY=wsk_...`: populate a local instance with simulated telemetry.

## Coding Style & Naming Conventions

Format Go with `gofmt`; use idiomatic exported names and lowercase package names. Follow existing Vue/TypeScript style: two-space indentation, single quotes, and no semicolons. Use PascalCase Vue filenames, such as `IssueView.vue`, and camelCase helpers. Keep TypeScript strict checks passing. Luau modules use PascalCase filenames and tab indentation. No dedicated frontend formatter or linter is configured.

## Testing Guidelines

Place Go tests beside implementation as `*_test.go`, using Go's `testing` package and `Test...` functions. Reuse HTTP test helpers and temporary databases. Extend `sdk/test/spec.luau` for SDK behavior. Add regression coverage for changes; no coverage threshold is configured. Run `make web` then `make test` before submitting.

## Commit & Pull Request Guidelines

Follow Git history: `feat:`, `fix:`, `docs:`, or `ci:`, with optional scopes such as `fix(release):`. Use imperative subjects. PRs should explain behavior changes, link relevant issues, and record validation results. Include screenshots for dashboard changes and document configuration or API changes.

## Security & Configuration

Configure runtime settings through `WORKSHELL_*` environment variables documented in `README.md`. Keep credentials, generated keys, local databases, and build outputs out of commits. Follow `SECURITY.md` for vulnerability reports.
