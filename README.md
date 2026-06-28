# scootship

**English** | [简体中文](README.zh-CN.md)

**A management center for a fleet of [Scoot](https://github.com/jamiesun/scoot) agents.**

Scoot is a local-first, single-binary AI agent that runs on your machines and keeps every
action in a JSONL audit trail. Today each Scoot install is an island. `scootship` is the
**management center** that the Scoot project's [`docs/EDGE.md`](https://github.com/jamiesun/scoot/blob/main/docs/EDGE.md)
points to: a single Go binary that ingests append-only fleet telemetry over the `scoot-edge`
protocol and serves an embedded admin dashboard so you can observe the whole fleet from one place.

> **Status: Phase 1 — observation + framework (pre-1.0).**
> The center ingests `status` heartbeats and `audit_batch` log shipping and renders the fleet.
> Task **dispatch/orchestration (EDGE.md E2)** is intentionally not built yet — the lease
> endpoint exists but dispatches nothing. See [`docs/roadmap.md`](docs/roadmap.md) for the
> project shape, boundaries, and direction.

## Why a separate companion

`scoot-edge` is, by design, an optional companion that **dials out** to a center; the edge
never opens a listener. That makes **the center the only inbound trusted surface in the
fleet**, so scootship is built defensively:

- Every node endpoint is authenticated by a **per-node bearer token**, and the dashboard by a
  **login session** (form login + HttpOnly cookie, not the browser's basic-auth popup).
- The dashboard login is **rate-limited per source IP**: repeated failures lock that IP out for
  a cooldown (with a `Retry-After`), so the center's sole inbound surface resists brute force.
- Every response carries defensive headers (a strict `Content-Security-Policy` with no inline
  scripts, plus `X-Frame-Options`, `X-Content-Type-Options`, and `Referrer-Policy`).
- Telemetry is **append-only** and is never reflected back into a node's local state.
- The center **can never raise a node's local policy ceiling** — it isn't a permission console.

One honest caveat from EDGE.md carries over: once a node opts in to audit shipping, the
bodies it ships (file contents, command output) live at the center. Treat the center, and its
dashboard login, as sensitive.

```text
        Operators (browser · login session)
                 |
                 v
   +-------------------------------------------+
   |        scootship (single Go binary)        |
   |  embedded dashboard (embed.FS)             |
   |  POST /telemetry   GET /jobs/lease (E2)    |
   |  per-node token auth · append-only store   |
   +----------------------^---------------------+
                          |  edge dials OUT (HTTPS + per-node token)
        +-----------------+-----------------+
        |                 |                 |
    scoot-edge        scoot-edge        scoot-edge   (optional, not built yet)
        |                 |                 |
      scoot             scoot             scoot
```

## Quick start

Requires Go 1.26+. No external dependencies, no Node toolchain, no database to install — the
dashboard and storage are built in.

```sh
# Terminal 1 — run the center in dev mode (dashboard open, demo token n-dev=dev-token seeded)
SCOOTSHIP_DEV=1 go run ./cmd/scootship serve

# Terminal 2 — the real scoot-edge does not exist yet (EDGE.md is E0/design-only),
# so drive the full heartbeat -> ingest -> dashboard path with the built-in simulator:
go run ./cmd/scootship mock-edge -ship-audit
```

Open <http://localhost:8080>. You'll be redirected to a sign-in page — in dev mode log in with
`admin` / `admin`. After signing in you get the dashboard shell with a **collapsible left
sidebar**: the `n-dev` node goes **online**, with its policy ceiling, derived audit counts,
capability labels, and (because `-ship-audit` is on) a few ingested audit events on the node
detail page.

Or use the Makefile:

```sh
make run        # center in dev mode
make mock-edge  # simulated node
make ci         # fmt-check + vet + test + build
```

## Configuration

`serve` is configured from the environment (secrets never come from committed config):

| Variable | Default | Meaning |
| --- | --- | --- |
| `SCOOTSHIP_ADDR` | `:8080` | Listen address. |
| `SCOOTSHIP_TLS_CERT` / `SCOOTSHIP_TLS_KEY` | _(unset)_ | PEM paths. EDGE.md mandates HTTPS in production; without these the center serves plain HTTP and warns loudly (dev only / terminate TLS at a proxy). |
| `SCOOTSHIP_DATA_DIR` | `./data` | Append-only store directory. |
| `SCOOTSHIP_ADMIN_USER` | `admin` | Dashboard login user. |
| `SCOOTSHIP_ADMIN_PASSWORD` | _(unset)_ | Dashboard login password. Required unless `SCOOTSHIP_DEV=1` (which enables a default `admin`/`admin` login). |
| `SCOOTSHIP_NODE_TOKENS_FILE` | _(unset)_ | JSON file: `{"n-7a3":"secret", ...}` (mode `0600`). |
| `SCOOTSHIP_NODE_TOKENS` | _(unset)_ | Inline `n-7a3=secret,n-8b4=secret2`. |
| `SCOOTSHIP_DEV` | _(unset)_ | `=1` seeds the demo node token and a default `admin`/`admin` dashboard login (insecure; local use). |
| `SCOOTSHIP_STALE_SECONDS` | `90` | A node is shown "stale" after this much silence. |
| `SCOOTSHIP_LOGIN_MAX_FAILS` | `5` | Failed dashboard logins from one source IP before it is locked out. |
| `SCOOTSHIP_LOGIN_WINDOW_SECONDS` | `900` | Sliding window over which failures are counted. |
| `SCOOTSHIP_LOGIN_LOCKOUT_SECONDS` | `900` | How long a tripped IP stays locked out. |
| `SCOOTSHIP_TRUSTED_PROXIES` | _(unset)_ | Comma-separated CIDRs/IPs of reverse proxies whose `X-Forwarded-For` may be trusted to attribute the real client IP. Unset means trust only the raw connection (spoofed `XFF` is ignored). |

`mock-edge` is a dev/test client configured by flags: `-center`, `-node`, `-token`,
`-interval`, `-ship-audit`.

## Protocol alignment

scootship implements the **center side** of the frozen `scoot-edge` v1 contract. The wire
shapes live in [`internal/protocol`](internal/protocol/protocol.go) and mirror EDGE.md exactly:

- Envelope `{"v":1,"type":"status|audit_batch|job|job_event","node_id":"...","sent_ts":...,"body":{}}`.
- **E1 (implemented):** `POST /telemetry` accepts `status` and `audit_batch` (and forward-compatibly
  `job_event`). Audit ingest is idempotent on the `{file_gen, byte_to}` cursor and acks the durably
  stored cursor so the edge only advances after a durable ack.
- **E2 (stubbed):** `GET /jobs/lease` authenticates and validates the node but dispatches nothing
  in Phase 1.

scootship talks only this contract; it does not depend on any Scoot internal.

## Project layout

| Path | Responsibility |
| --- | --- |
| `cmd/scootship` | CLI entrypoint: `serve`, `mock-edge`, `version`. |
| `internal/protocol` | The frozen scoot-edge v1 wire contract (envelope, bodies, cursor). |
| `internal/store` | Append-only JSONL fleet store with idempotent audit ingest + replay. |
| `internal/tokens` | Per-node bearer-token registry (the center's own auth surface). |
| `internal/loginguard` | Per-source-IP brute-force throttle for dashboard logins (failure window + lockout). |
| `internal/config` | Environment-driven configuration. |
| `internal/center` | HTTP server, bearer + login-session auth, telemetry ingest, lease stub, dashboard. |
| `internal/web` | Embedded dashboard templates and static assets (`embed.FS`). |
| `internal/mockedge` | Simulated scoot-edge node (stands in for the not-yet-built edge). |
| `docs/roadmap.md` | Project shape, non-goals, and direction. |

## Contributing

Read [`AGENT.md`](AGENT.md) (engineering handbook) and [`docs/roadmap.md`](docs/roadmap.md)
(intent and hard boundaries) before making changes. Run `make ci` before pushing.

## License

[MIT](LICENSE) — matching the Scoot ecosystem.
