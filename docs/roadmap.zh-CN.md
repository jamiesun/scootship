# Scootship 项目画像与方向

[English](roadmap.md) | **简体中文**

> 本文档定义 Scootship «应该成为什么样、绝不能变成什么样»，作为后续开发的北极星和护栏。
> 它描述目标与边界，不规定具体施工步骤。技术选型除非被明确绑定到下方铁律，否则只是建议。安全、协议、派发、存储与交付边界不是实现偏好，而是门禁。

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

- **`scoot-edge` 是可选伴生程序，且可能滞后于中心契约。** Scoot 仓库已经发布了 E1 edge 伴生程序、
  无人值守策略 clamp，并且从 `scoot-edge v0.8.0` 起已发布完整的 E2 作业派发，包括
  `edge.job_root` cwd confinement。因此「能对着协议契约工作」「自带一个可模拟边缘上报与领取任务的测试
  装置（mock edge）」仍是 Scootship 的硬需求，而不是锦上添花——scootship 不能假设本地一定
  装了某个特定版本的 edge。
- **协议契约以 `EDGE.md` 为准（`v:1`）。** 信封形如 `{"v":1,"type":"status|audit_batch|job|job_event","node_id":"...","sent_ts":<ms>,"body":{}}`；帧格式是 NDJSON。Scootship 必须把这份契约固化成一个独立、带版本的对接模块，并与 Scoot 侧 `scoot-edge` 的演进保持锁步。
- **审计事件 schema 取自 Scoot `src/audit.zig`**：`{"seq","ts","session_id","run_id?","kind","msg"}`，`kind ∈ {run,thought,tool_call,observation,final,policy_deny,system_error}`。Scootship 摄入时必须按 `EDGE.md` 的幂等游标 `{file_gen, byte_offset, seq}` 去重，append-only 存储，重复区间视为 no-op。

## 项目画像（目标状态）

做好之后，Scootship 应该是这样：

- **车队一眼可见。** 运维者打开仪表盘，能立刻看清：哪些节点在线、各自最近心跳、Scoot/edge 版本、每节点的本地策略天花板（`policy_ceiling`）、审计事件统计与趋势、以及（开启后）节点的标签与能力画像。观测是第一阶段的全部重点。
- **单二进制即可交付。** 像 Scoot 一样，Scootship 应当是「拷贝一个可执行文件就能跑」。Web 前端通过 Go 的 `embed.FS` 编进同一个二进制，不另起独立前端进程、不依赖外部静态服务器或 Node 运行时。
- **中心是被严密防守的入站面。** 因为边缘从不开端口、只有中心被连入，中心就是车队里唯一新增的可信攻击面。每个入站端点都必须鉴权（每节点 bearer token）、限流、限制请求体大小、设硬超时。强制 HTTPS，哪怕在 VPC 内网。
- **审计可重放、派发可溯源。** 中心存两层可审计事实：① 从车队摄入的 Scoot 运行审计，能事后逐行重放一次 agent 究竟做了什么；② 中心自身的派发溯源（谁、在什么时候、把哪个目标、以什么 `effective_policy`、派给了哪个节点，并通过 `session_id` 关联回 Scoot 的运行审计）。
- **简单优先于花哨。** admin dashboard 选**一种**低复杂度前端方案即可。默认是 Go 模板 + 少量从 `embed.FS` 提供的静态 JS/CSS。预编译 SPA 只有在构建可复现、依赖锁定、资源嵌入同一个二进制、运行时不依赖 CDN / 外部资产、且不会把 CI 或 release 变成独立前端产品时才可接受。不追求组件库齐全、不追求炫酷交互。
- **数据敏感性被当回事。** `audit_batch` 可能携带文件内容、命令输出、HTTP 响应体——一旦开启，中心就成了潜在敏感观测数据的汇聚地。中心必须对仪表盘访问做自己的登录鉴权与操作授权，并对存储的审计数据有保留/清理策略。
- **运维可信度先于远程编排。** 中心侧 E2 派发代码在明确门禁后才生长：生产 / dev 传输边界经过测试，
  部署与恢复文档化，审计生命周期和 gap 有实现，节点认证可治理，健康信号能在仪表盘上看清。这些
  门禁满足、且 Scoot 命名了兼容的 edge 契约版本后，面向操作员的派发**创建**已经开放；派发
  **控制**（取消/重试）仍待其自身的设计与威胁模型工作后再开放。只读派发审计继续展示
  provenance，且不会比创建本身已带来的能力面更进一步。

品质冲突时的优先级（高到低）：

1. **安全与可控**——宁可少一个功能，也不能让中心抬高某节点本地策略上限、不能回写 Scoot 本地状态、不能把节点令牌编进二进制或写进日志。
2. **观测的真实与不丢不重**——摄入必须幂等、append-only；游标只在中心 ack 后推进；宁可显式标记 `audit_gap`，也不静默丢数据。
3. **交付与运维的简单**——单二进制、嵌入式资源、简单存储，胜过架构华丽。
4. **运维成熟度**——生产安全默认值、部署清晰度、审计生命周期、令牌治理与健康信号，优先级高于新增权力面。
5. **功能广度**——编排、告警、富审计 UI 等都排在以上之后。

## 当前能力清单

阶段一（观测与基础框架，对齐 scoot-edge E1）已落地，可运行、可测试：

- **协议对接模块。** `EDGE.md` 的 `v:1` 信封、`status` / `audit_batch` 形状与审计 schema 固化于 `internal/protocol`；未知字段忽略、未知主版本拒绝。
- **遥测摄入与幂等存储。** `POST /telemetry` 摄入 `status` 心跳与 `audit_batch`，按 `{file_gen, byte_to}` 幂等游标去重、append-only 落库；无效审计游标、空审计批次和未知审计事件 kind 会在修改状态前被拒绝，并在启动时重放（`internal/protocol`、`internal/center`、`internal/store`）。
- **审计保留窗口与 gap 可见。** 中心为 API/仪表盘按节点保留可配置的最近审计窗口（`SCOOTSHIP_AUDIT_RETENTION_EVENTS`），同时将已接受事件保存在 append-only JSONL 日志中；保留窗口溢出会在节点生命周期状态中显式显示中心侧 `audit_gap`（`internal/store`、`internal/center`、`internal/web`）。
- **保留审计的运行时间线。** 节点详情 API / 页面会按 `session_id` / `run_id` 对保留的审计事件分组，并在每个运行内按 `seq` / `ts` 排序，使最近 agent 行为无需打开原始 JSONL 也能阅读（`internal/store`、`internal/center`、`internal/web`）。
- **只读健康信号。** 车队与节点视图会为离线 / 过期节点、版本漂移、审计正文滞后、审计保留缺口、重复审计上报、策略拒绝、系统错误和本地 unrestricted 上限派生仪表盘可见信号，但不增加任何处置路径（`internal/center`、`internal/web`）。
- **节点注册表、令牌鉴权与令牌生命周期。** 每节点独立 bearer token 鉴权，令牌只能为自身 `node_id` 说话；
  仪表盘操作员可以创建、轮换和撤销中心托管的节点认证令牌。仪表盘只在创建 / 轮换成功后一次性显示生成的
  bearer secret；常规仪表盘 / API 清单只展示安全元数据（来源、fingerprint、最近认证时间）
  （`internal/tokens`、`internal/center`、`internal/web`）。
- **观测仪表盘。** 嵌入式 admin dashboard（车队总览、节点详情、令牌清单、可收拢的左侧菜单），通过 `embed.FS` 编入单二进制（`internal/web`、`internal/center`）。
- **仪表盘登录与操作员治理。** 表单登录 + HttpOnly cookie 会话，可选「记住此设备」长期会话（绝不保存密码），
  支持直接内置能力（`fleet:view`、`tokens:manage`、`operators:manage`）、已登录状态变更的会话绑定
  CSRF 校验、资料/密码修改，并对登录失败按来源 IP 滑动窗口限流与锁定，辅以严格的安全响应头
  （`internal/center`、`internal/operators`、`internal/loginguard`）。
- **mock edge 测试装置。** 在没有真实 `scoot-edge` 的情况下模拟心跳与审计上报，打通端到端链路（`internal/mockedge`、`cmd/scootship mock-edge`）。
- **CLI 与配置。** `scootship serve | mock-edge | version`，全部经 `SCOOTSHIP_*` 环境变量配置；生产模式明文 HTTP 默认 fail-closed，除非配置直连 TLS、显式 dev 模式，或显式可信 TLS 反代模式（`cmd/scootship`、`internal/config`）。
- **部署、备份与恢复手册。** 运维文档覆盖直连 TLS 与可信 TLS 反代模式、私有数据目录和 token 文件权限、
  systemd 运行方式、敏感备份内容、恢复步骤与恢复后验证（`docs/deployment.zh-CN.md`）。
- **CI、docs、release 自动化与项目 skills。** GitHub Actions 运行 CI、构建并部署双语 mdBook 文档站点，并通过标签驱动的 release 构建产出跨平台单二进制 archives 与校验和；项目本地 skills 记录受控 release 编排和全项目审计流程（`.github/workflows`、`.agents/skills`）。

阶段二中心侧派发核心已落地，面向操作员的派发**创建**现已开放；派发**控制**
（取消/重试/编辑）仍未构建：

- **持久化派发队列与 provenance。** append-only JSONL store 现在持久化派发任务快照，启动时可重播恢复，
  按 `idem_key` 去重，并保留 requestor、目标节点、deadline、所需 labels/tools/skills、生命周期阶段、
  session 关联、effective policy 与 reject reason（`internal/store`）。
- **节点绑定 lease 派发。** `GET /jobs/lease` 仍按每节点 bearer token 鉴权，校验 `node` 查询参数与有界
  `capacity`，并只把已经持久化且绑定到该已鉴权节点的任务作为 `job` 信封返回（`internal/center/lease.go`）。
- **只降不升策略与能力不匹配拒绝。** 派发任务默认 `readonly`，会降到节点上报的 `policy_ceiling` 以内，
  并把无人值守的 `guarded` 折叠为 `readonly`；label/tool/skill 不匹配会以 `no_matching_capability`
  拒绝，而不是落入不安全执行（`internal/store`）。
- **生命周期遥测校验。** `job_event` body 会在修改状态前校验，并从 append-only 遥测更新派发生命周期 /
  provenance（`internal/protocol`、`internal/center/telemetry.go`、`internal/store`）。
- **面向操作员的派发创建。** 拥有 `dispatch:manage` 能力的仪表盘操作员可以从 `/dispatch/new` 创建新的、
  面向单个节点的派发任务：表单有会话与 CSRF 保护，会在服务端校验目标节点 / goal / 策略 /
  截止时间 / 重试次数的取值范围，总是把请求策略夹紧到该节点自身上报的上限，并受可配置的每节点
  待处理任务队列上限（`SCOOTSHIP_DISPATCH_QUEUE_LIMIT`、`store.ErrDispatchQueueFull`）约束，
  避免单个节点的队列无限增长（`internal/center/dispatch_create.go`、`internal/store`）。
- **只读派发审计仍保持只读。** `/dispatch` 列表和 `/api/dispatch` JSON 视图仍然只用于查看：可以看到
  生命周期阶段、目标节点、请求 / 实际策略、幂等键、拒绝原因和 `session_id` 关联，但对已存在的
  任务仍没有内联的编辑 / 取消 / 重试控制（`internal/center`、`internal/web`）。

派发**控制**——取消、重试或编辑一个已排队的任务——仍未构建。加入它属于阶段二的方向性工作，尚不是
当前能力。

> 说明：本节只记录已经存在的事实。后续每落地一项能力，应把它从「方向」移动到这里，并标注入口或证据路径。

## 非目标（铁律）

除非通过下方门禁显式修改边界，否则以下都是不可越界的硬规则：

- **不配置、不修改 Scoot 的本地执行策略 / 权限上限。** `guarded/readonly/unrestricted` 的天花板是每台机器的**本地** opt-in。Scootship 派发任务时只能请求「不高于」节点本地上限的策略，由节点自己 clamp；中心永远无法抬高任何节点的 `policy_ceiling`。这是用户明确划定的边界，也是 `EDGE.md` 的红线。
- **不回写、不反向同步 Scoot 本地状态。** 所有上行遥测都是 append-only、只读摄入；中心绝不把自身当作可写真相源去和边缘做双向状态对账。这不是「云同步」。
- **不在节点上执行原始命令。** 任务只能以 `kind=run` 的「目标」作为**数据**下发，由 Scoot 像本地输入一样重新校验。中心绝不合成 shell / eval，绝不提供任意远程命令执行通道。
- **不反向拨号进入边缘。** 中心纯粹是服务端；连接永远由边缘发起。中心不持有、不探测各边缘地址，不开反连。
- **不做面向公网的多租户 SaaS / 计费系统。** 沿用 `EDGE.md` 的 VPC 内网部署假设；即便网络可信，特权层面仍按不可信对待（纵深防御）。
- **不把前端做成独立进程或重型前端工程。** Web 资源必须 `embed` 进单二进制；不引入会破坏「单文件交付」的重型运行时、构建链或微服务拆分。
- **不把密钥编进产物。** 节点 bearer token、TLS 私钥等绝不编译进二进制、不提交进仓库、不打印、不写入任何审计或日志（对齐 Scoot constraint 7）。
- **不绕过协议契约私自扩展 Scoot。** Scootship 只通过 `scoot-edge` 公共协议面与车队交互，不依赖 Scoot 的内部实现细节，不要求 Scoot 暴露私有子系统。

### 边界变更门禁

修改上述任何铁律都不是普通文档编辑。边界变更只有在同一次改动中同时满足以下条件才有效：

- owner 明确批准新的边界，并说明新增权力为什么值得；
- Scoot 侧 `EDGE.md` 契约已先行或同步更新，且不让 Scootship 依赖 Scoot 私有内部实现；
- 改动包含聚焦的威胁模型说明，覆盖滥用路径、回滚与运维恢复；
- 测试或 CI 证明旧的不安全路径仍然不存在，除非新边界明确允许；
- 英文与中文 roadmap / agent 文档同步更新。

缺少任一项时，agent 必须继续按原非目标执行，并停止，而不是靠解释扩大范围。

## 方向与意图

> 用户已按「阶段」组织需求，故以下方向与 `scoot-edge` 的 E1 / E2 阶段对齐，并标注各方向服务于哪条画像或边界。每条用结果表达，不规定具体实现顺序。

### 阶段一 · 观测与基础框架（对齐 scoot-edge E1）

服务于画像「车队一眼可见」「单二进制交付」「中心是被严密防守的入站面」。

- **可对着契约工作的对接底座。** 把 `EDGE.md` 的 `v:1` 信封、`status` / `audit_batch` 形状、审计 schema 固化成带版本的协议模块；未知字段忽略、未知主版本拒绝。
- **mock edge 测试装置。** Scootship 必须能在没有真实边缘联调的情况下开发与验证：一个能模拟节点心跳、
  能力描述符与 `audit_batch` 上报的测试边缘。阶段一用 lease 轮询验证已鉴权、绑定节点、capacity 有界的
  空派发契约；阶段二中心侧测试现在覆盖已持久化、节点绑定的 lease 输出和生命周期遥测，而真实 edge 执行仍是
  上线门禁。
- **节点注册表与令牌治理。** 中心侧创建、私有保存、识别、轮换、吊销**每节点独立**认证 token——这是中心自身的治理面，区别于「Scoot 权限配置」，不违背上面的铁律。
- **遥测摄入（先心跳，后日志体）。** 先把 `status` 心跳跑通（版本、daemon 状态、`policy_ceiling`、`audit_stats`）；`audit_batch` 摄入按幂等游标去重、append-only 落库，并尊重「默认关闭、按 kind 白名单」的上行约束。
- **观测仪表盘。** 一个低复杂度的 admin dashboard：车队总览、节点详情、版本漂移、策略天花板、审计统计趋势，以及（开启后）标签 / 能力画像。
- **可被严密防守的入站服务。** 强制 HTTPS（中心持服务端证书，或在可信反代终止 TLS）、每端点鉴权、限流、限请求体、硬超时；仪表盘自身的登录鉴权与操作授权——表单登录 + HttpOnly cookie 会话，并对登录失败**按来源 IP 滑动窗口限流与锁定**以抵御暴力破解（来源 IP 只在配置了可信代理时才采信 `X-Forwarded-For`），辅以严格的安全响应头（CSP、`X-Frame-Options` 等）。

### 阶段一半 · E1 运维成熟包

服务于画像「中心是被严密防守的入站面」「审计可重放」「数据敏感性被当回事」。这是 E2 扩大中心权力前，最应该优先推进的方向。

- **生产 / dev 传输边界。** 生产模式下对不安全传输 fail-closed，除非显式选择命名清楚的开发模式；本地联调仍应有清晰的 dev-only 路径，例如可信本机 HTTP 或一键自签 HTTPS。
- **部署与恢复清晰。** 运维者能理解并复现安全部署：TLS 直连与可信反代的边界、私有数据目录的属主和权限、节点 token 文件权限，以及 append-only 遥测和操作员状态的备份 / 恢复预期。
- **边缘端点有边界且失败模式可见。** `/telemetry` 与 `/jobs/lease` 对超时、请求体限制（包括无请求体的 lease 请求）、鉴权失败、幂等错误，以及运维可见的错误 / 健康信号有明确预期。
- **真实 edge 对接路径。** 如果真实 `scoot-edge` 客户端强制 `https://`，Scootship 的 dev 故事必须让本地 center/edge 联调路径显式成立，同时不削弱生产默认值。
- **审计生命周期与 gap 可见。** 对敏感审计体的保留、清理、容量上限和显式 `audit_gap` 行为，要在接入大规模真实车队前让运维者能理解。
- **运行审计时间线。** 仪表盘能按 `session_id`、`run_id`、`seq`、`ts` 组织摄入审计，让运维者无需读原始 JSONL，也能回答「这次 agent 运行到底做了什么」。
- **令牌治理是节点认证，不是节点策略。** 令牌生命周期与最近认证时间可视化必须始终清晰限定在节点认证范围内，绝不暗示中心有权修改节点本地 `policy_ceiling`。
- **只读健康信号。** 节点离线、版本漂移、`policy_deny` 激增、审计停滞、重复上报异常等，先成为仪表盘可见信号，再考虑通知或处置系统。

### 阶段二 · 任务编排与派发（对齐 scoot-edge E2）

服务于画像「派发可溯源」与铁律「中心不抬高本地上限 / 不执行原始命令」。中心侧核心和面向操作员的派发
**创建**现在都已经存在；派发**控制**（取消 / 重试 / 编辑一个已存在的任务）仍在刻意保留，
等待其自身的设计与威胁模型工作。

下面的 E2 **创建**门禁，是打开 `/dispatch/new` 之前必须满足的条件。它已经满足；不要把这当作
对另一个仍然关闭的**控制**面（取消 / 重试 / 编辑）的一括子批准——控制面需要它自己的门禁：

- E1 传输行为已覆盖直连 TLS、可信 TLS 反代、显式 dev 模式、明文 HTTP fail-closed 的测试。
- 真实运维者所需的部署、备份、恢复、数据目录权限、token 文件权限与故障恢复流程已有文档。
- 审计保留、容量上限与显式 `audit_gap` 行为已有代码和测试。
- token 创建 / 轮换 / 否销流程已有 e2e 或集成覆盖。
- 运行审计时间线已能关联 `session_id`、`run_id`、`seq` 与 `ts`，无需翻原始 JSONL。
- 节点离线、版本漂移、`policy_deny` 激增、审计停滞、重复上报异常等只读健康信号已先于通知或处置系统可见。
- Scoot 已发布无人值守 readonly clamp，且 Scootship 改动中明确写出兼容的 `scoot-edge` 契约版本。
- 派发威胁模型已覆盖队列滥用、重放 / 幂等、能力伪造、授权、审计溯源与回滚。

当前门禁状态：**创建已开放；控制仍需门禁**。E1 侧已有直连 TLS、可信 TLS 反代 HTTP、显式 dev
HTTP、默认明文 HTTP fail-closed 行为的代码与测试证据，也已有部署 / 恢复文档、审计保留与 gap
可见性、token 生命周期治理、运行审计时间线、只读健康信号、端点失败模式和严格遥测 body 校验的证据。
Scoot 已发布无人值守一次性钳制（`scoot --unattended -e "<goal>"`），并且从 `scoot-edge v0.8.0`
起发布了完整的 E2 作业派发，包括 `edge.job_root` cwd confinement——命名的兼容契约版本是
`scoot-edge >= v0.8.0`。Scootship 中心侧的派发队列、lease、幂等、能力不匹配拒绝和生命周期
测试早于这次发布就已经与该契约兼容。上述门禁满足后，拥有 `dispatch:manage` 能力的仪表盘
操作员现在可以从 `/dispatch/new` 创建面向单个节点的派发任务，并受每节点待处理任务队列上限
（`SCOOTSHIP_DISPATCH_QUEUE_LIMIT`）约束。派发**控制**——从仪表盘编辑、取消或重试一个已排队的
任务——仍未构建，需要它自己的门禁才能打开。

- **基于 long-poll 的作业派发。** 已为直接面向节点的任务实现
  `GET /jobs/lease?node=&capacity=` 一侧的中心语义：按节点最近的能力 / 标签描述符路由作业；匹配不上则以
  `no_matching_capability` 拒绝，能力不符只降级为 reject，绝不降级为不安全执行。
- **作业生命周期与幂等。** 中心侧 store/telemetry 路径已跟踪 queued/leased 以及
  `accepted/running/done/failed/rejected`，携带 `idem_key` 保证「同一作业只跑一次」，按节点声明
  capacity 限制 lease 输出，拒绝过期的 `deadline_ts`，并记录 edge 契约中的重试上限。显式重试调度和
  `at_capacity` 生命周期上报仍属于上线工作。
- **只降不升的策略表达。** 中心侧 enqueue 路径已实现：派发时只能请求 `≤` 节点本地上限的策略，默认
  `readonly`；中心 UI / API 不提供任何「抬高节点上限」的入口——创建表单总是只降不升。
- **面向操作员的派发创建。** 已实现：拥有 `dispatch:manage` 能力的仪表盘操作员可以从 `/dispatch/new`
  创建一个面向单个节点的任务（会话 + CSRF 保护、服务端校验、按节点队列限流）。按设计只面向单个
  节点；仍没有广域的 label/capability fan-out 创建流程。
- **派发溯源审计。** 已作为只读审计面实现：中心侧记录派发 provenance，更新 lifecycle/session 关联，
  并把队列 / provenance 暴露给操作员查看；可通过 `session_id` 关联回摄入的 Scoot 运行审计，形成端到端可溯源链路。
- **派发控制（仍是方向性工作，尚未实现）。** 从仪表盘取消、重试或编辑一个已排队的任务，需要它自己的
  威胁模型说明（能影响一个正在运行 / 排队中任务的操作员入口，风险形态与纯创建不同）之后才能加入。

### 阶段三 · 治理与规模化运维

服务于画像「中心是被严密防守的入站面」「数据敏感性被当回事」，建立在 E1 运维成熟基线之后。

- **通知与响应成熟化。** 先在仪表盘可见的健康信号，后续可以进入显式通知或事件响应流程，但不静默修改节点状态。
- **多运维者治理。** 仪表盘的角色 / 访问控制成熟化（仍是中心自身治理，不等于 Scoot 权限配置）。
- **车队规模化运维。** 随着节点数量和保留审计体增长，存储、备份、清理与仪表盘性能仍要保持可理解、可操作。

## 完成的样子

> 当下列可观察结果出现时，对应方向才算真正达成。具体技术手段（SQLite、HTMX、模板、e2e 测试等）是举例与建议，由执行者按实际选择。

- **协议对接是真实可验证的。** Scootship 能用自带的 mock edge 跑通「心跳 → 摄入 → 仪表盘可见」链路；
  测试现在也守护已鉴权、节点绑定的 lease 输出和 `job_event` 生命周期更新。完整真实 edge 的「领取作业 →
  通过 edge 运行 → 回报生命周期」验证仍是 E2 上线信号。
- **观测一眼可达。** 运维者无需查库即可在仪表盘看到车队在线情况、版本、策略天花板与审计统计；新节点上报后能自动出现在注册表中。
- **摄入不丢不重。** 重复投递的 `audit_batch` 区间是 no-op；游标只在中心 ack 后推进；超出保留上限时产生显式 `audit_gap` 标记而非静默丢失。
- **E1 先具备生产可信度，再进入 E2。** 上方 E2 派发门禁必须以代码、测试和运维文档形式勾完，再扩大面向操作员的远程派发能力。
- **边界自动守住。** 任何试图「抬高节点本地策略上限」「回写 Scoot 本地状态」「在节点上跑原始命令」「反向连入边缘」的路径在设计上不存在；派发只能降权。
- **入站面是安全的。** 所有边缘端点使用生产安全传输且必须携带有效的每节点令牌；令牌可被单独轮换或吊销而不影响全队；密钥不出现在二进制、仓库、日志或审计中。
- **交付是单文件的。** 构建产物是一个内嵌前端资源的 Go 二进制，拷贝即可运行，启动不依赖外部前端进程或 Node 工具链。
- **派发可端到端追溯。** 任一中心派发的作业都能通过 `session_id` 串联到对应 Scoot 实例的运行审计，回答「谁、把什么、以什么策略、派给了谁、结果如何」。
