# Workshell

Self-hosted error tracking, logging, session replay-style timelines, performance tracing, anti-exploit logging and analytics for Roblox games — on your own server.

- **Issues** — server and client errors grouped by cause, with stack traces linked to your source files (via Rojo sourcemaps), breadcrumbs, affected players, release tracking and regression detection. Assign teammates, discuss in comment threads with @mentions, and open linked GitHub issues.
- **Logs** — all server output plus structured logs, with search, filters and a live stream.
- **Sessions & players** — every play session from join to leave on one timeline: events, logs, errors (with breadcrumbs), traces, purchases, exploit detections and client FPS/ping/memory. Player profiles show every session.
- **Performance** — `Workshell.trace(...)` spans with p50/p95/p99, client frame rate and ping by platform, and server health.
- **Releases** — every version your servers run, with its git commit, the issues it introduced, and its error-free session rate.
- **Security** — exploit and anti-cheat detections from `Workshell.flag(...)` and protected remotes, plus automatic flood detection. Optional pseudonymous (HMAC-hashed) player ids.
- **Analytics** — CCU, DAU (new vs returning), session length, D1–D30 retention, platforms, countries, Robux revenue, custom events and funnels.
- **Alerts** — rules for new issues, regressions, error spikes, exploit detections, player-count drops and low server FPS, delivered to Discord or any webhook.
- **Team** — profiles with avatars, an inbox for mentions and assignments, themes (including Catppuccin, Rosé Pine, Nord and Gruvbox) and an installable PWA that works on phones.

Workshell is one Go binary with the dashboard built in, backed by SQLite. Redis is optional.

## Quick start

### Docker

```sh
docker compose up -d     # edit WORKSHELL_PUBLIC_URL in docker-compose.yml first
```

### From source

Requires Go 1.26+, Node 22+ and pnpm.

```sh
make build               # builds the dashboard and bin/workshell
./bin/workshell          # listens on :8080 (all interfaces), stores data in ./data
```

Open `http://localhost:8080` — or `http://<your-machine's-LAN-IP>:8080` from your phone — create the admin account, then a project.

> **Roblox game servers can't reach `localhost`.** For a live game, Workshell must be reachable on a public URL, ideally HTTPS behind a reverse proxy (Caddy, nginx, Cloudflare Tunnel). For quick testing, a tunnel such as `cloudflared tunnel --url http://localhost:8080` works.

Try it with fake data: `make sim KEY=wsk_...` sends about a week of simulated traffic (raise the project's ingest limit under Settings → Security first for a fast backfill).

## Installing the SDK

1. Get the `Workshell` module into **ReplicatedStorage**: download `Workshell.rbxmx` from your project's **Settings → SDK setup**, or sync `sdk/` with Rojo / Wally.
2. Enable **Game Settings → Security → Allow HTTP Requests**.
3. Start it from a **server** Script:

```lua
local Workshell = require(game.ReplicatedStorage.Workshell)

Workshell.init({
	url = "https://workshell.example.com",
	key = "wsk_...",               -- or HttpService:GetSecret("workshell_key")
	signingSecret = "wss_...",     -- optional, see Security below
})
```

4. Optional — require it from a LocalScript too, for client errors, device type and client FPS/ping/memory:

```lua
require(game.ReplicatedStorage.Workshell)
```

Captured automatically: uncaught server and client errors, server output, player sessions (with country, platform and account metadata), server health every 30 s, client performance every 30 s, game pass purchases, and a final flush on shutdown.

### API

```lua
-- Logs
Workshell.info("Round started", { map = "Desert" })
Workshell.warn("DataStore budget low", { budget = 3 }, player)

-- Errors
Workshell.captureError(err, { player = player, tags = { store = "PlayerData" }, context = { attempt = 3 } })
local ok, result = Workshell.try(loadData, player)        -- pcall that reports failures
Workshell.setContext("round", { map = "Desert" })          -- attached to every error
Workshell.addBreadcrumb("Opened shop", "ui")

-- Sessions & analytics
Workshell.setUser(player, { level = 12, guild = "Red" })   -- session metadata
Workshell.track("level_up", { level = 5 }, player)
Workshell.trackPurchase(player, receipt.CurrencySpent, { productId = receipt.ProductId })

-- Performance
local data = Workshell.trace("datastore.load", function() return store:GetAsync(key) end, player)
local span = Workshell.startSpan("matchmaking.find", { player = player })
span:setAttribute("region", "eu"); span:finish()

-- Security
Workshell.flag(player, "speed", { speed = 90, max = 32 }, { severity = "high", action = "kicked" })
Workshell.protect(remotes.BuyItem, function(player, itemId) ... end, {
	rate = 5,                                               -- calls/second per player
	validate = function(player, itemId) return typeof(itemId) == "string" end,
})

-- Metrics
Workshell.increment("shop.opened")
Workshell.gauge("matchmaking.queue", #queue)
```

Clients can call `track`, `captureError`, `try` and `addBreadcrumb`. Client data is relayed through a rate-limited RemoteEvent; client events are marked `$client = true`, clients can't send `$`-prefixed events such as `$purchase`, and detections can only come from the server.

## Releases and Rojo

Run `workshell release` in CI before `rojo build`:

```sh
WORKSHELL_URL=https://workshell.example.com WORKSHELL_KEY=wsk_... \
  workshell release -project default.project.json -out src/shared/WorkshellRelease.json
```

It reads the git commit, branch, message and author; writes `WorkshellRelease.json` (Rojo syncs JSON files as ModuleScripts, and the SDK picks it up automatically, so every error and session is tagged with the commit); runs `rojo sourcemap` and uploads it. Stack frames like `ServerScriptService.Combat.Weapon:42` then show as `src/server/Combat/Weapon.server.luau:42`, linked to GitHub at that exact commit when a repository is connected (client paths like `Players.x.PlayerGui.Shop` resolve too). The Releases page shows what each version introduced.

## Security

- **Signed requests.** Generate a signing secret under Settings → Security and pass it as `signingSecret`; each batch is signed with HMAC-SHA256 over a timestamp and the body. With "require signed requests" on, a leaked ingest key alone can't be used to forge or replay data.
- **Exploiter-proofing.** Exploiters can only reach Workshell through your game's clients. Client-originated data is capped per player (configurable); floods are dropped and recorded as `client_flood` detections. Server logs are capped per server, and each project has an ingest rate limit.
- **Safe rendering.** All user-supplied text is rendered as text (comments use a VNode-based Markdown subset, never HTML), a strict Content-Security-Policy forbids inline and third-party scripts, control/bidi characters are stripped at ingest, and alert text can't ping `@everyone` on Discord.
- **Secrets at rest.** Webhook URLs, GitHub tokens, signing and hashing secrets are encrypted with AES-256-GCM under `WORKSHELL_SECRET_KEY` (auto-generated into the data directory if unset).
- **Pseudonymous players.** Optionally store an HMAC of each Roblox user id instead of the id; lookups by Roblox id still work.
- Login throttling, an audit log of sensitive changes, and SameSite/HttpOnly session cookies.

## Configuration

| Variable | Default | |
| --- | --- | --- |
| `WORKSHELL_ADDR` | `:8080` | Listen address (all interfaces by default) |
| `WORKSHELL_DATA_DIR` | `./data` | SQLite database and generated secret key. Back this up. |
| `WORKSHELL_PUBLIC_URL` | — | Public URL, for setup snippets and alert links |
| `WORKSHELL_SECRET_KEY` | generated | 32-byte hex/base64 key encrypting stored secrets |
| `WORKSHELL_REDIS_URL` | — | e.g. `redis://localhost:6379/0`: durable ingest queue + live streaming across instances |
| `WORKSHELL_SECURE_COOKIES` | `false` | Set `true` when served over HTTPS |
| `WORKSHELL_TRUST_PROXY` | `false` | Trust `X-Forwarded-*` from a reverse proxy |
| `WORKSHELL_MAX_INGEST_BYTES` | `8388608` | Max (decompressed) ingest request size |
| `WORKSHELL_SESSION_DAYS` | `30` | Dashboard login lifetime |

Retention is per project (logs/traces/health 14 days and errors/events/sessions/detections 90 days by default).

## How it works

```
Roblox server ──(SDK: gzip JSON, HMAC-signed)──▶ POST /api/v1/ingest ──▶ queue (memory | Redis Stream)
Roblox clients ─(RemoteEvent, rate-limited)───▶ server SDK                        │
                                                                           writer ─┴─▶ SQLite (WAL)
Browser / PWA ──▶ Vue dashboard ──▶ /api/projects/…  ◀── live updates (SSE, memory | Redis pub/sub)
```

- `cmd/workshell` — server and `workshell release` CLI. `internal/server` — ingest, grouping, collaboration, sessions, releases, performance, security, alerts. `internal/pipeline` — queue and live broker. `internal/secure` — encryption and HMAC. `internal/db` — schema and migrations.
- `web/` — Vue 3 + Vite + Tailwind dashboard (PWA), embedded into the binary.
- `sdk/` — the Luau SDK; `sdk/test` runs it against Roblox API mocks under the `luau` CLI.
- `cmd/workshell-sim` — traffic simulator.

A simulated week (≈680k items) ingests in a few seconds; dashboard queries return in well under a second.

### Ingest protocol

`POST /api/v1/ingest` with `X-Workshell-Key` (plus `X-Workshell-Timestamp` and `X-Workshell-Signature: v1=<hex hmac>` when signing). Returns `202`. Item types: `error`, `log`, `event`, `session_start`, `session_update`, `session_end`, `heartbeat`, `metric`, `span`, `detection`, `client_perf`, `server_close`; see `internal/server/ingest.go` for fields. Unknown types are ignored, so older servers accept newer SDKs.

## Development

```sh
make dev        # server on :8080
make dev-web    # dashboard with hot reload on :5173 — also on your LAN for testing on a phone
make test       # Go tests, dashboard type check, SDK tests (needs `luau`)
```

## License

[PolyForm Shield 1.0.0](LICENSE). You can use, modify and self-host Workshell for anything, including commercially, except to provide a product that competes with Workshell.

Required Notice: Copyright 2026 Darwin
