# Scootship 项目画像与方向

[English](roadmap.md) | **简体中文**

> 本文档定义 Scootship «应该成为什么样、绝不能变成什么样»，作为后续开发的北极星和护栏。
> 它描述目标与边界，不规定具体施工步骤；技术选型除非被列为硬边界，否则均为建议，决策权留给执行者。

## 项目概述

Scootship 是为 [Scoot](https://github.com/jamiesun/scoot) 车队设计的**管理中心（management center）**。

Scoot 本身是一个纯 Zig 编写、本地优先、单二进制的 AI agent 守护进程 / CLI：它把一个目标或定时任务变成可审计的系统级动作（shell、文件、搜索、HTTP），全程留下 JSONL 审计痕迹，且默认拒绝危险操作。今天每一个 Scoot 实例都是一座孤岛，要观察或下发任务只能逐台 SSH。

Scootship 就是 Scoot 文档 `docs/EDGE.md` 里反复提到的那个「未来的控制台 / management center」。它通过 **`scoot-edge` 对接协议**接收车队上报的健康状态与审计日志，并（在明确开启后）向车队派发以「目标」表达的任务，让运维者在一个标准 admin dashboard 上观测和编排整支 Scoot 车队。

核心运行方式遵循 `scoot-edge` 已经冻结的拓扑与授权模型：

- **边缘只向外拨号，中心永不回拨。** `scoot-edge` 主动连出到中心；中心是服务端，从不反向连接边缘。边缘不开任何入站端口。
- **中心是车队里唯一的可信入站面。** 因为中心暴露被连入的服务端，安全重量落在**授权模型**而非传输上：每节点独立 bearer token、强制 HTTPS、所有上行数据 append-only 且永不回写本地状态。
- **中心的权力有天花板，且天花板设在每台机器本地。** 中心下发的任务默认 `readonly`，只能被本地配置「降权」，中心永远无法抬高任何节点的本地策略上限。

### 架构图

```text
                         Operators (浏览器)
                               |
                               v   HTTPS · 仪表盘登录鉴权 / 操作授权
   +------------------------------------------------------------------+
   |                  Scootship  (单个 Go 二进制)                     |
   |                                                                  |
   |   嵌入式 Web UI (embed.FS) ── 标准 Admin Dashboard               |
   |        |                                                         |
   |   Center HTTP API                                                |
   |    - 边缘入站 (TLS 服务端 · 每节点 bearer token 鉴权)            |
   |        * POST /telemetry   ← status 心跳 / audit_batch (E1)      |
   |        * GET  /jobs/lease  → 任务派发 long-poll        (E2)      |
   |    - 节点注册表 & 令牌签发/吊销                                  |
   |    - 任务编排 & 作业生命周期 (E2)                                |
   |    - 审计存储 (车队上报审计 + 中心派发溯源)                      |
   |        |                                                         |
   |   嵌入式存储 (建议 SQLite，单文件、append-only 友好)            |
   +-------------------------------^----------------------------------+
                                   |  边缘向外拨号 (HTTPS + 每节点令牌)
                                   |  NDJSON · 上行 append-only
              +--------------------+--------------------+
              |                    |                    |
         scoot-edge           scoot-edge           scoot-edge   (可选 · 默认不安装 · 见下方前提)
              |                    |                    |
            scoot                scoot                scoot       (本地优先 agent)
        logs/*.jsonl         logs/*.jsonl         logs/*.jsonl
```

### 关键前提与依赖（待核验 / 协同风险）

- **`scoot-edge` 目前是 E0：只有设计、没有代码。** `EDGE.md` 明确写着「No `scoot-edge` code exists yet」，且授权模型与红线必须在写任何边缘代码前签字确认。这意味着 Scootship 在很长一段时间里**没有真实的边缘可对接**。因此「能对着协议契约工作」「自带一个可模拟边缘上报与领取任务的测试装置（mock edge）」是 Scootship 早期的硬需求，而不是锦上添花。
- **协议契约以 `EDGE.md` 为准（`v:1`）。** 信封形如 `{"v":1,"type":"status|audit_batch|job|job_event","node_id":"...","sent_ts":<ms>,"body":{}}`；帧格式是 NDJSON。Scootship 必须把这份契约固化成一个独立、带版本的对接模块，并与 Scoot 侧 `scoot-edge` 的演进保持锁步。
- **审计事件 schema 取自 Scoot `src/audit.zig`**：`{"seq","ts","session_id","run_id?","kind","msg"}`，`kind ∈ {run,thought,tool_call,observation,final,policy_deny,system_error}`。Scootship 摄入时必须按 `EDGE.md` 的幂等游标 `{file_gen, byte_offset, seq}` 去重，append-only 存储，重复区间视为 no-op。

## 项目画像（目标状态）

做好之后，Scootship 应该是这样：

- **车队一眼可见。** 运维者打开仪表盘，能立刻看清：哪些节点在线、各自最近心跳、Scoot/edge 版本、每节点的本地策略天花板（`policy_ceiling`）、审计事件统计与趋势、以及（开启后）节点的标签与能力画像。观测是第一阶段的全部重点。
- **单二进制即可交付。** 像 Scoot 一样，Scootship 应当是「拷贝一个可执行文件就能跑」。Web 前端通过 Go 的 `embed.FS` 编进同一个二进制，不另起独立前端进程、不依赖外部静态服务器或 Node 运行时。
- **中心是被严密防守的入站面。** 因为边缘从不开端口、只有中心被连入，中心就是车队里唯一新增的可信攻击面。每个入站端点都必须鉴权（每节点 bearer token）、限流、限制请求体大小、设硬超时。强制 HTTPS，哪怕在 VPC 内网。
- **审计可重放、派发可溯源。** 中心存两层可审计事实：① 从车队摄入的 Scoot 运行审计，能事后逐行重放一次 agent 究竟做了什么；② 中心自身的派发溯源（谁、在什么时候、把哪个目标、以什么 `effective_policy`、派给了哪个节点，并通过 `session_id` 关联回 Scoot 的运行审计）。
- **简单优先于花哨。** admin dashboard 选**一种**低复杂度前端方案即可（建议：Go 模板 + HTMX/Alpine 这类无构建步骤、对 embed 友好的方案；一个很小的预编译 SPA 也可接受）。不追求组件库齐全、不追求炫酷交互。
- **数据敏感性被当回事。** `audit_batch` 可能携带文件内容、命令输出、HTTP 响应体——一旦开启，中心就成了潜在敏感观测数据的汇聚地。中心必须对仪表盘访问做自己的登录鉴权与操作授权，并对存储的审计数据有保留/清理策略。

品质冲突时的优先级（高到低）：

1. **安全与可控**——宁可少一个功能，也不能让中心抬高某节点本地策略上限、不能回写 Scoot 本地状态、不能把节点令牌编进二进制或写进日志。
2. **观测的真实与不丢不重**——摄入必须幂等、append-only；游标只在中心 ack 后推进；宁可显式标记 `audit_gap`，也不静默丢数据。
3. **交付与运维的简单**——单二进制、嵌入式资源、简单存储，胜过架构华丽。
4. **功能广度**——编排、告警、富审计 UI 等都排在以上之后。

## 当前能力清单

阶段一（观测与基础框架，对齐 scoot-edge E1）已落地，可运行、可测试：

- **协议对接模块。** `EDGE.md` 的 `v:1` 信封、`status` / `audit_batch` 形状与审计 schema 固化于 `internal/protocol`；未知字段忽略、未知主版本拒绝。
- **遥测摄入与幂等存储。** `POST /telemetry` 摄入 `status` 心跳与 `audit_batch`，按 `{file_gen, byte_to}` 幂等游标去重、append-only 落库并在启动时重放（`internal/center`、`internal/store`）。
- **节点注册表与令牌鉴权。** 每节点独立 bearer token 鉴权，令牌只能为自身 `node_id` 说话（`internal/tokens`、`internal/center`）。
- **观测仪表盘。** 嵌入式 admin dashboard（车队总览、节点详情、可收拢的左侧菜单），通过 `embed.FS` 编入单二进制（`internal/web`、`internal/center`）。
- **仪表盘登录与防暴破。** 表单登录 + HttpOnly cookie 会话，登录失败按来源 IP 滑动窗口限流与锁定，辅以严格的安全响应头（`internal/center`、`internal/loginguard`）。
- **mock edge 测试装置。** 在没有真实 `scoot-edge` 的情况下模拟心跳与审计上报，打通端到端链路（`internal/mockedge`、`cmd/scootship mock-edge`）。
- **CLI 与配置。** `scootship serve | mock-edge | version`，全部经 `SCOOTSHIP_*` 环境变量配置（`cmd/scootship`、`internal/config`）。

阶段二（任务编排与派发）尚未实现：`GET /jobs/lease` 目前是观测期占位，按契约鉴权但不派发任何作业。

> 说明：本节只记录已经存在的事实。后续每落地一项能力，应把它从「方向」移动到这里，并标注入口或证据路径。

## 非目标（铁律）

除非在本文档中显式修改边界，否则以下都是不可越界的硬规则：

- **不配置、不修改 Scoot 的本地执行策略 / 权限上限。** `guarded/readonly/unrestricted` 的天花板是每台机器的**本地** opt-in。Scootship 派发任务时只能请求「不高于」节点本地上限的策略，由节点自己 clamp；中心永远无法抬高任何节点的 `policy_ceiling`。这是用户明确划定的边界，也是 `EDGE.md` 的红线。
- **不回写、不反向同步 Scoot 本地状态。** 所有上行遥测都是 append-only、只读摄入；中心绝不把自身当作可写真相源去和边缘做双向状态对账。这不是「云同步」。
- **不在节点上执行原始命令。** 任务只能以 `kind=run` 的「目标」作为**数据**下发，由 Scoot 像本地输入一样重新校验。中心绝不合成 shell / eval，绝不提供任意远程命令执行通道。
- **不反向拨号进入边缘。** 中心纯粹是服务端；连接永远由边缘发起。中心不持有、不探测各边缘地址，不开反连。
- **不做面向公网的多租户 SaaS / 计费系统。** 沿用 `EDGE.md` 的 VPC 内网部署假设；即便网络可信，特权层面仍按不可信对待（纵深防御）。
- **不把前端做成独立进程或重型前端工程。** Web 资源必须 `embed` 进单二进制；不引入会破坏「单文件交付」的重型运行时、构建链或微服务拆分。
- **不把密钥编进产物。** 节点 bearer token、TLS 私钥等绝不编译进二进制、不提交进仓库、不打印、不写入任何审计或日志（对齐 Scoot constraint 7）。
- **不绕过协议契约私自扩展 Scoot。** Scootship 只通过 `scoot-edge` 公共协议面与车队交互，不依赖 Scoot 的内部实现细节，不要求 Scoot 暴露私有子系统。

## 方向与意图

> 用户已按「阶段」组织需求，故以下方向与 `scoot-edge` 的 E1 / E2 阶段对齐，并标注各方向服务于哪条画像或边界。每条用结果表达，不规定具体实现顺序。

### 阶段一 · 观测与基础框架（对齐 scoot-edge E1）

服务于画像「车队一眼可见」「单二进制交付」「中心是被严密防守的入站面」。

- **可对着契约工作的对接底座。** 把 `EDGE.md` 的 `v:1` 信封、`status` / `audit_batch` 形状、审计 schema 固化成带版本的协议模块；未知字段忽略、未知主版本拒绝。
- **mock edge 测试装置。** 因为真实 `scoot-edge` 尚不存在，Scootship 必须能在没有真实边缘的情况下开发与验证：一个能模拟节点心跳、能力描述符与 `audit_batch` 上报、并能领取 / 回报作业的测试边缘。
- **节点注册表与令牌治理。** 中心侧签发、保存（安全地）、识别、限流、吊销**每节点独立** token——这是中心自身的治理面，区别于「Scoot 权限配置」，不违背上面的铁律。
- **遥测摄入（先心跳，后日志体）。** 先把 `status` 心跳跑通（版本、daemon 状态、`policy_ceiling`、`audit_stats`）；`audit_batch` 摄入按幂等游标去重、append-only 落库，并尊重「默认关闭、按 kind 白名单」的上行约束。
- **观测仪表盘。** 一个低复杂度的 admin dashboard：车队总览、节点详情、版本漂移、策略天花板、审计统计趋势，以及（开启后）标签 / 能力画像。
- **可被严密防守的入站服务。** 强制 HTTPS（中心持服务端证书，或在可信反代终止 TLS）、每端点鉴权、限流、限请求体、硬超时；仪表盘自身的登录鉴权与操作授权——表单登录 + HttpOnly cookie 会话，并对登录失败**按来源 IP 滑动窗口限流与锁定**以抵御暴力破解（来源 IP 只在配置了可信代理时才采信 `X-Forwarded-For`），辅以严格的安全响应头（CSP、`X-Frame-Options` 等）。

### 阶段二 · 任务编排与派发（对齐 scoot-edge E2）

服务于画像「派发可溯源」与铁律「中心不抬高本地上限 / 不执行原始命令」。

- **基于 long-poll 的作业派发。** 实现 `GET /jobs/lease?node=&capacity=` 一侧的中心语义：按节点最近的能力 / 标签描述符路由作业；匹配不上则以 `no_matching_capability` 拒绝，能力不符只降级为 reject，绝不降级为不安全执行。
- **作业生命周期与幂等。** 跟踪 `accepted/running/done/failed/rejected`，携带 `idem_key` 保证「同一作业只跑一次」，遵守容量背压（`at_capacity`）、`deadline_ts` 与重试上限。
- **只降不升的策略表达。** 派发时只能请求 `≤` 节点本地上限的策略，默认 `readonly`；中心 UI / API 不提供任何「抬高节点上限」的入口。
- **派发溯源审计。** 中心侧记录每次派发的来龙去脉，并通过 `session_id` 关联回摄入的 Scoot 运行审计，形成端到端可追溯链路。

### 阶段三 · 审计深化与运维成熟度

服务于画像「审计可重放」「数据敏感性被当回事」。

- **运行审计重放视图。** 把摄入的 JSONL 审计按 `seq/ts` 重建成一次运行的可读时间线（thought / tool_call / observation / policy_deny / final）。
- **保留与生命周期。** 对敏感审计体的保留期、容量上限、清理与 `audit_gap` 可见性提供运维手段。
- **告警与健康。** 节点离线、版本漂移、`policy_deny` 激增、审计断流等可观测信号的提醒能力。
- **多运维者治理。** 仪表盘的角色 / 访问控制成熟化（仍是中心自身治理，不等于 Scoot 权限配置）。

## 完成的样子

> 当下列可观察结果出现时，对应方向才算真正达成。具体技术手段（SQLite、HTMX、模板、e2e 测试等）是举例与建议，由执行者按实际选择。

- **协议对接是真实可验证的。** 即便真实 `scoot-edge` 尚未发布，Scootship 也能用自带的 mock edge 跑通「心跳 → 摄入 → 仪表盘可见」与「领取作业 → 回报生命周期」的完整链路，并有自动化测试守护这条核心数据流。
- **观测一眼可达。** 运维者无需查库即可在仪表盘看到车队在线情况、版本、策略天花板与审计统计；新节点上报后能自动出现在注册表中。
- **摄入不丢不重。** 重复投递的 `audit_batch` 区间是 no-op；游标只在中心 ack 后推进；超出保留上限时产生显式 `audit_gap` 标记而非静默丢失。
- **边界自动守住。** 任何试图「抬高节点本地策略上限」「回写 Scoot 本地状态」「在节点上跑原始命令」「反向连入边缘」的路径在设计上不存在；派发只能降权。
- **入站面是安全的。** 所有边缘端点强制 HTTPS 且必须携带有效的每节点令牌；令牌可被单独吊销而不影响全队；密钥不出现在二进制、仓库、日志或审计中。
- **交付是单文件的。** 构建产物是一个内嵌前端资源的 Go 二进制，拷贝即可运行，启动不依赖外部前端进程或 Node 工具链。
- **派发可端到端追溯。** 任一中心派发的作业都能通过 `session_id` 串联到对应 Scoot 实例的运行审计，回答「谁、把什么、以什么策略、派给了谁、结果如何」。
