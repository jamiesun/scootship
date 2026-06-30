#!/bin/sh
set -eu

root_dir=".mdbook"
en_src="$root_dir/en/src"
zh_root="$root_dir/zh-CN"
zh_src="$zh_root/src"
repo_url="https://github.com/jamiesun/scootship/blob/main"

rm -rf "$root_dir"
mkdir -p "$en_src/assets" "$zh_src/assets" "$zh_root/theme"
cp docs/assets/hero.png "$en_src/assets/hero.png"
cp docs/assets/hero.png "$zh_src/assets/hero.png"
cp docs/mdbook/language-switch.css "$zh_root/theme/language-switch.css"
cp docs/mdbook/language-switch.js "$zh_root/theme/language-switch.js"

cat > "$zh_root/book.toml" <<'EOF'
[book]
title = "scootship 文档"
authors = ["scootship contributors"]
language = "zh-CN"
src = "src"

[build]
build-dir = "book"
create-missing = false

[output.html]
default-theme = "coal"
preferred-dark-theme = "coal"
site-url = "/scootship/zh-CN/"
git-repository-url = "https://github.com/jamiesun/scootship"
additional-css = ["theme/language-switch.css"]
additional-js = ["theme/language-switch.js"]
no-section-label = true
EOF

cat > "$en_src/SUMMARY.md" <<'EOF'
# Summary

- [Start here](index.md)
- [Project roadmap](roadmap.md)
- [Deployment, backup, and recovery](deployment.md)
- [E2 dispatch threat model](dispatch-threat-model.md)
- [Agent engineering guide](agent.md)
EOF

cat > "$zh_src/SUMMARY.md" <<'EOF'
# Summary

- [从这里开始](index.md)
- [项目路线图](roadmap.md)
- [部署、备份与恢复](deployment.md)
- [E2 派发威胁模型](dispatch-threat-model.md)
- [Agent 工程指南](agent.md)
EOF

cat > "$en_src/index.md" <<'EOF'
# Scootship documentation

![scootship — fleet command center](assets/hero.png)

Scootship is a small management center for a fleet of Scoot agents. It gives operators one place to see node health, recent audit activity, token status, and operational signals without turning the center into a remote-command console.

If you are opening these docs for the first time, start with the local demo below, then read the safety boundaries before deploying it near real audit data.

## Try it locally

Run the center in one terminal:

```sh
SCOOTSHIP_DEV=1 go run ./cmd/scootship serve
```

Run the mock edge in another terminal:

```sh
go run ./cmd/scootship mock-edge -ship-audit
```

Open <http://localhost:8080> and sign in with `admin` / `admin` in dev mode. You should see the demo node come online and recent mock audit events appear on the node detail page.

## What to read next

- **Need the product direction?** Read the [project roadmap](roadmap.md). It explains what Scootship is allowed to become — and what it must never become.
- **Deploying it for real?** Read [deployment, backup, and recovery](deployment.md) before pointing real nodes at the center.
- **Thinking about job dispatch?** Read the [E2 dispatch threat model](dispatch-threat-model.md) first. Dispatch is intentionally not enabled in Phase 1.
- **Changing code?** Read the [agent engineering guide](agent.md) so changes stay inside the project boundaries.

## Important boundaries

Scootship is observation-first today:

- The center receives append-only telemetry; it does not write state back to nodes.
- Every node uses its own bearer token, and that token may only speak for its own node id.
- The dashboard is authenticated with a login session and protected mutations require CSRF checks.
- The center never raises a node's local policy ceiling.
- The `/jobs/lease` endpoint is present but dispatches no jobs in Phase 1.

Use the language switch in the top navigation bar to open the Chinese version of the same page.
EOF

cat > "$zh_src/index.md" <<'EOF'
# Scootship 文档

![scootship — 车队控制中心](assets/hero.png)

Scootship 是一个面向 Scoot agent 车队的小型管理中心。它让运维者在一个地方查看节点健康、最近审计活动、token 状态和运行信号，但不会把中心变成远程命令控制台。

如果你第一次打开这份文档，建议先跑下面的本地演示，再阅读安全边界，然后再考虑接入真实审计数据。

## 先在本地跑起来

在第一个终端运行中心：

```sh
SCOOTSHIP_DEV=1 go run ./cmd/scootship serve
```

在第二个终端运行模拟边缘节点：

```sh
go run ./cmd/scootship mock-edge -ship-audit
```

打开 <http://localhost:8080>，dev 模式下使用 `admin` / `admin` 登录。你应该能看到演示节点上线，并在节点详情页看到最近的模拟审计事件。

## 接下来读什么

- **想了解产品方向？** 读 [项目路线图](roadmap.md)。它说明 Scootship 可以成为什么，以及绝不能变成什么。
- **准备生产部署？** 在让真实节点连接中心前，先读 [部署、备份与恢复](deployment.md)。
- **正在考虑任务派发？** 先读 [E2 派发威胁模型](dispatch-threat-model.md)。阶段一有意不启用派发。
- **准备改代码？** 读 [Agent 工程指南](agent.md)，确保改动不越过项目边界。

## 必须记住的边界

今天的 Scootship 优先做观测：

- 中心只接收 append-only 遥测，不把状态写回节点。
- 每个节点使用独立 bearer token，且 token 只能代表自己的节点 ID。
- 仪表盘使用登录会话鉴权，已登录后的状态变更必须通过 CSRF 校验。
- 中心绝不抬高节点本地策略天花板。
- `/jobs/lease` 端点已经存在，但阶段一不派发任何作业。

使用顶部导航栏里的语言切换按钮，可以打开同一页面的英文版本。
EOF

rewrite_common() {
  input="$1"
  output="$2"
  lang="$3"
  cp "$input" "$output"

  if [ "$lang" = "en" ]; then
    perl -0pi.bak \
      -e 's#\]\(docs/README\.zh-CN\.md\)#](zh-CN/)#g;' \
      -e 's#\]\(docs/AGENT\.zh-CN\.md\)#](zh-CN/agent.html)#g;' \
      -e 's#\]\(docs/roadmap\.md\)#](roadmap.md)#g;' \
      -e 's#\]\(docs/deployment\.md\)#](deployment.md)#g;' \
      -e 's#\]\(docs/dispatch-threat-model\.md\)#](dispatch-threat-model.md)#g;' \
      -e 's#\]\(README\.md\)#](index.md)#g;' \
      -e 's#\]\(AGENT\.md\)#](agent.md)#g;' \
      -e 's#\]\(roadmap\.zh-CN\.md\)#](zh-CN/roadmap.html)#g;' \
      -e 's#\]\(deployment\.zh-CN\.md\)#](zh-CN/deployment.html)#g;' \
      -e 's#\]\(dispatch-threat-model\.zh-CN\.md\)#](zh-CN/dispatch-threat-model.html)#g;' \
      -e "s#\\]\\(internal/protocol/protocol\\.go\\)#](${repo_url}/internal/protocol/protocol.go)#g;" \
      -e "s#\\]\\(LICENSE\\)#](${repo_url}/LICENSE)#g;" \
      "$output"
  else
    perl -0pi.bak \
      -e 's#\]\(\.\./README\.md\)#](../)#g;' \
      -e 's#\]\(\.\./AGENT\.md\)#](../agent.html)#g;' \
      -e 's#\]\(roadmap\.md\)#](../roadmap.html)#g;' \
      -e 's#\]\(deployment\.md\)#](../deployment.html)#g;' \
      -e 's#\]\(dispatch-threat-model\.md\)#](../dispatch-threat-model.html)#g;' \
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

rewrite_common AGENT.md "$en_src/agent.md" en
rewrite_common docs/roadmap.md "$en_src/roadmap.md" en
rewrite_common docs/deployment.md "$en_src/deployment.md" en
rewrite_common docs/dispatch-threat-model.md "$en_src/dispatch-threat-model.md" en

rewrite_common docs/AGENT.zh-CN.md "$zh_src/agent.md" zh
rewrite_common docs/roadmap.zh-CN.md "$zh_src/roadmap.md" zh
rewrite_common docs/deployment.zh-CN.md "$zh_src/deployment.md" zh
rewrite_common docs/dispatch-threat-model.zh-CN.md "$zh_src/dispatch-threat-model.md" zh
