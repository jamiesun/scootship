# scootship

![scootship — 车队控制中心](assets/hero.png)

[English](../README.md) | **简体中文**

**一个面向 [Scoot](https://github.com/jamiesun/scoot) agent 车队的管理中心。**

Scoot 是一个本地优先、单二进制的 AI agent，运行在你的机器上，并把每一个动作记进 JSONL 审计痕迹。
今天每个 Scoot 安装都是一座孤岛。`scootship` 就是 Scoot 项目
[`docs/EDGE.md`](https://github.com/jamiesun/scoot/blob/main/docs/EDGE.md) 指向的那个**管理中心**：
一个单一的 Go 二进制，通过 `scoot-edge` 协议摄入 append-only 的车队遥测，并提供一个嵌入式 admin
dashboard，让你在一个地方观测整支车队。

> **状态：阶段一 —— 观测 + 框架（pre-1.0）。**
> 中心摄入 `status` 心跳与 `audit_batch` 日志上报，并渲染车队。
> 任务**派发 / 编排（EDGE.md E2）**有意尚未构建 —— lease 端点存在但不派发任何东西。项目形态、
边界与方向见 [`roadmap.zh-CN.md`](roadmap.zh-CN.md)。

## 为什么是一个独立的伴生程序

`scoot-edge` 在设计上是一个**向外拨号**到中心的可选伴生程序；边缘从不开启监听端口。这使得**中心成为
车队里唯一的可信入站面**，因此 scootship 以防御姿态构建：

- 每个节点端点都由**每节点 bearer token** 鉴权，仪表盘由**登录会话**鉴权（表单登录 + HttpOnly
  cookie，而非浏览器的 basic-auth 弹窗）。
- 仪表盘登录**按来源 IP 限流**：重复失败会把该 IP 锁定一段冷却期（带 `Retry-After`），让中心唯一的
  入站面抵御暴力破解。
- 每个响应都带有防御性头部（严格的 `Content-Security-Policy`、无内联脚本，外加 `X-Frame-Options`、
  `X-Content-Type-Options`、`Referrer-Policy`）。
- 遥测是 **append-only** 的，绝不回写到节点的本地状态。
- 中心**永远无法抬高节点的本地策略天花板** —— 它不是一个权限控制台。

EDGE.md 里一个诚实的告诫同样适用：一旦节点选择开启审计上报，它上报的内容（文件内容、命令输出）就存放
在中心。请把中心，以及它的仪表盘登录，当作敏感资产对待。仪表盘/API 对每个节点只保留有界的最近审计窗口；
窗口被裁剪时会显式标出 `audit_gap`，已接受的审计批次仍会 append-only 持久化到中心 JSONL 日志。

```text
        Operators (browser · login session)
                 |
                 v
   +-------------------------------------------+
   |        scootship (single Go binary)        |
   |  embedded dashboard (embed.FS)             |
   |  POST /telemetry   GET /jobs/lease (E2)    |
   |  per-node token auth · append-only store   |
   +----------------------^---------------------+
                          |  edge dials OUT (HTTPS + per-node token)
        +-----------------+-----------------+
        |                 |                 |
    scoot-edge        scoot-edge        scoot-edge   (可选 · 尚未实现)
        |                 |                 |
      scoot             scoot             scoot
```

## 快速开始

需要 Go 1.26+。无外部依赖、无 Node 工具链、无需安装数据库 —— 仪表盘与存储都是内建的。

```sh
# 终端 1 —— 以 dev 模式运行中心（仪表盘开启，种子演示令牌 n-dev=dev-token）
SCOOTSHIP_DEV=1 go run ./cmd/scootship serve

# 终端 2 —— 真实的 scoot-edge 尚不存在（EDGE.md 处于 E0 / 仅设计阶段），
# 所以用内建模拟器跑通完整的 心跳 -> 摄入 -> 仪表盘 链路：
go run ./cmd/scootship mock-edge -ship-audit
```

打开 <http://localhost:8080>。你会被重定向到登录页 —— 在 dev 模式下用 `admin` / `admin` 登录。登录
后你会看到带**可收拢左侧菜单**的仪表盘外壳（Fleet、Tokens、Operators + 含 Account 的 Settings 菜单），
并在右上角提供退出登录：`n-dev` 节点变为**在线**，显示其策略天花板、派生的审计计数、能力标签，以及
（因为开了 `-ship-audit`）节点详情页上几条已摄入的审计事件。

或使用 Makefile：

```sh
make run         # dev 模式中心
make mock-edge   # 模拟节点
make docs        # 构建双语 mdBook 文档到 ./book（需要 mdBook）
make docs-serve  # 本地预览双语 mdBook 文档
make ci          # fmt-check + vet + test + build
```

## CI、release 与项目 skills

- CI 通过 `.github/workflows/ci.yml` 在 push、pull request 和手动触发时运行：
  `gofmt`、`go vet`、`go test`、`go build`、CLI 版本 smoke，以及 release target
  的交叉编译 smoke 检查。
- `.github/workflows/docs.yml` 中的 docs workflow 会在文档相关 push、pull request 和手动触发时
  构建双语 mdBook；推送到 `main` 时还会把生成的 `book/` 站点部署到 GitHub Pages。
- 推送 `vX.Y.Z` 标签会触发 `.github/workflows/release.yml`，它会把标签注入
  `internal/version.Version`，为 Linux、macOS 与 Windows 交叉编译单二进制 archives，
  并随 GitHub Release 发布校验和。
- 项目本地 agent skills 位于 `.agents/skills`：`auto-release` 用于受控 release 编排，
  `project-audit` 用于生成带评分的全仓库健康报告。

## 配置

`serve` 通过环境变量配置（密钥绝不来自提交进仓库的配置）：

| 变量 | 默认值 | 含义 |
| --- | --- | --- |
| `SCOOTSHIP_ADDR` | `:8080` | 监听地址。 |
| `SCOOTSHIP_TLS_CERT` / `SCOOTSHIP_TLS_KEY` | _(未设置)_ | 直连 HTTPS 的 PEM 路径。EDGE.md 要求生产安全传输；未配置直连 TLS 时，除非显式 `SCOOTSHIP_DEV=1` 或 `SCOOTSHIP_BEHIND_TLS_PROXY=1`，否则启动失败。 |
| `SCOOTSHIP_BEHIND_TLS_PROXY` | _(未设置)_ | `=1` 表示中心只在可信反向代理终止 TLS 后方监听明文 HTTP。必须确保该监听地址不会被直接暴露。 |
| `SCOOTSHIP_DATA_DIR` | `./data` | append-only 存储目录。 |
| `SCOOTSHIP_ADMIN_USER` | `admin` | 当操作员存储为空时，用于 bootstrap 第一个仪表盘操作员的用户名。 |
| `SCOOTSHIP_ADMIN_PASSWORD` | _(未设置)_ | 仅用于 bootstrap 第一个仪表盘操作员的密码。首次启动除非 `SCOOTSHIP_DEV=1`（会 bootstrap `admin`/`admin`），否则必填。bootstrap 后操作员在仪表盘中管理，并存于 `SCOOTSHIP_DATA_DIR/operators.json`。 |
| `SCOOTSHIP_NODE_TOKENS_FILE` | _(未设置)_ | JSON 文件：`{"n-7a3":"secret", ...}`。必须是普通私有文件，不能有可执行、组或其他用户权限（通常设置为 `0600`）。 |
| `SCOOTSHIP_NODE_TOKENS` | _(未设置)_ | 内联 `n-7a3=secret,n-8b4=secret2`。 |
| `SCOOTSHIP_DEV` | _(未设置)_ | `=1` 种子演示节点令牌和默认 `admin`/`admin` 仪表盘登录（不安全；本地使用）。 |
| `SCOOTSHIP_STALE_SECONDS` | `90` | 节点静默这么多秒后显示为「stale」。 |
| `SCOOTSHIP_MAX_TELEMETRY_BYTES` | `8388608` | 单个 `/telemetry` 请求体的最大字节数。 |
| `SCOOTSHIP_AUDIT_RETENTION_EVENTS` | `1000` | 每节点为仪表盘/API 保留的最近审计事件数。溢出会显式产生 `audit_gap`；已接受事件仍保留在 append-only JSONL 日志中。 |
| `SCOOTSHIP_LOGIN_MAX_FAILS` | `5` | 单个来源 IP 登录失败多少次后被锁定。 |
| `SCOOTSHIP_LOGIN_WINDOW_SECONDS` | `900` | 统计失败次数的滑动窗口。 |
| `SCOOTSHIP_LOGIN_LOCKOUT_SECONDS` | `900` | 触发后该 IP 被锁定多久。 |
| `SCOOTSHIP_TRUSTED_PROXIES` | _(未设置)_ | 逗号分隔的反向代理 CIDR/IP，其 `X-Forwarded-For` 可被采信以归因真实客户端 IP。未设置表示只信任原始连接（伪造的 `XFF` 被忽略）。 |

`mock-edge` 是一个 dev/test 客户端，由标志位配置：`-center`、`-node`、`-token`、`-interval`、
`-ship-audit`。它会把 token 作为 `Authorization: Bearer <token>` 发送，信封中的 `node_id`
必须与该 token 绑定的节点一致。

仪表盘也可以创建、轮换和撤销中心托管的节点令牌。secret 由中心生成，在创建或轮换成功后只显示一次，
不会进入清单或 API，并以私有 `0600` 权限持久化到 `SCOOTSHIP_DATA_DIR/managed_node_tokens.json`。
撤销状态也保存在这里，因此操作员可以撤销原本来自环境变量或私有 token 文件的令牌，而不需要编辑原始来源。
真实 `scoot-edge` 使用同一个节点 ID 和 secret；当前 edge 客户端流程中即设置 `SCOOT_EDGE_TOKEN=<secret>`，
并用 `--node-id <node>` 指向中心的 `/telemetry` 端点。

生产部署、数据目录权限、TLS 反代边界、备份和恢复，请使用运维手册
[`deployment.zh-CN.md`](deployment.zh-CN.md)。

## 协议对齐

scootship 实现冻结的 `scoot-edge` v1 契约的**中心侧**。线缆形状位于
[`internal/protocol`](../internal/protocol/protocol.go)，精确镜像 EDGE.md：

- 信封 `{"v":1,"type":"status|audit_batch|job|job_event","node_id":"...","sent_ts":...,"body":{}}`。
- **E1（已实现）：** `POST /telemetry` 接受 `status` 与 `audit_batch`（并向前兼容 `job_event`）。
  审计摄入按 `{file_gen, byte_to}` 游标幂等，并 ack 已持久化的游标，使边缘只在持久 ack 后推进。遥测批次会在
  写入 store 前完整解码和校验；无效审计游标、空审计批次和未知审计事件 kind 都会被拒绝。最近审计窗口由
  `SCOOTSHIP_AUDIT_RETENTION_EVENTS` 约束；裁剪会以中心侧 `audit_gap` 显示。节点详情 API / 页面也会按
  `session_id` / `run_id` 把保留窗口内的审计组织成按时间顺序的运行时间线。
- **E1 健康信号（已实现）：** 车队与节点页面派生只读信号，用于显示节点过期、版本漂移、审计正文滞后、
  保留缺口、重复审计上报、策略拒绝、系统错误和本地 unrestricted 上限。它们不会触发处置，也不会修改节点状态。
- **节点令牌生命周期（已实现）：** 仪表盘操作员可以创建、轮换和撤销中心托管的每节点认证令牌；secret
  由中心生成，只显示一次，随后不会进入清单、API、日志或审计。
- **仪表盘操作授权（已实现）：** 已登录操作员直接关联内置能力（`fleet:view`、`tokens:manage`、
  `operators:manage`），不引入角色组；所有已登录后的状态变更表单都要求会话绑定的 CSRF token。
- **E2（占位）：** `GET /jobs/lease` 会鉴权节点，要求匹配的 `node` 查询参数，限制 `capacity`，但在阶段一不派发任何东西。预派发威胁模型位于
  [`dispatch-threat-model.zh-CN.md`](dispatch-threat-model.zh-CN.md)；这是门禁材料，不是实现批准。

scootship 只说这份契约；它不依赖任何 Scoot 内部实现。

## 项目布局

| 路径 | 职责 |
| --- | --- |
| `cmd/scootship` | CLI 入口：`serve`、`mock-edge`、`version`。 |
| `internal/protocol` | 冻结的 scoot-edge v1 线缆契约（信封、bodies、游标）。 |
| `internal/store` | append-only JSONL 车队存储，带幂等审计摄入、重放、可见审计保留缺口和保留窗口内运行时间线。 |
| `internal/tokens` | 每节点 bearer-token 注册表、私有托管生命周期状态，以及仪表盘安全可展示的令牌清单元数据（中心的节点鉴权面）。 |
| `internal/operators` | 仪表盘操作员账户、直接能力、资料/密码管理与密码哈希。 |
| `internal/loginguard` | 仪表盘登录的按来源 IP 暴力破解限流（失败窗口 + 锁定）。 |
| `internal/config` | 环境驱动的配置。 |
| `internal/center` | HTTP 服务器、bearer + 登录会话鉴权、能力门禁、CSRF 校验、遥测摄入、lease 占位、只读健康信号、仪表盘 + JSON API。 |
| `internal/web` | 嵌入式仪表盘模板与静态资源（`embed.FS`）。 |
| `internal/mockedge` | 模拟的 scoot-edge 节点（替代尚未构建的边缘）。 |
| `internal/version` | 构建版本字符串；release 构建会从标签覆盖它。 |
| `.github/workflows` | CI、mdBook 文档与标签驱动的 release 自动化。 |
| `.agents/skills` | 项目本地 release 与 audit skills。 |
| `docs/roadmap.zh-CN.md` | 项目形态、非目标与方向。 |

## 贡献

修改前请阅读 [`AGENT.zh-CN.md`](AGENT.zh-CN.md)（工程手册）与
[`roadmap.zh-CN.md`](roadmap.zh-CN.md)（意图与硬边界）。生产运维请阅读
[`deployment.zh-CN.md`](deployment.zh-CN.md)。推送前运行 `make ci`。

## 许可

[MIT](../LICENSE) —— 与 Scoot 生态保持一致。
