#!/usr/bin/env bash

# ------------------------------------------------------------
# Scootship Repository Project Audit Script (corrected)
# ------------------------------------------------------------
# This script follows the `project-audit` skill guidelines.
# It runs read‑only checks, captures outputs, and generates a
# timestamped Markdown report under the `reports/` directory.
# ------------------------------------------------------------

set -euo pipefail

# Helper functions
log() { echo "[audit] $*"; }

time_now() { date '+%Y%m%d-%H%M%S'; }

# Ensure the reports directory exists (git‑ignored by .gitignore)
mkdir -p reports

# Repository metadata
COMMIT_SHORT=$(git rev-parse --short HEAD)
BRANCH_REF=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "DETACHED")
TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S %Z')
REPORT_FILE="reports/$(time_now)-project-audit.md"

log "Generating report $REPORT_FILE"

# 0. Orientation – work‑tree dirt
log "Collecting work‑tree dirt"
ORIENT=$(git status --porcelain || true)
if [ -z "$ORIENT" ]; then ORIENT="none"; fi

# 1. Build & Test health (BT)
log "Running gofmt"
GOFMT_OUT=$(gofmt -l . || true)
log "Running go vet"
GOVET_OUT=$(go vet ./... 2>&1 || true)
log "Running go test"
GOTEST_OUT=$(go test ./... 2>&1 || true)
log "Running go build"
GOBUILD_OUT=$(go build ./... 2>&1 || true)
log "Running version command"
VERSION_OUT=$(go run ./cmd/scootship version 2>&1 || true)

# 2. Docs ↔ Functionality (DF) & Documentation friendliness (DX)
log "Generating CLI help output"
HELP_OUT=$(go run ./cmd/scootship help 2>&1 || true)

# 3. Roadmap boundaries (RB)
log "Scanning for prohibited patterns"
RB_OUT=$(grep -RniE "exec|shell|command|reverse|dial|policy_ceiling|jobs/lease|dispatch|sync" internal cmd --include='*.go' || true)

# 4. Config consistency (CC)
log "Scanning config references"
CC_OUT=$(grep -R "SCOOTSHIP_" -n internal/config cmd/scootship README.md docs/README.zh-CN.md || true)

# 5. Security (SEC)
log "Scanning security‑relevant code"
SEC_OUT=$(grep -RniE "Authorization|Bearer|Set-Cookie|HttpOnly|SameSite|X-Forwarded-For|MaxBytesReader|ReadTimeout|WriteTimeout|Retry-After" internal --include='*.go' || true)

# 6. Bilingual parity (BL)
log "Counting headings in English and Chinese docs"
BL_OUT=$(grep -cE '^#' README.md docs/README.zh-CN.md AGENT.md docs/AGENT.zh-CN.md docs/roadmap.md docs/roadmap.zh-CN.md || true)

# 7. Storage / Idempotency (ST)
log "Scanning storage code"
ST_OUT=$(grep -RniE "file_gen|byte_to|cursor|idempot|audit_batch|Ack" internal/store internal/center internal/protocol --include='*.go' || true)

# 8. CI / Release readiness (REL)
log "Listing CI workflows"
CI_FILES=$(ls .github/workflows 2>/dev/null || true)
CI_CI=$(sed -n '1,200p' .github/workflows/ci.yml 2>/dev/null || true)
CI_REL=$(sed -n '1,260p' .github/workflows/release.yml 2>/dev/null || true)

# ------------------------------------------------------------
# Generate report
cat > "$REPORT_FILE" <<'EOF'
# Scootship Whole-Project Audit

- Timestamp: $TIMESTAMP
- Commit: $COMMIT_SHORT
- Branch/ref: $BRANCH_REF
- Dimensions scored: DF, DX, CQ, RB, CC, SEC, BL, BT, ST, REL
- Pre‑existing worktree dirt: $ORIENT

## Scorecard

| Dimension | Score | Rationale | Evidence |
|---|---|---|---|
| DF — Docs ↔ functionality | TBD | | |
| DX — Documentation friendliness | TBD | | |
| CQ — Code quality & robustness | TBD | | |
| RB — Roadmap boundaries | TBD | | |
| CC — Config consistency | TBD | | |
| SEC — Security | TBD | | |
| BL — Bilingual parity | TBD | | |
| BT — Build & test health | TBD | | |
| ST — Storage & idempotency | TBD | | |
| REL — CI/release readiness | TBD | | |

## Findings by dimension

### DF — Documentation ↔ functionality
- Finding: 
- Evidence: 
- Impact: 

### DX — Documentation friendliness
- Finding: 
- Evidence: 
- Impact: 

### CQ — Code quality & robustness
- Finding: 
- Evidence: 
- Impact: 

### RB — Roadmap boundaries
- Finding: 
- Evidence: 
- Impact: 

### CC — Config consistency
- Finding: 
- Evidence: 
- Impact: 

### SEC — Security
- Finding: 
- Evidence: 
- Impact: 

### BL — Bilingual parity
- Finding: 
- Evidence: 
- Impact: 

### BT — Build & test health
- gofmt output: $GOFMT_OUT
- go vet output: $GOVET_OUT
- go test output: $GOTEST_OUT
- go build output: $GOBUILD_OUT
- version output: $VERSION_OUT

### ST — Storage & idempotency
- Finding: 
- Evidence: $ST_OUT
- Impact: 

### REL — CI/release readiness
- CI workflows: $CI_FILES
- CI yaml excerpt: \n$CI_CI
- Release yaml excerpt: \n$CI_REL

## Prioritized recommendations

1. <high‑leverage change>
2. <next change>
3. <next change>

## Overall verdict

PASS/WARN/FAIL
EOF

log "Audit completed. Report written to $REPORT_FILE"
