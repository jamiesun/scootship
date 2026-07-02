# Scootship E2 派发威胁模型

[English](dispatch-threat-model.md) | **简体中文**

本文覆盖 EDGE.md E2 作业派发表面。中心侧队列、lease、幂等、生命周期，以及面向操作员的派发**创建**代码
现在都已经存在，并在下文覆盖。本文档是这个创建面的门禁材料，不是对未来派发工作的一揽子授权：单独的
**控制**面（取消 / 重试 / 编辑一个已排队的任务）不在本文范围内，需要在构建前有它自己的威胁模型说明。

## 执行摘要

E2 的最高风险主题是权力扩大、队列滥用、能力或节点身份伪造、重放 / 重复执行，以及派发溯源丢失。中心绝不能变成远程 shell 或节点策略控制面；派发必须始终是 schema 化的 goal 数据、每节点鉴权、低于节点本地上限、幂等、容量有界，并且端到端可审计。

## 范围与假设

范围内：

- `GET /jobs/lease?node=&capacity=` 派发语义。
- E2 使用的队列、直接节点路由、生命周期和派发溯源存储。
- E2 将依赖的现有节点鉴权、遥测摄入、审计保留、健康信号和仪表盘 / 操作员鉴权。

范围外：

- 仅凭本文档授权面向操作员的派发上线。
- 不修改 Scoot 的 `docs/EDGE.md`，也不依赖 Scoot 内部实现。
- 不做多租户 SaaS、计费、公网产品化、反向拨号或远程 shell。

本文档覆盖的创建面已确认的假设：

- 部署仍是私有 / VPC 风格，而不是公网多租户 SaaS。
- 仪表盘操作员是可信人类，但操作员账户可能被攻陷，必须作为真实威胁源处理。
- 节点 descriptor 和 capability 声明只是建议性路由信号，不是权限来源。
- 真实 Scoot edge 会在运行作业前强制无人值守 readonly clamp，并拒绝高于本地上限的策略。Scoot 已把
  clamp 以 `scoot --unattended -e "<goal>"` 形式发布，并从 `scoot-edge v0.8.0` 起为派发任务发布了
  `edge.job_root` cwd confinement。本文档命名的兼容契约版本是 `scoot-edge >= v0.8.0`。

在单独的派发**控制**面（取消 / 重试 / 编辑）被构建前，仍需要确认的假设：

- 一个能影响已经在运行或已排队任务（而不只是创建新任务）的操作员入口，需要单独审视授权、审计与回滚
  语义。

开放问题：

- 是否应在按节点的 `SCOOTSHIP_DISPATCH_QUEUE_LIMIT` 上限之外，再加上按操作员或按来源 IP 的限流，
  而不只依赖现有的仪表盘登录锁定？
- 派发溯源是否应携带显式的 token fingerprint（最终 lease / 运行该任务的 edge 是用哪个凭据鉴权的）
  和 goal fingerprint（哈希），作为区别于原始 `goal` 文本的、防篡改审计字段？（已推迟；见
  TM-002/TM-008。）
- 派发**控制**（取消 / 重试 / 编辑一个已排队的任务）的设计和威胁模型应该是什么样？这尚未构建，
  不在本文档范围内。
- E2 创建是否应该从今天单个人工选定节点，扩展到 label/capability fan-out（按条件派发给所有匹配
  节点）？如果要扩展，广域 fan-out 需要什么样的确认步骤？
- 中心派发溯源适用哪些审计保留和备份要求？

## 系统模型

### 主要组件

- 操作员经表单登录和 HttpOnly 会话使用嵌入式仪表盘（`internal/center/auth.go`、`internal/web`）。
- 边缘节点用每节点 bearer token 访问节点路由（`internal/center/server.go`、`internal/center/auth.go`、`internal/tokens`）。
- `/telemetry` 摄入 append-only 的 status、audit_batch 和 job lifecycle events（`internal/center/telemetry.go`、`internal/store`）。
- `/jobs/lease` 是已鉴权、节点绑定的派发端点，只返回该已鉴权节点的已持久化任务（`internal/center/lease.go`）。
- 线缆 schema 已定义并校验 `JobBody` 与 `JobEventBody`；生命周期遥测会更新已持久化的派发记录
  （`internal/protocol/protocol.go`、`internal/center/telemetry.go`、`internal/store`）。

### 数据流与信任边界

- 操作员浏览器 -> 仪表盘：凭据、会话和 job 请求经 HTTPS；受登录会话、HttpOnly cookie、锁定机制和安全头保护。
- 仪表盘 -> 中心派发逻辑：操作员提交的 goal、目标节点、请求策略、截止时间、重试次数，以及所需的
  labels/tools/skills；由 `dispatch:manage` 能力授权，受 CSRF 保护，服务端校验，并在进入队列前审计
  （记录 requestor + job_id）。
- 边缘 -> 中心 `/telemetry`：bearer token、status、audit_batch 和 `job_event`；按节点鉴权，修改 store 前校验。
- 边缘 -> 中心 `/jobs/lease`：bearer token、node ID、capacity；当前已鉴权，返回 job 时仍必须绑定节点。
- 中心 -> append-only store：遥测、派发溯源和作业生命周期；凡游标或幂等语义依赖持久性的地方，必须先持久化再 ack。

#### 图

```mermaid
flowchart TD
  O["操作员浏览器"] -->|HTTPS session| D["嵌入式仪表盘"]
  D -->|operator job request| C["中心派发逻辑"]
  E["Scoot edge"] -->|Bearer telemetry| T["遥测端点"]
  E -->|Bearer lease| L["Lease 端点"]
  T --> S["Append only store"]
  C --> S
  L -->|persisted jobs| E
  E --> A["本地 Scoot clamp"]
```

## 资产与安全目标

| 资产 | 为什么重要 | 安全目标 |
| --- | --- | --- |
| 节点 bearer tokens | 认证边缘节点访问 telemetry 和 lease 端点 | C/I |
| 操作员会话与账户 | 派发权力从仪表盘访问开始 | C/I |
| 作业队列与幂等键 | 决定哪些工作被提供给节点以及是否重复执行 | I/A |
| 节点本地策略上限 | 防止中心扩大本地执行权力 | I |
| 审计批次与派发溯源 | 证明运行了什么、为什么、谁请求、结果如何 | C/I |
| Append-only store 与备份 | 遥测、操作员、token 和派发链路的恢复来源 | C/I/A |

## 攻击者模型

### 能力

- 远程攻击者可能触达部署暴露的中心端点。
- 如果运维处理不当，攻击者可能获得节点 token。
- 攻击者可能攻陷仪表盘操作员账户。
- 恶意或失陷节点可以伪造 descriptor、capability、health 和 lifecycle 数据。
- 可信反代终止 TLS 时，网络中间层可能存在。

### 非能力

- 攻击者不能让中心反向拨号到边缘，因为这条路径不应存在。
- 攻击者不能假设中心能抬高 Scoot 本地策略；这个边界必须留在节点本地。
- 合格 E2 设计不能通过线缆执行任意 shell；job 只能是 schema 化的 `kind=run` goal 数据。

## 入口点与攻击面

| 表面 | 如何触达 | 信任边界 | 说明 | 证据 |
| --- | --- | --- | --- | --- |
| 仪表盘登录 | 浏览器表单 POST | 操作员 -> 中心 | 会话签发和暴力破解锁定 | `internal/center/auth.go`、`internal/loginguard` |
| 派发创建 UI | 已登录仪表盘，`dispatch:manage` 能力 | 操作员 -> 队列 | `/dispatch/new`（GET）和 `POST /dispatch` 受能力门禁、CSRF 保护，并按节点队列有界；`/dispatch` 与 `/api/dispatch` 仍保持只读 | `internal/center/dispatch_create.go`、`internal/store` |
| `/telemetry` | 边缘 HTTP POST | 边缘 -> 中心 | 解析 NDJSON 并在修改状态前校验 | `internal/center/telemetry.go` |
| `/jobs/lease` | 边缘 HTTP GET | 边缘 -> 中心 | 节点 token 鉴权、节点绑定、capacity 有界、持久化任务 lease | `internal/center/lease.go` |
| token 生命周期 UI/API | 已登录仪表盘 | 操作员 -> 节点鉴权注册表 | 创建、轮换、撤销中心托管 token | `internal/center/tokens.go`、`internal/tokens` |
| Append-only store | 服务进程 | 中心 -> 磁盘 | 存储遥测和派发证据 | `internal/store` |

## 主要滥用路径

1. 被攻陷的操作员账户提交高风险 goal，大范围派发，并试图隐藏溯源。
2. 泄露的节点 token 领取其他节点的 job，除非节点绑定持续强制。
3. 恶意节点伪造 capability，诱导中心派发不应分配给它的 job。
4. 网络或客户端重试重放 lease 或 job ack，导致重复执行。
5. 队列洪泛或夸大 capacity 使合法节点饥饿，或拖垮中心。
6. 派发实现错误地把 goal 数据转换成 shell/eval。
7. 中心请求高于节点天花板的策略，且真实 edge 没有 clamp。
8. job lifecycle event 没有匹配的派发溯源，导致事后无法还原。

## 威胁模型表

| ID | 威胁源 | 前置条件 | 动作 | 影响 | 受影响资产 | 现有控制 | 缺口 | 建议缓解 | 检测思路 | 可能性 | 影响 | 优先级 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| TM-001 | 被攻陷操作员 | 操作员持有 `dispatch:manage` 且会话有效 | 针对可达节点提交有害 goal | 未授权的单节点动作，上限受该节点自身天花板约束 | 操作员账户、队列、审计 | 已有登录/会话/锁定、会话绑定 CSRF、`dispatch:manage` 能力门禁（与 `fleet:view` 独立）、按节点待处理队列上限，以及 requestor+job_id 日志记录；创建仅面向单个节点，没有可被滥用的 fan-out 面 | 尚无按操作员的派发限流或异常告警；派发溯源中 goal/token fingerprint 尚未独立字段化 | 在登录锁定之上增加派发专属限流/告警；为溯源增加 goal/token fingerprint（TM-002/TM-008） | bulk dispatch 和策略变化告警 | 中 | 高 | 高 |
| TM-002 | 被盗节点 token | token 泄露 | 作为节点领取 job 或伪造 lifecycle | job 被窃取、状态伪造、审计混乱 | 节点 token、队列、溯源 | 每节点 token、node mismatch 校验和节点绑定 lease 已有 | 派发 provenance 尚未附带 token fingerprint | job 绑定 authenticated node；支持轮换 / 撤销；记录 token fingerprint | 新 IP 或异常节点变更告警 | 中 | 高 | 高 |
| TM-003 | 恶意节点 | 节点能上报 descriptor/capability | 伪造能力吸引 job | 错误路由或不安全执行尝试 | descriptor、策略上限 | 路线图限定 descriptor 只是建议，本地 ceiling gate 执行 | 能力校验语义未定义 | descriptor 仅作 hint；敏感 job 需 allowlist label 或人工分配 | capability drift 告警 | 中 | 中 | 中 |
| TM-004 | 网络 / 客户端重试 | E2 long-poll 和生命周期重试存在 | 重放 lease 或 lifecycle | 重复执行或终态错误 | 幂等键、队列状态 | 已有持久化 `idem_key` 去重和终态保护 | 显式重试调度仍是未来工作 | 保持重复 lease/event 幂等；重试调度落地时补 retry-window 测试 | duplicate idem key 和 late event 指标 | 中 | 高 | 高 |
| TM-005 | 远程攻击者或被攻陷操作员 | 中心端点可达，或持有操作员会话 | 洪泛 lease/telemetry、夸大 capacity，或创建派发任务的速度快于其消化速度 | DoS 或队列饥饿 | 中心可用性、队列公平性 | 已有请求超时、telemetry body cap、lease 最大 capacity、可配置的每节点待处理派发任务上限（`SCOOTSHIP_DISPATCH_QUEUE_LIMIT`、`ErrDispatchQueueFull`），以及仪表盘登录锁定 | 除了登录锁定和按节点队列上限之外，`POST /dispatch` 本身没有专门的按 IP / 按操作员限流 | 如观察到滥用，为派发创建增加显式的按操作员或按 IP 限流 | rate、queue depth、timeout 与拒绝指标；节点队列达到上限时告警 | 中 | 中 | 中 |
| TM-006 | 实现缺陷 | 开发者扩展 dispatch path | 把 goal 转成 shell/eval 或原始命令 | 远程命令执行 | 节点执行边界 | 铁律禁止 raw command；协议校验闭合 `kind=run`；测试覆盖 job lease body | 尚无静态 dispatch grep/audit 规则 | 保持闭合 `kind=run`，不加 shell 字段，测试证明 raw command path 不存在 | dispatch 路径 shell/eval grep/audit 规则 | 低 | 高 | 高 |
| TM-007 | 契约不匹配 | 操作员或中心对接的 edge 版本早于加入 confinement 的发布版 | 中心按已有 cwd confinement 假设派发，而旧版 edge 并未强制执行 | 旧版未加固 edge 上，readonly job 造成过度读取 | 本地策略上限、节点安全 | Scoot 已在 `scoot-edge v0.8.0` 中发布 `edge.job_root` cwd confinement；兼容版本已在 `docs/roadmap.zh-CN.md` 和本文档中命名 | 中心没有运行时机制去检查实际连接的是哪个 `scoot-edge` 版本 / 构建；操作员仍可能让旧版 edge 连到当前中心 | 在部署文档中显著标注所需的最低 edge 版本；考虑未来在节点 / 车队视图上增加 `edge_version` 兼容性警告 | `edge_version` 漂移已作为健康信号存在；可考虑增加专门的低版本警告 | 低 | 中 | 中 |
| TM-008 | 存储 / 溯源缺口 | lifecycle 没有派发上下文 | 无法证明谁、什么、为什么 | 审计完整性受损 | 派发溯源、审计链 | append-only 派发快照包含 requestor、node、policy、idem key、lifecycle 与 session 关联 | goal fingerprint 和 token fingerprint 尚未独立字段化 | 增加显式 fingerprint 和无匹配 dispatch 的 lifecycle 检测 | lifecycle without dispatch 告警 | 中 | 中 | 中 |

## 严重度校准

- Critical：预鉴权或仅凭 token 即可原始命令执行、抬高节点策略上限，或未审计的全队派发。
- High：被攻陷操作员或 token 可以派发有害工作，出现重复执行，或 Scoot clamp 契约不匹配导致本地策略绕过。
- Medium：capability 伪造、队列饥饿、lifecycle 污染或在现有鉴权边界下的局部溯源丢失。
- Low：文案误导、低敏元数据暴露，或不影响派发权力的噪声失败。

## 安全复审重点路径

| 路径 | 为什么重要 | 相关威胁 |
| --- | --- | --- |
| `internal/center/lease.go` | lease 返回路径必须保持节点绑定且 capacity 有界 | TM-002, TM-004 |
| `internal/protocol/protocol.go` | Job / job-event schema 定义哪些权力能过线缆 | TM-006, TM-007 |
| `internal/center/auth.go` | 操作员会话门控派发权力 | TM-001 |
| `internal/tokens` | 节点 token 生命周期决定 token 泄露后的恢复能力 | TM-002 |
| `internal/store` | 队列 / 溯源持久化、幂等与按节点队列上限都在这里或新聚焦接口后面实现 | TM-004, TM-005, TM-008 |
| `internal/mockedge` | E2 测试不能把 mock edge 变成第二个 Scoot 实现 | TM-003, TM-007 |
| `docs/roadmap.zh-CN.md` | 边界门禁与非目标阻止不安全的局部派发 | TM-006, TM-007 |

## 质量检查

- 已覆盖入口点：仪表盘登录、派发创建 UI、telemetry、lease、token 生命周期和存储。
- 每个信任边界至少出现在一个滥用路径或威胁行中。
- 已区分运行时与 CI/release；CI 不被建模为 E2 运行时权力路径。
- 单独的派发**控制**面（取消 / 重试 / 编辑）明确保持在范围之外且尚未构建，直到它有自己的威胁模型说明。
- 本文档是派发**创建**的门禁材料，不是对仍未构建的**控制**面的批准。
