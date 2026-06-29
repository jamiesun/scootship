---
name: auto-release
description: Cut a new scootship release end-to-end. Use when asked to "release", "发布版本", "出新版本", "bump the version", "tag a release", or "auto release". Determines the next semantic version from PRs merged since the last tag, bumps the Go version source, opens and merges a release PR, then pushes the git tag that triggers the GitHub release workflow.
---

# Auto Release

Cut a new release for **jamiesun/scootship** without version drift. The version is
computed from changes since the last tag, written to the local source of truth,
landed through a normal PR, then frozen by a `vX.Y.Z` tag. The tag triggers
`.github/workflows/release.yml`, which builds cross-platform single-binary
archives and publishes a GitHub release.

## Version model

- **Source version for normal builds:** `internal/version/version.go` →
  `var Version = "X.Y.Z"`.
- **Release builds override it from the tag:** the Release workflow runs Go with
  `-ldflags "-X github.com/jamiesun/scootship/internal/version.Version=${TAG#v}"`.
- Therefore a release = (1) bump `internal/version/version.go` on `main`, then
  (2) push a matching `vX.Y.Z` tag. Keep them equal.

Do not hardcode the release version in README, workflow files, templates, or the
web UI. They should read `version.Version` or use the GitHub tag.

## Bump decision: major vs minor vs patch

Classify every PR merged since the last tag, then take the highest bump:

| Signal in the PR (label / title / body) | Bump |
| --- | --- |
| Breaking change: label `breaking`/`breaking-change`/`major`, a `feat!:`/`fix!:` title, or `BREAKING CHANGE` in the body | **major** |
| New capability: label `enhancement`/`feature`, or `feat:` title | **minor** |
| Fix / refactor / docs / chore / perf only: label `bug`, or `fix:`/`docs:`/`chore:`/`refactor:`/`perf:` title | **patch** |

Standard SemVer math on `X.Y.Z`:

- **major** → `(X+1).0.0`
- **minor** → `X.(Y+1).0`
- **patch** → `X.Y.(Z+1)`

Notes:

- `小版本` means minor; `大版本` means major. If the user explicitly names a level,
  honor it after showing the computed recommendation.
- Pre-1.0 caveat: confirm before jumping to `1.0.0` unless the user explicitly
  asked for a stable release.
- If no PRs or direct commits landed since the last tag, stop: there is nothing
  to release.

## Procedure

Run from the repo root. Assume `gh` is authenticated and `main` is the release
branch.

### 1. Safety checks and sync

```sh
git status --porcelain
git checkout main
git pull --ff-only origin main
git fetch --tags origin
last_tag=$(git tag -l 'v*' --sort=-v:refname | head -1)
echo "last tag: ${last_tag:-<none>}"
```

Abort if the working tree is dirty, `main` cannot fast-forward, or the remote tag
state is ambiguous. If there are no tags, treat the previous version as `0.0.0`.

### 2. Collect merged PRs and direct commits since the last tag

This repo normally squash-merges, so a merge commit subject often ends with
`(#N)`:

```sh
range="${last_tag:+$last_tag..}origin/main"
git log --oneline "$range"
git log --oneline "$range" | grep -oE '\(#[0-9]+\)' | tr -d '(#)' | sort -un
```

For each PR number, read the signals:

```sh
gh pr view <N> --json number,title,body,labels,mergedAt \
  --jq '{n:.number, title:.title, labels:[.labels[].name], body:.body}'
```

If a commit has no `(#N)`, classify the commit subject with the same
`feat/fix/docs/chore/refactor/perf/!` rules and include it in the evidence.

### 3. Decide and announce the next version

Apply the table across all collected PRs and direct commits. Tell the user:

- the recommended bump level,
- the computed next version,
- one evidence line per PR/commit: number or short SHA, bump level, and why.

If the user gave an explicit override, use it but call out the difference.

### 4. Bump the source version

Edit only the version string in `internal/version/version.go`:

```go
var Version = "X.Y.Z"
```

Then validate:

```sh
make ci
go run ./cmd/scootship version
```

The version smoke must print `scootship X.Y.Z`.

### 5. Land the bump via a release PR

```sh
git checkout -b release/vX.Y.Z
git add internal/version/version.go
git commit -m "Release vX.Y.Z

<one bullet per PR/commit since the last tag>"
git push -u origin release/vX.Y.Z

gh pr create --base main --head release/vX.Y.Z \
  --title "Release vX.Y.Z" \
  --body "Version bump to vX.Y.Z.

## Changes since ${last_tag:-start}
<bulleted evidence with PR links or commit SHAs>"
```

Watch CI and merge only when green:

```sh
gh pr checks <pr-number> --watch --interval 15
gh pr merge <pr-number> --squash --delete-branch
git checkout main
git pull --ff-only origin main
```

Confirm `internal/version/version.go` on `main` still equals `X.Y.Z` before
tagging.

### 6. Tag to trigger the Release workflow

```sh
git tag -a "vX.Y.Z" -m "Release vX.Y.Z"
git push origin "vX.Y.Z"
gh run watch "$(gh run list --workflow=Release --branch "vX.Y.Z" --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status
```

The Release workflow should build these archives and matching checksums:

- `scootship-vX.Y.Z-linux-amd64.tar.gz`
- `scootship-vX.Y.Z-linux-arm64.tar.gz`
- `scootship-vX.Y.Z-linux-armv7.tar.gz`
- `scootship-vX.Y.Z-macos-amd64.tar.gz`
- `scootship-vX.Y.Z-macos-arm64.tar.gz`
- `scootship-vX.Y.Z-windows-amd64.tar.gz`

### 7. Verify the published release

```sh
gh release view "vX.Y.Z" --json tagName,isDraft,isLatest,assets,url \
  --jq '{tag:.tagName, draft:.isDraft, latest:.isLatest, url:.url, assets:[.assets[].name]}'
```

Done when: the tag exists, the release is not a draft, all archives and `.sha256`
files are attached, and a downloaded binary prints `scootship X.Y.Z`.

## Guardrails

- Never tag before the release PR is merged to `main`.
- Never merge the release PR while required checks are failing.
- Never publish a tag whose `X.Y.Z` differs from `internal/version/version.go` on
  the tagged commit.
- Do not edit protocol fields, dispatch behavior, node policy ceilings, token
  handling, or dashboard auth as part of a release-only PR.
- Do not put node tokens, TLS keys, dashboard passwords, or GitHub PATs in commit
  messages, PR bodies, workflow logs, release notes, or shell history.
- If the Release workflow fails after the tag is pushed, fix forward with a new
  commit and a new tag unless the tag has not been consumed publicly and the user
  explicitly authorizes tag deletion.
