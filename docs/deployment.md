# Scootship Deployment, Backup, and Recovery

**English** | [简体中文](deployment.zh-CN.md)

This document is the operator runbook for a real Scootship center. It covers production transport,
storage, backup, and recovery. The center persists E2 dispatch records and lets `dispatch:manage`
operators create node-targeted dispatch jobs; this runbook does not grant the center any authority
to raise a node's local policy ceiling, and it is not a threat-model review for the separate,
still-unbuilt dispatch **control** surface (cancel/retry/edit of an existing job) — see
[`docs/dispatch-threat-model.md`](dispatch-threat-model.md) for that.

## Production Boundary

Scootship has one inbound trusted surface: the center HTTP service. In production, run it in one of
two explicit transport modes:

- **Direct HTTPS:** set both `SCOOTSHIP_TLS_CERT` and `SCOOTSHIP_TLS_KEY`; the binary serves TLS
  itself.
- **Trusted TLS proxy:** terminate HTTPS at a reverse proxy, bind Scootship to a private interface,
  and set `SCOOTSHIP_BEHIND_TLS_PROXY=1`.

Plain HTTP without `SCOOTSHIP_DEV=1` or `SCOOTSHIP_BEHIND_TLS_PROXY=1` fails closed at startup.
`SCOOTSHIP_DEV=1` is local-only; it seeds `admin/admin` and `n-dev=dev-token` and must not be used
for a shared center.

## Files and Permissions

Use a dedicated OS user and a private data directory:

```sh
sudo useradd --system --home /var/lib/scootship --shell /usr/sbin/nologin scootship
sudo install -d -o scootship -g scootship -m 0700 /var/lib/scootship
sudo install -d -o root -g scootship -m 0750 /etc/scootship
```

Important files:

| Path | Contents | Required protection |
| --- | --- | --- |
| `SCOOTSHIP_DATA_DIR/center.jsonl` | append-only telemetry and audit store | private data directory; backup as sensitive |
| `SCOOTSHIP_DATA_DIR/operators.json` | dashboard operator records with password hashes and direct capabilities | `0600`, backup as sensitive |
| `SCOOTSHIP_DATA_DIR/managed_node_tokens.json` | center-managed node token secrets and revocations | `0600`, backup as secret material |
| `SCOOTSHIP_NODE_TOKENS_FILE` | optional static node token JSON | regular private file, no group/world/executable bits |
| TLS private key | direct HTTPS key when used | private key handling; never commit or log |

Example static token file:

```sh
sudo tee /etc/scootship/node-tokens.json >/dev/null <<'JSON'
{
  "node-a": "replace-with-node-a-secret"
}
JSON
sudo chown scootship:scootship /etc/scootship/node-tokens.json
sudo chmod 0600 /etc/scootship/node-tokens.json
```

## Environment File

Keep the service configuration outside the repository:

```sh
sudo tee /etc/scootship/scootship.env >/dev/null <<'ENV'
SCOOTSHIP_ADDR=127.0.0.1:8080
SCOOTSHIP_BEHIND_TLS_PROXY=1
SCOOTSHIP_DATA_DIR=/var/lib/scootship
SCOOTSHIP_ADMIN_USER=admin
SCOOTSHIP_ADMIN_PASSWORD=replace-on-first-bootstrap-only
SCOOTSHIP_NODE_TOKENS_FILE=/etc/scootship/node-tokens.json
SCOOTSHIP_AUDIT_RETENTION_EVENTS=1000
SCOOTSHIP_TRUSTED_PROXIES=127.0.0.1/32
ENV
sudo chown root:scootship /etc/scootship/scootship.env
sudo chmod 0640 /etc/scootship/scootship.env
```

`SCOOTSHIP_ADMIN_PASSWORD` is only used to bootstrap the first operator when
`operators.json` is empty. After bootstrap, manage operators from the dashboard. Do not leave a
real password in shell history.

Bootstrap and legacy operators receive all current built-in capabilities
(`fleet:view`, `tokens:manage`, `operators:manage`, `dispatch:manage`) so an upgrade cannot lock out
the governance surface. New operators should be granted only the capabilities they need.

Dashboard token management generates node bearer tokens on the center side. The generated secret is
shown once after create or rotate; after that the dashboard and API expose only the node id, source,
fingerprint, and authentication activity. Copy the generated secret into the edge configuration for
the same node id. The current `scoot-edge` client sends it as `Authorization: Bearer <token>`; for
local protocol testing this is `SCOOT_EDGE_TOKEN=<secret>` together with `--node-id <node>` and the
center `/telemetry` URL.

## systemd Unit

Install the binary and run it as the dedicated user:

```ini
[Unit]
Description=Scootship center
After=network-online.target
Wants=network-online.target

[Service]
User=scootship
Group=scootship
EnvironmentFile=/etc/scootship/scootship.env
ExecStart=/usr/local/bin/scootship serve
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/scootship

[Install]
WantedBy=multi-user.target
```

Then:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now scootship
sudo systemctl status scootship
curl -fsS http://127.0.0.1:8080/healthz
```

For direct HTTPS, replace `SCOOTSHIP_BEHIND_TLS_PROXY=1` with `SCOOTSHIP_TLS_CERT` and
`SCOOTSHIP_TLS_KEY`, bind `SCOOTSHIP_ADDR` to the intended address, and verify with `https://`.

## Container Image

Release tags publish multi-arch Linux images to GHCR. Docker tags omit the leading Git tag `v`:

```sh
docker pull ghcr.io/jamiesun/scootship:X.Y.Z
docker pull ghcr.io/jamiesun/scootship:X.Y.Z-alpine
```

The image defaults to `scootship serve`, listens on `:8080`, stores state in `/data`, and runs as a
non-root user. It preserves the same transport fail-closed behavior as the binary: set direct TLS,
trusted TLS proxy mode, or `SCOOTSHIP_DEV=1` for local-only testing.

Example behind a trusted TLS reverse proxy:

```sh
sudo tee /etc/scootship/scootship.container.env >/dev/null <<'ENV'
SCOOTSHIP_BEHIND_TLS_PROXY=1
SCOOTSHIP_ADMIN_PASSWORD=replace-on-first-bootstrap-only
SCOOTSHIP_NODE_TOKENS_FILE=/run/secrets/node-tokens.json
ENV
sudo chmod 0600 /etc/scootship/scootship.container.env

docker volume create scootship-data
docker run -d --name scootship \
  -p 127.0.0.1:8080:8080 \
  -v scootship-data:/data \
  -v /etc/scootship/node-tokens.json:/run/secrets/node-tokens.json:ro \
  --env-file /etc/scootship/scootship.container.env \
  ghcr.io/jamiesun/scootship:X.Y.Z
```

For direct HTTPS, also mount the certificate and key as read-only files and set
`SCOOTSHIP_TLS_CERT` / `SCOOTSHIP_TLS_KEY` to the in-container paths. Keep `/data`, token files, and
TLS keys out of the image and out of shell history. Mounted token and TLS files must be readable by
the container user (`uid 65532`) without making them world-readable; use a dedicated group, host ACL,
or Docker secret mechanism appropriate for your deployment.

## Reverse Proxy Notes

When using a reverse proxy:

- Terminate public HTTPS at the proxy.
- Bind Scootship to `127.0.0.1` or another private address.
- Set `SCOOTSHIP_TRUSTED_PROXIES` only to the proxy IP/CIDR. If unset, spoofed
  `X-Forwarded-For` is ignored and login throttling uses the raw connection address.
- Forward `Host`, `X-Forwarded-For`, and `X-Forwarded-Proto`.
- Do not expose the plain Scootship listener directly.

## Backup

Backups contain audit bodies, operator records, and possibly bearer token secrets. Store them
encrypted and restrict access like production credentials.

Recommended quiet backup:

```sh
sudo systemctl stop scootship
sudo tar --numeric-owner --xattrs -czf /secure-backups/scootship-data-$(date +%Y%m%d%H%M%S).tgz \
  -C / var/lib/scootship etc/scootship
sudo systemctl start scootship
```

If downtime is unacceptable, snapshot the underlying volume or filesystem instead. The store is
append-only and fsynced before acknowledgements, but a file-level copy taken while the service is
writing can capture a partial final line; startup skips malformed final JSONL records, so a clean
service stop or storage snapshot is preferred.

Back up:

- `SCOOTSHIP_DATA_DIR`
- `/etc/scootship` or the equivalent environment/token configuration directory
- TLS cert/key material if it is not otherwise recoverable
- the exact binary version or release archive used for the restore target

## Restore

1. Install the same or newer compatible `scootship` binary.
2. Recreate the dedicated OS user and directories.
3. Stop the service.
4. Restore the data and configuration archive.
5. Reapply ownership and permissions:

```sh
sudo chown -R scootship:scootship /var/lib/scootship
sudo chmod 0700 /var/lib/scootship
sudo chown -R root:scootship /etc/scootship
sudo chmod 0750 /etc/scootship
sudo chmod 0640 /etc/scootship/scootship.env
sudo chown scootship:scootship /etc/scootship/node-tokens.json 2>/dev/null || true
sudo chmod 0600 /etc/scootship/node-tokens.json 2>/dev/null || true
sudo chmod 0600 /var/lib/scootship/operators.json /var/lib/scootship/managed_node_tokens.json 2>/dev/null || true
```

6. Start Scootship and verify:

```sh
sudo systemctl start scootship
curl -fsS http://127.0.0.1:8080/healthz
sudo journalctl -u scootship -n 100 --no-pager
```

7. Log in to the dashboard and check:

- Fleet page renders and known nodes replay from `center.jsonl`.
- Token inventory shows expected node fingerprints, not secrets.
- Operator login works; reset operator passwords if the backup was exposed.
- A test edge heartbeat authenticates with its expected per-node token.

After restoring into a new security boundary, rotate managed node tokens and any static token file
entries that might have been exposed with the backup.

## Failure Modes

- Startup fails with plain HTTP: configure direct TLS, trusted TLS proxy mode, or local-only dev mode.
- Startup fails on token file permissions: fix the token file to a regular private file with no
  executable, group, or world permissions.
- Dashboard is locked: `operators.json` exists without a usable operator, or no bootstrap password
  was provided while the operator store was empty. Restore a valid operator store or restart with a
  bootstrap password only after moving the empty/broken operator file aside.
- Nodes cannot authenticate after restore: check `SCOOTSHIP_NODE_TOKENS_FILE`,
  `managed_node_tokens.json`, and any managed revocation for that node.

## Do Not

- Do not run production with `SCOOTSHIP_DEV=1`.
- Do not store bearer tokens, TLS keys, or real bootstrap passwords in the repository.
- Do not copy backups into low-trust ticket systems or chat logs.
- Do not expose the plain listener when relying on a TLS proxy.
- Do not treat this document as a threat-model review for dispatch **control** (cancel/retry/edit
  of an existing job); see `docs/dispatch-threat-model.md`. Dispatch **creation** is
  capability-gated (`dispatch:manage`), CSRF-protected, and bounded by
  `SCOOTSHIP_DISPATCH_QUEUE_LIMIT` per node, but `/jobs/lease` still only returns already-persisted,
  node-bound jobs, and there is still no cancel/retry/edit UI for an existing job.
