#!/bin/sh
set -eu

src_dir=".mdbook/src"
repo_url="https://github.com/jamiesun/scootship/blob/main"

rm -rf "$src_dir"
mkdir -p "$src_dir/en" "$src_dir/zh-CN" "$src_dir/assets"
cp docs/assets/hero.png "$src_dir/assets/hero.png"

cat > "$src_dir/index.md" <<'EOF'
# scootship documentation

![scootship — fleet command center](assets/hero.png)

scootship is the management center for a fleet of Scoot agents. This mdBook is generated from the repository's canonical bilingual Markdown documentation.

## Choose a language

- [English documentation](en/overview.md)
- [简体中文文档](zh-CN/overview.md)

## What is included

- Project overview and quick start
- Project roadmap and hard boundaries
- Production deployment, backup, and recovery runbook
- E2 dispatch threat model gate artifact
- Agent/contributor engineering guide

The runtime product remains a single Go binary with an embedded dashboard; this book is documentation output only.
EOF

cat > "$src_dir/SUMMARY.md" <<'EOF'
# Summary

[Home](index.md)

# English

- [Overview](en/overview.md)
- [Project roadmap](en/roadmap.md)
- [Deployment, backup, and recovery](en/deployment.md)
- [E2 dispatch threat model](en/dispatch-threat-model.md)
- [Agent engineering guide](en/agent.md)

# 简体中文

- [概览](zh-CN/overview.md)
- [项目路线图](zh-CN/roadmap.md)
- [部署、备份与恢复](zh-CN/deployment.md)
- [E2 派发威胁模型](zh-CN/dispatch-threat-model.md)
- [Agent 工程指南](zh-CN/agent.md)
EOF

rewrite_common() {
  input="$1"
  output="$2"
  lang="$3"
  cp "$input" "$output"

  if [ "$lang" = "en" ]; then
    perl -0pi.bak \
      -e 's#\]\(docs/assets/hero\.png\)#](../assets/hero.png)#g;' \
      -e 's#\]\(docs/README\.zh-CN\.md\)#](../zh-CN/overview.md)#g;' \
      -e 's#\]\(docs/AGENT\.zh-CN\.md\)#](../zh-CN/agent.md)#g;' \
      -e 's#\]\(docs/roadmap\.md\)#](roadmap.md)#g;' \
      -e 's#\]\(docs/deployment\.md\)#](deployment.md)#g;' \
      -e 's#\]\(docs/dispatch-threat-model\.md\)#](dispatch-threat-model.md)#g;' \
      -e 's#\]\(README\.md\)#](overview.md)#g;' \
      -e 's#\]\(AGENT\.md\)#](agent.md)#g;' \
      -e 's#\]\(roadmap\.zh-CN\.md\)#](../zh-CN/roadmap.md)#g;' \
      -e 's#\]\(deployment\.zh-CN\.md\)#](../zh-CN/deployment.md)#g;' \
      -e 's#\]\(dispatch-threat-model\.zh-CN\.md\)#](../zh-CN/dispatch-threat-model.md)#g;' \
      -e "s#\\]\\(internal/protocol/protocol\\.go\\)#](${repo_url}/internal/protocol/protocol.go)#g;" \
      -e "s#\\]\\(LICENSE\\)#](${repo_url}/LICENSE)#g;" \
      "$output"
  else
    perl -0pi.bak \
      -e 's#\]\(assets/hero\.png\)#](../assets/hero.png)#g;' \
      -e 's#\]\(\.\./README\.md\)#](../en/overview.md)#g;' \
      -e 's#\]\(\.\./AGENT\.md\)#](../en/agent.md)#g;' \
      -e 's#\]\(roadmap\.md\)#](../en/roadmap.md)#g;' \
      -e 's#\]\(deployment\.md\)#](../en/deployment.md)#g;' \
      -e 's#\]\(dispatch-threat-model\.md\)#](../en/dispatch-threat-model.md)#g;' \
      -e 's#\]\(roadmap\.zh-CN\.md\)#](roadmap.md)#g;' \
      -e 's#\]\(deployment\.zh-CN\.md\)#](deployment.md)#g;' \
      -e 's#\]\(dispatch-threat-model\.zh-CN\.md\)#](dispatch-threat-model.md)#g;' \
      -e 's#\]\(AGENT\.zh-CN\.md\)#](agent.md)#g;' \
      -e "s#\\]\\(\\.\\./internal/protocol/protocol\\.go\\)#](${repo_url}/internal/protocol/protocol.go)#g;" \
      -e "s#\\]\\(\\.\\./LICENSE\\)#](${repo_url}/LICENSE)#g;" \
      "$output"
  fi
  rm -f "$output.bak"
}

rewrite_common README.md "$src_dir/en/overview.md" en
rewrite_common AGENT.md "$src_dir/en/agent.md" en
rewrite_common docs/roadmap.md "$src_dir/en/roadmap.md" en
rewrite_common docs/deployment.md "$src_dir/en/deployment.md" en
rewrite_common docs/dispatch-threat-model.md "$src_dir/en/dispatch-threat-model.md" en

rewrite_common docs/README.zh-CN.md "$src_dir/zh-CN/overview.md" zh
rewrite_common docs/AGENT.zh-CN.md "$src_dir/zh-CN/agent.md" zh
rewrite_common docs/roadmap.zh-CN.md "$src_dir/zh-CN/roadmap.md" zh
rewrite_common docs/deployment.zh-CN.md "$src_dir/zh-CN/deployment.md" zh
rewrite_common docs/dispatch-threat-model.zh-CN.md "$src_dir/zh-CN/dispatch-threat-model.md" zh
