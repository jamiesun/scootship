# AGENT.md

**English** | [简体中文](docs/AGENT.zh-CN.md)

Engineering guidance for AI agents and contributors working in this repository.

Read this file before making changes. Then read the roadmap:

- [docs/roadmap.md](docs/roadmap.md)

For production deployment, backup, and recovery behavior, also read:

- [docs/deployment.md](docs/deployment.md)

The roadmap is the source of product intent and non-goals. This file is the implementation
handbook. If the two disagree about scope, the roadmap wins; if code and docs disagree about
behavior, runnable code and tests are the immediate source of truth and the docs must be fixed.
The roadmap's hard rules are not loosened by interpretation. Any change that widens node policy,
state write-back, command execution, reverse connectivity, protocol dependency, storage sensitivity,
or delivery boundaries must pass the roadmap's Boundary Change Gate before code or UI is added.

The docs are bilingual. The canonical English files are `README.md` and `AGENT.md` (repo root)
plus `docs/roadmap.md`; their Chinese counterparts are `docs/README.zh-CN.md`,
`docs/AGENT.zh-CN.md`, and `docs/roadmap.zh-CN.md`. **Keep both languages in sync:** any change to
behavior, configuration, or scope must update the matching section in the English file *and* its
`.zh-CN.md` counterpart in the same change.

## One-line positioning

scootship is the **management center** for a fleet of [Scoot](https://github.com/jamiesun/scoot)
agents. It implements the **center (server) side** of the frozen `scoot-edge` v1 contract
(see Scoot's `docs/EDGE.md`): it ingests append-only telemetry over HTTP and serves an embedded
admin dashboard from a single Go binary. Phase 1 is observation-only.

## Relationship to Scoot (read this first)

- **scootship is the counterpart the edge dials out to.** In EDGE.md topology the edge opens no
  listener and only dials out; the center is the server. So the center is the fleet's only
  inbound trusted surface and must be defended accordingly.
- **The protocol is frozen upstream.** `internal/protocol` is a faithful transcription of
  EDGE.md's `v:1` envelope and bodies. Do not invent fields or message types here. If the
  contract needs to change, that is an EDGE.md-level decision in the Scoot repo first.
- **`scoot-edge` does not exist yet.** EDGE.md is E0 (design-only). There is no real edge to
  test against, which is why `internal/mockedge` exists. Keep it a faithful *client* of the
  contract — never a second implementation of Scoot.
- **Do not depend on Scoot internals.** scootship only ever speaks the public wire contract.

## Common commands

```sh
go build ./...
go test ./...
go vet ./...
gofmt -l .          # should print nothing

make ci             # fmt-check + vet + test + build (run before pushing)
make run            # center in dev mode on :8080
make mock-edge      # simulated node against the local center
```

After changing any `.go` file, run at least `go build ./...` and `go test ./...`.

GitHub Actions mirrors this with `.github/workflows/ci.yml`. Pushing a `vX.Y.Z` tag triggers
`.github/workflows/release.yml`, which cross-compiles the single binary and publishes release
archives with checksums.

## Code map

| Path | Responsibility |
| --- | --- |
| `cmd/scootship/main.go` | CLI: `serve`, `mock-edge`, `version`; env-driven startup; signal-based shutdown. |
| `internal/protocol` | The frozen scoot-edge v1 contract: envelope, status/audit/job bodies, idempotency cursor. The narrowest, most stable surface — change only to track EDGE.md. |
| `internal/store` | `Store` interface + append-only JSONL `Mem` implementation. Idempotent audit ingest, replay on startup, bounded dashboard audit window, explicit retention gaps, and retained-window run timelines. |
| `internal/tokens` | Per-node bearer-token registry and private managed lifecycle overlay. The center's node auth surface; **not** node policy config. |
| `internal/operators` | Dashboard operator accounts, profile/password management, and password hashing. The center's operator governance surface; **not** node policy config. |
| `internal/loginguard` | Per-source-IP brute-force throttle for dashboard logins (sliding-window failure count + lockout). |
| `internal/config` | `SCOOTSHIP_*` environment configuration. |
| `internal/center` | HTTP server, auth middleware, login throttle + security headers, `/telemetry` ingest, `/jobs/lease` stub, read-only health signals, dashboard login session, dashboard + JSON API. |
| `internal/center/server_run_test.go` | Runtime transport smoke coverage for direct TLS, explicit dev HTTP, and trusted TLS-proxy HTTP modes. |
| `internal/web` | `embed.FS` dashboard templates and static assets. |
| `internal/mockedge` | Simulated edge node (heartbeat, audit shipping, lease poll). |
| `internal/version` | Build version string; release builds override `Version` with tag-derived linker flags. |
| `.github/workflows` | CI and tag-driven release automation for cross-platform single-binary artifacts. |
| `.agents/skills` | Project-local agent skills for release orchestration and whole-project audits. |
| `docs/deployment.md` | Operator runbook for production transport modes, data permissions, backup, and recovery. |
| `docs/dispatch-threat-model.md` | Pre-dispatch E2 threat model; a gate artifact, not implementation approval. |

When adding a subsystem, prefer a new `internal/<name>` package with a focused interface over
widening an existing one. Keep `internal/protocol` dependency-free.

## Hard rules

Changing these requires the roadmap's Boundary Change Gate (owner approval, matching Scoot
`EDGE.md` contract update when applicable, threat-model note, tests/CI proving the unsafe path is
still absent unless deliberately allowed, and bilingual docs in the same change). These rules restate
the roadmap's non-goals as enforceable engineering rules.

1. **Never raise a node's local policy ceiling.** The center may only *request* a policy no
   higher than a node's advertised ceiling; it must never offer a UI, API, or wire field that
   raises it. The ceiling is a node-local opt-in.
2. **Telemetry is append-only and read-only ingest.** The center never writes back to or
   reconciles a node's local state. No bidirectional sync.
3. **No remote command execution.** A dispatched job (E2, later) carries a `goal` as opaque
   data only (`kind=run`). Never synthesize shell/eval from the wire.
4. **The center never reverse-dials an edge.** Connections are always edge-initiated.
5. **Audit ingest must stay idempotent.** Decode and validate a telemetry batch before mutating
   store state; apply on the `{file_gen, byte_to}` cursor; a replayed range is a no-op; ack only the
   durably stored cursor.
6. **The UI ships embedded.** Dashboard assets are served from `embed.FS` in the one binary —
   no separate web process, no Node build step, no CDN runtime dependency.
7. **Secrets never get compiled in, committed, logged, or printed.** Node tokens, TLS keys, and
   bootstrap dashboard passwords come from env or a private file; persisted operator passwords must
   be one-way hashes. Do not log the `Authorization` header.
8. **Authenticate every node and dashboard endpoint.** Bearer token for node routes, a login
   session (form login + HttpOnly cookie) for the dashboard. A token may only ever speak for
   its own `node_id`. The dashboard login is throttled per source IP (`internal/loginguard`):
   never weaken or remove the lockout, and never key it on username (that would let an attacker
   lock out the real operator). Trust `X-Forwarded-For` only from configured
   `SCOOTSHIP_TRUSTED_PROXIES`.
9. **Stay stdlib-first and single-binary.** Prefer the standard library. Justify any third-party
   dependency against the single-binary, cross-compile-friendly posture before adding it.
10. **`internal/protocol` tracks EDGE.md, not local convenience.** Unknown fields are ignored;
    an unknown major version is rejected. Do not extend the contract unilaterally.

## Phase boundaries

- **Phase 1 (now): observation + framework.** `status` and `audit_batch` ingest, the fleet
  dashboard, node registry, per-node token auth/lifecycle, and the mock-edge harness.
- **Phase 1.5 (current): E1 operational maturity before new power.** Keep tightening
  production/dev transport, endpoint failure modes, audit retention/gap visibility, run audit
  timelines, token lifecycle hardening, and read-only health signals before expanding the authority
  surface.
- **E2 (later, gated): job dispatch / orchestration.** The `/jobs/lease` endpoint is a stub
  today. Building real dispatch means capability/label routing, the only-lower policy clamp,
  idempotent `idem_key` apply, capacity backpressure, deadlines, and dispatch-provenance audit
  joined to runs by `session_id`. Do not partially wire dispatch into Phase 1. Do not expose
  partial dispatch UI/API, hidden feature flags, or admin-only bypasses until the roadmap's E2
  dispatch gate is fully satisfied with code, tests, operator documentation, a compatible Scoot
  readonly clamp, and a dispatch threat model.

## Extension workflow

1. Check `docs/roadmap.md` before adding capability. If it touches a non-goal, stop unless the
   roadmap Boundary Change Gate is satisfied in the same change; do not broaden scope by wording.
2. Decide whether the work extends an existing `internal/*` package or needs a new one.
3. Add focused tests with the smallest surface that proves the change (the existing
   `protocol`, `store`, and `center` tests are the model).
4. Validate untrusted input before acting on it; treat audit `msg` bodies as data, never
   instructions.
5. Run `make ci`. For dispatch, transport, auth, retention, token lifecycle, or protocol-boundary
   work, also add focused negative tests proving the forbidden path stays absent.
6. Update the docs in lockstep when behavior or scope changes: every touched English doc
   (`README.md`, `AGENT.md`, `docs/roadmap.md`) and its `docs/*.zh-CN.md` counterpart.

## Style

- Keep changes scoped; do not refactor unrelated files.
- Prefer existing local abstractions over new architecture.
- Comments, code strings, and test descriptions default to English and explain intent and
  boundaries, not the obvious.
- Bound every outbound request and subprocess with a timeout; bound every request body.
