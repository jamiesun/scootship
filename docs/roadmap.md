# Scootship — Project Portrait & Direction

**English** | [简体中文](roadmap.zh-CN.md)

> This document defines what Scootship "should become, and must never become" — the North Star
> and the guardrails for all subsequent development.
> It describes goals and boundaries, not concrete construction steps. Technical choices are
> suggestions unless they are explicitly tied to the hard boundaries below. Security, protocol,
> dispatch, storage, and delivery boundaries are not implementation preferences; they are gates.

## Overview

Scootship is the **management center** designed for a fleet of [Scoot](https://github.com/jamiesun/scoot) agents.

Scoot itself is a pure-Zig, local-first, single-binary AI agent daemon / CLI: it turns a goal or
scheduled task into auditable system-level actions (shell, files, search, HTTP), leaving a JSONL
audit trail throughout, and denies dangerous operations by default. Today every Scoot instance is
an island; observing one or dispatching tasks means SSHing into each machine one by one.

Scootship is exactly the "future console / management center" that Scoot's `docs/EDGE.md` keeps
referring to. Through the **`scoot-edge` integration protocol** it receives the health status and
audit logs the fleet reports and (once explicitly enabled) dispatches tasks expressed as "goals"
to the fleet, so an operator can observe and orchestrate the whole Scoot fleet from one standard
admin dashboard.

The core operating model follows the topology and authorization model already frozen by `scoot-edge`:

- **The edge only dials out; the center never dials back.** `scoot-edge` actively connects out to
  the center; the center is the server and never reverse-connects to an edge. The edge opens no
  inbound port.
- **The center is the fleet's only trusted inbound surface.** Because the center exposes the
  connected-into server, the security weight falls on the **authorization model** rather than the
  transport: per-node bearer token, mandatory HTTPS, all upstream data append-only and never
  written back to local state.
- **The center's power has a ceiling, set locally on each machine.** Tasks the center dispatches
  default to `readonly` and can only be "downgraded" by local config; the center can never raise
  any node's local policy ceiling.

### Architecture diagram

```text
                    Operators (browser)
                          |  HTTPS · dashboard login auth / action authz
                          v
   +-- Scootship · single Go binary --------------------------------+
   |
   |   Embedded Web UI (embed.FS) -- standard admin dashboard
   |   Center HTTP API
   |     * POST /telemetry   <- status heartbeat / audit_batch   (E1)
   |     * GET  /jobs/lease   -> job dispatch long-poll           (E2)
   |   - node registry & per-node token issue / revoke
   |   - task orchestration & job lifecycle                       (E2)
   |   - audit storage (fleet-reported audit + center dispatch trace)
   |   - embedded store (SQLite suggested; single-file, append-only)
   +----------------------------------------------------------------+
                          ^
                          |  edge dials OUT (HTTPS + per-node token)
                          |  NDJSON · upstream append-only
        +-----------------+-----------------+
        |                 |                 |
    scoot-edge        scoot-edge        scoot-edge   (optional · not installed by default · see premises)
        |                 |                 |
      scoot             scoot             scoot       (local-first agent)
   logs/*.jsonl      logs/*.jsonl      logs/*.jsonl
```

### Key premises and dependencies (to verify / coordination risk)

- **`scoot-edge` is currently E0: design only, no code.** `EDGE.md` explicitly states "No
  `scoot-edge` code exists yet", and its authorization model and red lines must be signed off
  before any edge code is written. This means Scootship will, for a long time, **have no real
  edge to integrate with**. So "being able to work against the protocol contract" and "shipping a
  mock edge that simulates reporting and job leasing" are hard requirements for Scootship early
  on, not nice-to-haves.
- **The protocol contract follows `EDGE.md` (`v:1`).** The envelope looks like
  `{"v":1,"type":"status|audit_batch|job|job_event","node_id":"...","sent_ts":<ms>,"body":{}}`;
  the frame format is NDJSON. Scootship must freeze this contract into a standalone, versioned
  integration module and keep it in lockstep with the evolution of `scoot-edge` on the Scoot side.
- **The audit event schema comes from Scoot's `src/audit.zig`**:
  `{"seq","ts","session_id","run_id?","kind","msg"}`, with
  `kind ∈ {run,thought,tool_call,observation,final,policy_deny,system_error}`. On ingest Scootship
  must de-duplicate on `EDGE.md`'s idempotency cursor `{file_gen, byte_offset, seq}`, store
  append-only, and treat a repeated range as a no-op.

## Project portrait (target state)

Once done, Scootship should look like this:

- **The fleet at a glance.** Opening the dashboard, an operator immediately sees: which nodes are
  online, each one's last heartbeat, Scoot/edge versions, each node's local policy ceiling
  (`policy_ceiling`), audit-event counts and trends, and (once enabled) node labels and capability
  profiles. Observation is the entire focus of Phase 1.
- **Delivered as a single binary.** Like Scoot, Scootship should be "copy one executable and run".
  The web frontend is compiled into the same binary via Go's `embed.FS` — no separate frontend
  process, no external static server, no Node runtime.
- **The center is a tightly defended inbound surface.** Because the edge never opens a port and
  only the center is connected into, the center is the only new trusted attack surface in the
  fleet. Every inbound endpoint must authenticate (per-node bearer token), rate-limit, bound
  request-body size, and set hard timeouts. HTTPS is mandatory, even inside a VPC.
- **Audit is replayable, dispatch is traceable.** The center stores two layers of auditable fact:
  (1) the Scoot run audit ingested from the fleet, enough to replay line by line what an agent
  actually did; (2) the center's own dispatch provenance (who, when, which goal, under what
  `effective_policy`, to which node — joined back to Scoot's run audit by `session_id`).
- **Simple over fancy.** Pick **one** low-complexity frontend approach for the admin dashboard.
  The default is Go templates plus minimal static JavaScript/CSS from `embed.FS`. A pre-compiled
  SPA is acceptable only if its build is reproducible, dependency-locked, embedded into the same
  binary, free of runtime CDN/external asset dependencies, and does not turn CI or releases into a
  separate frontend product. Don't chase a full component library or flashy interactions.
- **Data sensitivity is taken seriously.** `audit_batch` may carry file contents, command output,
  and HTTP response bodies — once enabled, the center becomes a sink for potentially sensitive
  observation data. The center must do its own login authentication and action authorization for
  dashboard access, and have a retention / cleanup policy for stored audit data.
- **Operational trust comes before remote orchestration.** The center must not grow into E2 job
  dispatch until the E2 dispatch gate is fully satisfied: production/dev transport boundaries are
  tested, deployment and recovery are documented, audit lifecycle and gaps are implemented, node auth
  can be governed, and health signals are clear on the dashboard.

Priorities when qualities conflict (highest to lowest):

1. **Security and control** — rather drop a feature than let the center raise any node's local
   policy ceiling, write back Scoot's local state, or compile node tokens into the binary or write
   them to logs.
2. **Faithful, lossless, dup-free observation** — ingest must be idempotent and append-only; the
   cursor advances only after the center acks; rather mark an explicit `audit_gap` than silently
   drop data.
3. **Simple delivery and operations** — a single binary, embedded assets, and simple storage beat
   architectural flourish.
4. **Operational maturity** — production-safe defaults, deployment clarity, audit lifecycle, token
   governance, and health signals outrank new power surfaces.
5. **Feature breadth** — orchestration, alerting, a rich audit UI, etc. all rank below the above.

## Current capabilities

Phase 1 (observation and base framework, aligned with scoot-edge E1) has landed; it runs and is
tested:

- **Protocol integration module.** `EDGE.md`'s `v:1` envelope, `status` / `audit_batch` shapes,
  and the audit schema are frozen in `internal/protocol`; unknown fields are ignored, an unknown
  major version is rejected.
- **Telemetry ingest and idempotent storage.** `POST /telemetry` ingests `status` heartbeats and
  `audit_batch`; de-duplicates on the `{file_gen, byte_to}` idempotency cursor, stores append-only,
  and replays on startup (`internal/center`, `internal/store`).
- **Audit retention window and gap visibility.** The center keeps a configurable recent audit window
  per node for API/dashboard reads (`SCOOTSHIP_AUDIT_RETENTION_EVENTS`) while preserving accepted
  events in the append-only JSONL log; retention overflow is surfaced as an explicit center-side
  `audit_gap` in node lifecycle state (`internal/store`, `internal/center`, `internal/web`).
- **Run audit timeline over retained audit.** Node detail API/pages group retained audit events by
  `session_id` / `run_id` and order each run by `seq` / `ts`, so recent agent activity can be read
  without opening raw JSONL (`internal/store`, `internal/center`, `internal/web`).
- **Read-only health signals.** Fleet and node views derive dashboard-visible health signals for
  offline/stale nodes, version drift, audit body lag, audit retention gaps, duplicate audit reports,
  policy denies, system errors, and unrestricted local ceilings without adding any remediation path
  (`internal/center`, `internal/web`).
- **Node registry and token auth.** Per-node bearer-token auth; a token may only ever speak for its
  own `node_id`; the dashboard exposes read-only token inventory metadata (source, fingerprint,
  last authentication) without displaying bearer secrets (`internal/tokens`, `internal/center`).
- **Observation dashboard.** An embedded admin dashboard (fleet overview, node detail, token
  inventory, collapsible left sidebar), compiled into the single binary via `embed.FS`
  (`internal/web`, `internal/center`).
- **Dashboard login and operator governance.** Form login + HttpOnly cookie session, optional
  "remember this device" long-lived session (never password storage), multi-operator management,
  profile/password updates, and per-source-IP failed-login lockout backed by strict security
  response headers (`internal/center`, `internal/operators`, `internal/loginguard`).
- **Mock edge harness.** Simulates heartbeats and audit shipping without a real `scoot-edge`,
  exercising the end-to-end path (`internal/mockedge`, `cmd/scootship mock-edge`).
- **CLI and configuration.** `scootship serve | mock-edge | version`, all configured via
  `SCOOTSHIP_*` environment variables; production plain HTTP fails closed unless direct TLS,
  explicit dev mode, or explicit trusted TLS-proxy mode is configured (`cmd/scootship`,
  `internal/config`).
- **CI, release automation, and project skills.** GitHub Actions run CI and tag-driven release
  builds for cross-platform single-binary archives with checksums; project-local skills document
  controlled release orchestration and whole-project audits (`.github/workflows`,
  `.agents/skills`).

Phase 2 (task orchestration and dispatch) is not implemented yet: `GET /jobs/lease` is an
observation-period stub today — it authenticates per the contract but dispatches no jobs.

> Note: this section records only what already exists. As each capability lands, move it here from
> "Direction and intent" and cite its entry point or evidence path.

## Non-goals (hard rules)

Unless the boundary is explicitly changed through the gate below, all of the following are hard
rules that must not be crossed:

- **Do not configure or modify Scoot's local execution policy / permission ceiling.** The
  `guarded/readonly/unrestricted` ceiling is each machine's **local** opt-in. When dispatching,
  Scootship may only request a policy "no higher than" the node's local ceiling, clamped by the
  node itself; the center can never raise any node's `policy_ceiling`. This is a boundary the user
  has explicitly drawn, and a red line in `EDGE.md`.
- **Do not write back or reverse-sync Scoot's local state.** All upstream telemetry is append-only,
  read-only ingest; the center never treats itself as a writable source of truth doing two-way
  state reconciliation with the edge. This is not "cloud sync".
- **Do not execute raw commands on a node.** A task can only be sent down as a `kind=run` "goal"
  carried as **data**, re-validated by Scoot like any local input. The center never synthesizes
  shell / eval and never offers an arbitrary remote command-execution channel.
- **Do not reverse-dial into the edge.** The center is purely a server; connections are always
  initiated by the edge. The center neither holds nor probes edge addresses and opens no reverse
  connection.
- **Do not build a public-facing multi-tenant SaaS / billing system.** Follow `EDGE.md`'s VPC
  intranet deployment assumption; even on a trusted network, treat the privilege layer as untrusted
  (defense in depth).
- **Do not turn the frontend into a separate process or a heavy frontend project.** Web assets must
  be `embed`ded into the single binary; introduce no heavy runtime, build chain, or microservice
  split that breaks "single-file delivery".
- **Do not bake secrets into artifacts.** Node bearer tokens, TLS private keys, etc. are never
  compiled into the binary, committed to the repo, printed, or written to any audit or log (aligned
  with Scoot constraint 7).
- **Do not extend Scoot unilaterally around the protocol contract.** Scootship interacts with the
  fleet only through the public `scoot-edge` protocol surface; it does not depend on Scoot's
  internal implementation details or require Scoot to expose private subsystems.

### Boundary change gate

Changing any hard rule above is not a normal documentation edit. A boundary change is valid only
when all of these are true in the same change set:

- the owner explicitly approves the new boundary and the reason it is worth the added authority;
- the corresponding Scoot `EDGE.md` contract is updated first or in lockstep, with no private
  Scoot internals becoming a Scootship dependency;
- the change includes a focused threat-model note covering abuse paths, rollback, and operator
  recovery;
- tests or CI checks prove the old unsafe path is still absent unless the new boundary deliberately
  permits it;
- both English and Chinese roadmap / agent docs are updated together.

If any item is missing, agents must treat the existing non-goal as still binding and stop rather
than widening scope by interpretation.

## Direction and intent

> The user organized requirements by "phase", so the directions below align with `scoot-edge`'s
> E1 / E2 phases and note which portrait or boundary each serves. Each is expressed as an outcome,
> not a prescribed implementation order.

### Phase 1 · Observation and base framework (aligned with scoot-edge E1)

Serves the portraits "the fleet at a glance", "single-binary delivery", and "the center is a
tightly defended inbound surface".

- **An integration base you can work against the contract with.** Freeze `EDGE.md`'s `v:1`
  envelope, `status` / `audit_batch` shapes, and audit schema into a versioned protocol module;
  ignore unknown fields, reject an unknown major version.
- **Mock edge harness.** Because a real `scoot-edge` does not exist yet, Scootship must be
  developable and verifiable without a real edge: a test edge that can simulate node heartbeats,
  capability descriptors, and `audit_batch` reporting. In Phase 1, lease polling verifies only the
  authenticated empty-dispatch contract (`X-Scootship-Dispatch: disabled-phase1`); real job
  lifecycle simulation belongs to E2 after the dispatch gate is satisfied.
- **Node registry and token governance.** The center issues, stores (securely), recognizes,
  rate-limits, and revokes **per-node** tokens — this is the center's own governance surface,
  distinct from "Scoot permission config", and does not violate the hard rules above.
- **Telemetry ingest (heartbeat first, then log bodies).** Get `status` heartbeats working first
  (version, daemon state, `policy_ceiling`, `audit_stats`); ingest `audit_batch` de-duplicated on
  the idempotency cursor, stored append-only, respecting the "off by default, kind allowlist"
  upstream constraint.
- **Observation dashboard.** A low-complexity admin dashboard: fleet overview, node detail, version
  drift, policy ceiling, audit-stat trends, and (once enabled) label / capability profiles.
- **A tightly defendable inbound service.** Mandatory HTTPS (the center holds the server cert, or
  terminate TLS at a trusted reverse proxy), per-endpoint auth, rate-limiting, request-body bounds,
  hard timeouts; the dashboard's own login authentication and action authorization — form login +
  HttpOnly cookie session, with failed logins **throttled and locked out per source IP on a sliding
  window** to resist brute force (the source IP trusts `X-Forwarded-For` only when a trusted proxy
  is configured), backed by strict security response headers (CSP, `X-Frame-Options`, etc.).

### Phase 1.5 · E1 operational maturity package

Serves the portraits "the center is a tightly defended inbound surface", "audit is replayable", and
"data sensitivity is taken seriously". This is the preferred next direction before E2 expands the
center's authority.

- **Production/dev transport boundary.** Production mode is fail-closed for insecure transport unless
  an explicitly named development mode is selected; local integration remains possible through a
  clear dev-only path such as trusted local HTTP or a one-command self-signed HTTPS setup.
- **Deployment and recovery clarity.** Operators can understand and reproduce safe deployments:
  direct TLS versus trusted reverse proxy, private data-directory ownership and permissions, node
  token file permissions, and backup / restore expectations for append-only telemetry and operator
  state.
- **Bounded edge endpoints with visible failure modes.** `/telemetry` and `/jobs/lease` have clear
  route-level expectations for timeouts, request-body limits (including bodyless lease requests),
  authentication failures, idempotency errors, and operator-visible error / health signals.
- **Real-edge integration path.** Since real `scoot-edge` clients may require `https://`, Scootship's
  dev story must make local center/edge integration explicit without weakening production defaults.
- **Audit lifecycle and gap visibility.** Sensitive audit bodies have retention, cleanup, capacity,
  and explicit `audit_gap` behavior that operators can reason about before storing large real fleets.
- **Run audit timeline.** The dashboard can organize ingested audit by `session_id`, `run_id`, `seq`,
  and `ts`, so an operator can answer "what did this agent run actually do?" without reading raw
  JSONL.
- **Token governance as node authentication, not node policy.** Token inventory matures toward clear
  create / rotate / revoke flows and recent-authentication visibility, while never implying authority
  to change a node's local `policy_ceiling`.
- **Read-only health signals.** Node offline state, version drift, `policy_deny` spikes, audit stalls,
  and suspicious duplicate-reporting patterns become dashboard-visible signals before any notification
  or remediation system is added.

### Phase 2 · Task orchestration and dispatch (aligned with scoot-edge E2)

Serves the portrait "dispatch is traceable" and the hard rules "the center does not raise the local
ceiling / does not execute raw commands". This phase is deliberately gated: keep `/jobs/lease` as an
authenticated empty-dispatch stub until every dispatch gate below is satisfied and the Scoot side has
landed the corresponding unattended readonly clamp.

The E2 dispatch gate is all-or-nothing. Do not expose partial dispatch UI/API, hidden feature flags,
or "admin-only" bypasses until these conditions are met:

- E1 transport behavior is tested for direct TLS, trusted TLS proxy, explicit dev mode, and fail-closed
  plain HTTP.
- Deployment, backup, restore, data-directory permissions, token-file permissions, and recovery
  procedures are documented for a real operator.
- Audit retention, capacity limit, and explicit `audit_gap` behavior exist in code and tests.
- Token create / rotate / revoke flows are implemented with e2e or integration coverage.
- Run audit timeline views can correlate `session_id`, `run_id`, `seq`, and `ts` without raw JSONL
  spelunking.
- Read-only health signals for offline nodes, version drift, policy-deny spikes, audit stalls, and
  duplicate reporting are visible before notification or remediation is added.
- Scoot has shipped the unattended readonly clamp and the compatible `scoot-edge` contract version is
  named in the Scootship change.
- A dispatch threat-model note covers queue abuse, replay/idempotency, capability spoofing,
  authorization, audit provenance, and rollback.

- **Long-poll-based job dispatch.** Implement the center-side semantics of
  `GET /jobs/lease?node=&capacity=`: route jobs by a node's most recent capability / label
  descriptor; on a miss, reject with `no_matching_capability` — a capability mismatch only
  downgrades to reject, never to unsafe execution.
- **Job lifecycle and idempotency.** Track `accepted/running/done/failed/rejected`, carry
  `idem_key` to guarantee "the same job runs only once", and respect capacity backpressure
  (`at_capacity`), `deadline_ts`, and a retry cap.
- **Only-lower policy expression.** When dispatching, only request a policy `≤` the node's local
  ceiling, defaulting to `readonly`; the center UI / API offers no entry point to "raise a node's
  ceiling".
- **Dispatch-provenance audit.** The center records the full story of each dispatch and joins it
  back to the ingested Scoot run audit via `session_id`, forming an end-to-end traceable chain.
- **E2 prerequisites are explicit.** Queue semantics, capability / label matching, `idem_key`
  idempotency, capacity backpressure, deadlines, `job_event` handling, dispatch provenance, and the
  Scoot-side unattended readonly clamp must exist as code plus tests, not just prose, before any
  partial dispatch UI or API is exposed.

### Phase 3 · Governance and operational scale

Serves the portraits "the center is a tightly defended inbound surface" and "data sensitivity is
taken seriously" after the E1 maturity baseline is in place.

- **Notification and response maturity.** Health signals that are first visible in the dashboard can
  later feed explicit notification or incident workflows, without silently mutating node state.
- **Multi-operator governance.** Mature the dashboard's role / access control (still the center's
  own governance, not equal to Scoot permission config).
- **Fleet-scale operations.** Keep storage, backup, cleanup, and dashboard performance understandable
  as the number of nodes and retained audit bodies grows.

## What "done" looks like

> A direction is truly achieved only when the observable outcomes below appear. The concrete
> techniques (SQLite, HTMX, templates, e2e tests, etc.) are examples and suggestions, chosen by the
> implementer in practice.

- **Protocol integration is real and verifiable.** Even before a real `scoot-edge` ships, Scootship
  can use its built-in mock edge to run the full "heartbeat → ingest → visible on dashboard" path and
  the Phase 1 "authenticated lease poll → empty dispatch" path, with automated tests guarding this
  core data flow. Real "lease job → report lifecycle" verification is an E2-only completion signal.
- **Observation is one glance away.** Without querying a database, an operator sees fleet online
  status, versions, policy ceilings, and audit stats on the dashboard; a new node appears in the
  registry automatically after it reports.
- **Ingest is lossless and dup-free.** A re-delivered `audit_batch` range is a no-op; the cursor
  advances only after the center acks; exceeding the retention cap produces an explicit `audit_gap`
  marker rather than a silent loss.
- **E1 is production-credible before E2.** The E2 dispatch gate above is fully checked off with code,
  tests, and operator documentation before remote dispatch grows.
- **Boundaries hold automatically.** Any path that tries to "raise a node's local policy ceiling",
  "write back Scoot's local state", "run raw commands on a node", or "reverse-connect into the edge"
  does not exist by design; dispatch can only downgrade.
- **The inbound surface is secure.** All edge endpoints require production-safe transport and a valid
  per-node token; a token can be rotated or revoked individually without affecting the fleet; secrets
  never appear in the binary, repo, logs, or audit.
- **Delivery is a single file.** The build artifact is one Go binary with embedded frontend assets;
  copy and run, with no external frontend process or Node toolchain at startup.
- **Dispatch is end-to-end traceable.** Any job the center dispatches can be strung back to the
  corresponding Scoot instance's run audit via `session_id`, answering "who, what, under what
  policy, to whom, and with what result".
