# scootship

![scootship — fleet command center](docs/assets/hero.png)

**English** | [简体中文](docs/README.zh-CN.md)

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
dashboard login, as sensitive. The dashboard/API keep a bounded recent audit window per node and
mark an explicit `audit_gap` when that window trims; accepted audit batches are still persisted
append-only to the center JSONL log.

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
sidebar** (Fleet, Tokens, Operators + a Settings menu with Account), plus top-right sign-out:
the `n-dev` node goes **online**, with its policy ceiling, derived audit counts, capability
labels, and (because `-ship-audit` is on) a few ingested audit events on the node detail page.

Or use the Makefile:

```sh
make run         # center in dev mode
make mock-edge   # simulated node
make docs        # build bilingual mdBook docs into ./book (requires mdBook)
make docs-serve  # preview the English mdBook root locally
make ci          # fmt-check + vet + test + build
```

## CI, release, and project skills

- CI runs on pushes, pull requests, and manual dispatch via `.github/workflows/ci.yml`:
  `gofmt`, `go vet`, `go test`, `go build`, a CLI version smoke, and release-target
  cross-compile smoke checks.
- The docs workflow in `.github/workflows/docs.yml` builds the bilingual mdBook on docs-related
  pushes, pull requests, and manual dispatch; pushes to `main` also deploy the generated `book/`
  site to GitHub Pages.
- Pushing a `vX.Y.Z` tag triggers `.github/workflows/release.yml`, which injects the tag
  into `internal/version.Version`, cross-compiles single-binary archives for Linux, macOS,
  and Windows, publishes checksums with the GitHub release, and publishes multi-arch Linux
  Docker images to GHCR as `ghcr.io/jamiesun/scootship:X.Y.Z` plus `latest` tags.
- Project-local agent skills live in `.agents/skills`: `auto-release` for controlled release
  orchestration and `project-audit` for scored whole-repo health reports.

## Configuration

`serve` is configured from the environment (secrets never come from committed config):

| Variable | Default | Meaning |
| --- | --- | --- |
| `SCOOTSHIP_ADDR` | `:8080` | Listen address. |
| `SCOOTSHIP_TLS_CERT` / `SCOOTSHIP_TLS_KEY` | _(unset)_ | PEM paths for direct HTTPS. EDGE.md mandates production-safe transport. Without direct TLS, startup fails unless `SCOOTSHIP_DEV=1` or `SCOOTSHIP_BEHIND_TLS_PROXY=1` is explicit. |
| `SCOOTSHIP_BEHIND_TLS_PROXY` | _(unset)_ | `=1` allows the center to listen with plain HTTP only when a trusted reverse proxy terminates TLS in front of it. Ensure the listener is not directly exposed. |
| `SCOOTSHIP_DATA_DIR` | `./data` | Append-only store directory. |
| `SCOOTSHIP_ADMIN_USER` | `admin` | Username used to bootstrap the first dashboard operator when the operator store is empty. |
| `SCOOTSHIP_ADMIN_PASSWORD` | _(unset)_ | Password used only to bootstrap the first dashboard operator. Required for first startup unless `SCOOTSHIP_DEV=1` (which bootstraps `admin`/`admin`). After bootstrap, operators are managed from the dashboard and stored in `SCOOTSHIP_DATA_DIR/operators.json`. |
| `SCOOTSHIP_NODE_TOKENS_FILE` | _(unset)_ | JSON file: `{"n-7a3":"secret", ...}`. Must be a regular private file with no executable, group, or world permissions (`0600` is the normal setting). |
| `SCOOTSHIP_NODE_TOKENS` | _(unset)_ | Inline `n-7a3=secret,n-8b4=secret2`. |
| `SCOOTSHIP_DEV` | _(unset)_ | `=1` seeds the demo node token and a default `admin`/`admin` dashboard login (insecure; local use). |
| `SCOOTSHIP_STALE_SECONDS` | `90` | A node is shown "stale" after this much silence. |
| `SCOOTSHIP_MAX_TELEMETRY_BYTES` | `8388608` | Maximum size of one `/telemetry` request body. |
| `SCOOTSHIP_AUDIT_RETENTION_EVENTS` | `1000` | Recent audit events retained per node for dashboard/API reads. Overflow creates an explicit `audit_gap`; accepted events remain in the append-only JSONL log. |
| `SCOOTSHIP_LOGIN_MAX_FAILS` | `5` | Failed dashboard logins from one source IP before it is locked out. |
| `SCOOTSHIP_LOGIN_WINDOW_SECONDS` | `900` | Sliding window over which failures are counted. |
| `SCOOTSHIP_LOGIN_LOCKOUT_SECONDS` | `900` | How long a tripped IP stays locked out. |
| `SCOOTSHIP_TRUSTED_PROXIES` | _(unset)_ | Comma-separated CIDRs/IPs of reverse proxies whose `X-Forwarded-For` may be trusted to attribute the real client IP. Unset means trust only the raw connection (spoofed `XFF` is ignored). |

`mock-edge` is a dev/test client configured by flags: `-center`, `-node`, `-token`,
`-interval`, `-ship-audit`. It sends the token as `Authorization: Bearer <token>` and the
envelope `node_id` must match the node assigned to that token.

The dashboard can also create, rotate, and revoke center-managed node tokens. The center generates
high-entropy bearer token secrets, displays them once after creation or rotation, never lists or
returns them from inventory APIs, and persists them in
`SCOOTSHIP_DATA_DIR/managed_node_tokens.json` with private `0600` permissions. Revocations are
stored there too, so an operator can revoke a token originally loaded from the env or private token
file without editing that original source. Configure a real `scoot-edge` with the same node id and
secret; in the current edge client flow that means setting `SCOOT_EDGE_TOKEN=<secret>` and using
`--node-id <node>` against the center's `/telemetry` endpoint.

For production setup, data-directory permissions, TLS proxy boundaries, backup, and restore, use the
operator runbook in [`docs/deployment.md`](docs/deployment.md).

## Protocol alignment

scootship implements the **center side** of the frozen `scoot-edge` v1 contract. The wire
shapes live in [`internal/protocol`](internal/protocol/protocol.go) and mirror EDGE.md exactly:

- Envelope `{"v":1,"type":"status|audit_batch|job|job_event","node_id":"...","sent_ts":...,"body":{}}`.
- **E1 (implemented):** `POST /telemetry` accepts `status` and `audit_batch` (and forward-compatibly
  `job_event`). Audit ingest is idempotent on the `{file_gen, byte_to}` cursor and acks the durably
  stored cursor so the edge only advances after a durable ack. Telemetry batches are decoded and
  validated before store mutation; invalid audit cursors, empty audit batches, and unknown audit
  event kinds are rejected. The recent audit window is bounded by `SCOOTSHIP_AUDIT_RETENTION_EVENTS`;
  trimming is visible as a center-side `audit_gap`. Node detail API/pages also group retained audit
  by `session_id` / `run_id` into chronological run timelines.
- **E1 health signals (implemented):** fleet and node pages derive read-only signals for stale nodes,
  version drift, audit body lag, retention gaps, duplicate audit reports, policy denies, system
  errors, and unrestricted local ceilings. They do not trigger remediation or mutate node state.
- **Node token lifecycle (implemented):** dashboard operators can create, rotate, and revoke
  center-managed per-node authentication tokens; secrets are generated by the center, displayed
  once, and then never listed, returned by APIs, logged, or audited.
- **Dashboard action authorization (implemented):** authenticated operators carry direct built-in
  capabilities (`fleet:view`, `tokens:manage`, `operators:manage`) instead of role groups. All
  authenticated state-changing forms require a session-bound CSRF token.
- **E2 (stubbed):** `GET /jobs/lease` authenticates the node, requires a matching `node` query
  param, bounds `capacity`, and dispatches nothing in Phase 1. The pre-dispatch threat model is in
  [`docs/dispatch-threat-model.md`](docs/dispatch-threat-model.md); it is a gate artifact, not
  implementation approval.

scootship talks only this contract; it does not depend on any Scoot internal.

## Project layout

| Path | Responsibility |
| --- | --- |
| `cmd/scootship` | CLI entrypoint: `serve`, `mock-edge`, `version`. |
| `internal/protocol` | The frozen scoot-edge v1 wire contract (envelope, bodies, cursor). |
| `internal/store` | Append-only JSONL fleet store with idempotent audit ingest, replay, visible audit-retention gaps, and retained-window run timelines. |
| `internal/tokens` | Per-node bearer-token registry, private managed lifecycle state, and dashboard-safe token inventory metadata (the center's node auth surface). |
| `internal/operators` | Dashboard operator accounts, direct capabilities, profile/password management, and password hashing. |
| `internal/loginguard` | Per-source-IP brute-force throttle for dashboard logins (failure window + lockout). |
| `internal/config` | Environment-driven configuration. |
| `internal/center` | HTTP server, bearer + login-session auth, capability gates, CSRF checks, telemetry ingest, lease stub, read-only health signals, dashboard + JSON API. |
| `internal/web` | Embedded dashboard templates and static assets (`embed.FS`). |
| `internal/mockedge` | Simulated scoot-edge node (stands in for the not-yet-built edge). |
| `internal/version` | Build version string; release builds override it from the tag. |
| `.github/workflows` | CI, mdBook docs, and tag-driven release automation. |
| `.agents/skills` | Project-local release and audit skills. |
| `docs/roadmap.md` | Project shape, non-goals, and direction. |

## Contributing

Read [`AGENT.md`](AGENT.md) (engineering handbook) and [`docs/roadmap.md`](docs/roadmap.md)
(intent and hard boundaries) before making changes. For production operation, read
[`docs/deployment.md`](docs/deployment.md). Run `make ci` before pushing.

## License

[MIT](LICENSE) — matching the Scoot ecosystem.
