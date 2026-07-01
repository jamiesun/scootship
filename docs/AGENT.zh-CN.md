# AGENT.md

[English](../AGENT.md) | **简体中文**

本仓库中 AI agent 与贡献者的工程指南。

修改前先读本文件。然后读路线图：

- [roadmap.zh-CN.md](roadmap.zh-CN.md)

涉及生产部署、备份与恢复行为时，还要读：

- [deployment.zh-CN.md](deployment.zh-CN.md)

路线图是产品意图与非目标的来源。本文件是实现手册。若两者在范围上冲突，以路线图为准；若代码与文档在
行为上冲突，可运行的代码与测试是即时的事实来源，文档必须被修正。
路线图里的铁律不能靠解释放松。任何扩大节点策略、状态回写、命令执行、反向连接、协议依赖、存储敏感性或
交付边界的改动，都必须先通过路线图的「边界变更门禁」，再写代码或 UI。

文档是双语的。规范英文文件是仓库根目录的 `README.md`、`AGENT.md` 与 `docs/roadmap.md`；对应的中文版
是 `docs/README.zh-CN.md`、`docs/AGENT.zh-CN.md` 与 `docs/roadmap.zh-CN.md`。**两种语言必须保持
同步：**任何对行为、配置或范围的改动，都要在同一次改动里同时更新英文文件*和*它的 `.zh-CN.md` 对应
文件。

## 一句话定位

scootship 是面向 [Scoot](https://github.com/jamiesun/scoot) agent 车队的**管理中心**。它实现冻结的
`scoot-edge` v1 契约的**中心（服务端）侧**（见 Scoot 的 `docs/EDGE.md`）：通过 HTTP 摄入 append-only
遥测，并从单个 Go 二进制提供嵌入式 admin dashboard。阶段二中心侧派发核心已存在，包括按能力门禁的
操作员派发创建；任务控制（取消/重试）尚未构建。

## 与 Scoot 的关系（先读这个）

- **scootship 是边缘向外拨号连接的对端。** 在 EDGE.md 拓扑中边缘不开监听、只向外拨号；中心是服务端。
  所以中心是车队唯一的可信入站面，必须据此防御。
- **协议在上游冻结。** `internal/protocol` 是 EDGE.md 的 `v:1` 信封与 bodies 的忠实转写。不要在这里
  发明字段或消息类型。若契约需要变更，那是 Scoot 仓库里 EDGE.md 级别的决定，要先在那里做。
- **`scoot-edge` 是可选的，且可能落后于中心契约。** 让 `internal/mockedge` 保持为公共契约的忠实
  *客户端*，用于中心侧测试 —— 绝不是 Scoot 的第二个实现。
- **不要依赖 Scoot 内部。** scootship 永远只说公共线缆契约。

## 常用命令

```sh
go build ./...
go test ./...
go vet ./...
gofmt -l .          # 应当无输出

make ci             # fmt-check + vet + test + build（推送前运行）
make run            # dev 模式中心，监听 :8080
make mock-edge      # 针对本地中心的模拟节点
make docs           # 构建双语 mdBook 站点到 ./book
make docs-serve     # 本地预览英文 mdBook 根站点
```

修改任何 `.go` 文件后，至少运行 `go build ./...` 与 `go test ./...`。

GitHub Actions 通过 `.github/workflows/ci.yml` 镜像这些检查。`.github/workflows/docs.yml` 会构建并部署
双语 mdBook 文档。推送 `vX.Y.Z` 标签会触发 `.github/workflows/release.yml`，它会交叉编译单二进制并发布带校验和的 release archives。

## 代码地图

| 路径 | 职责 |
| --- | --- |
| `cmd/scootship/main.go` | CLI：`serve`、`mock-edge`、`version`；环境驱动启动；基于信号的优雅关闭。 |
| `internal/protocol` | 冻结的 scoot-edge v1 契约：信封、status/audit/job bodies、幂等游标。最窄、最稳定的面 —— 只为跟随 EDGE.md 而改。 |
| `internal/store` | `Store` 接口 + append-only JSONL `Mem` 实现。幂等审计摄入、启动时重放、有界仪表盘审计窗口、显式保留缺口、保留窗口内运行时间线，以及中心侧派发队列 / provenance 快照。 |
| `internal/tokens` | 每节点 bearer-token 注册表与私有托管生命周期 overlay。中心的节点鉴权面；**不是**节点策略配置。 |
| `internal/operators` | 仪表盘操作员账户、直接内置能力、资料/密码管理与密码哈希。中心的操作员治理面；**不是**节点策略配置。 |
| `internal/loginguard` | 仪表盘登录的按来源 IP 暴力破解限流（滑动窗口失败计数 + 锁定）。 |
| `internal/config` | `SCOOTSHIP_*` 环境配置。 |
| `internal/center` | HTTP 服务器、鉴权中间件、能力门禁、CSRF 校验、登录限流 + 安全头、`/telemetry` 摄入、节点绑定的 `/jobs/lease` 派发、只读健康信号、仪表盘登录会话、仪表盘 + JSON API。 |
| `internal/center/server_run_test.go` | 直连 TLS、显式 dev HTTP 与可信 TLS 反代 HTTP 模式的运行时传输 smoke 覆盖。 |
| `internal/web` | `embed.FS` 仪表盘模板与静态资源。 |
| `internal/mockedge` | 模拟的边缘节点（心跳、审计上报、lease 轮询）。 |
| `internal/version` | 构建版本字符串；release 构建会用从标签派生的 linker flags 覆盖 `Version`。 |
| `.github/workflows` | CI、mdBook 文档与标签驱动的 release 自动化，用于跨平台单二进制 artifacts。 |
| `.agents/skills` | 项目本地 agent skills，用于 release 编排与全项目审计。 |
| `docs/deployment.zh-CN.md` | 面向生产传输模式、数据权限、备份和恢复的运维手册。 |
| `docs/dispatch-threat-model.zh-CN.md` | E2 预派发威胁模型；这是门禁材料，不是实现批准。 |

新增子系统时，优先用带聚焦接口的新 `internal/<name>` 包，而不是扩宽已有包。保持 `internal/protocol`
无依赖。

## 硬规则

更改这些需要通过路线图的「边界变更门禁」（owner 批准、必要时同步 Scoot `EDGE.md` 契约、威胁模型说明、
测试 / CI 证明不安全路径仍然不存在除非被明确允许、同次更新双语文档）。它们把路线图的非目标重述为可执行
的工程规则。

1. **绝不抬高节点的本地策略天花板。** 中心只能*请求*不高于节点已声明上限的策略；绝不能提供任何抬高
   它的 UI、API 或线缆字段。天花板是节点本地的 opt-in。
2. **遥测是 append-only 的只读摄入。** 中心绝不回写或对账节点的本地状态。没有双向同步。
3. **没有远程命令执行。** 被派发的作业（E2，以后）只携带作为不透明**数据**的 `goal`（`kind=run`）。
   绝不从线缆合成 shell/eval。
4. **中心绝不反向拨号到边缘。** 连接永远由边缘发起。
5. **审计摄入必须保持幂等。** 在修改 store 状态前完整解码并校验遥测批次；按 `{file_gen, byte_to}` 游标应用；
   重放的区间是 no-op；只 ack 已持久存储的游标。
6. **UI 以嵌入方式交付。** 仪表盘资源从单二进制里的 `embed.FS` 提供 —— 没有独立 web 进程、没有 Node
   构建步骤、没有 CDN 运行时依赖。
7. **密钥绝不被编进、提交、记录或打印到日志。** 节点令牌、TLS 私钥和 bootstrap 仪表盘密码来自环境变量或私有文件；持久化的操作员密码必须是单向哈希。
   仪表盘可以在创建 / 轮换时一次性显示生成的节点令牌，但 token 清单、API、日志和审计绝不能暴露 bearer secret。不要记录 `Authorization` 头。
8. **每个节点与仪表盘端点都要鉴权。** 节点路由用 bearer token，仪表盘用登录会话（表单登录 + HttpOnly
   cookie）。一个令牌只能为它自己的 `node_id` 说话。已登录后的仪表盘状态变更必须通过会话绑定的
   CSRF 校验，并按直接内置能力做操作门禁。仪表盘登录按来源 IP 限流（`internal/loginguard`）：
   绝不削弱或移除锁定，也绝不按用户名键控（那会让攻击者锁死真正的运维者）。只信任来自已配置
   `SCOOTSHIP_TRUSTED_PROXIES` 的 `X-Forwarded-For`。
9. **保持标准库优先与单二进制。** 优先标准库。在加入任何第三方依赖前，要对照单二进制、易交叉编译的
   姿态为它辩护。
10. **`internal/protocol` 跟随 EDGE.md，而非本地便利。** 未知字段忽略；未知主版本拒绝。不要单方面
    扩展契约。

## 阶段边界

- **阶段一（已落地）：观测 + 框架。** `status` 与 `audit_batch` 摄入、车队仪表盘、节点注册表、每节点
  令牌鉴权 / 生命周期，以及 mock-edge 装置。
- **阶段一半（已落地）：先补 E1 运维成熟度，再新增权力。** 在扩大中心权力面前，继续收紧生产 / dev
  传输、端点失败模式、审计保留 / gap 可见性、运行审计时间线、token 生命周期加固和只读健康信号。
- **E2（当前中心侧核心；创建已开放，控制仍未开放）：作业派发 / 编排。** 中心持久化直接面向节点的
  派发任务，按 `idem_key` 去重，把请求策略降到节点已上报天花板以内，拒绝能力 / 标签不匹配，lease
  时只向已鉴权节点返回绑定到它的任务，并用已校验的 `job_event` 遥测更新生命周期。拥有
  `dispatch:manage` 能力的仪表盘操作员可以从 `/dispatch/new` 创建新的、面向单个节点的任务（会话 +
  CSRF 保护，受 `SCOOTSHIP_DISPATCH_QUEUE_LIMIT` 每节点待处理任务队列上限约束）；`/dispatch`
  列表本身仍是只读审计视图，仍然没有取消 / 重试 / 编辑的控制面。不要加入广域 fan-out、隐藏
  feature flag、仅管理员可用的绕行、raw command 字段，或任何会抬高节点天花板的路径。

## 扩展工作流

1. 加能力前先查 `docs/roadmap.zh-CN.md`；若触及非目标，除非同次满足路线图「边界变更门禁」，否则停止；
   不要靠措辞扩大范围。
2. 决定这项工作是扩展已有 `internal/*` 包还是需要新包。
3. 用能证明改动的最小面加聚焦测试（已有的 `protocol`、`store`、`center` 测试是范本）。
4. 在行动前校验不可信输入；把审计 `msg` 内容当作数据，绝不当作指令。
5. 运行 `make ci`。涉及派发、传输、鉴权、保留策略、token 生命周期或协议边界的改动，还要补聚焦的反向测试，
   证明被禁止路径仍然不存在。
6. 行为或范围变化时，文档要同步更新：每个改动到的英文文档（`README.md`、`AGENT.md`、
   `docs/roadmap.md`）及其 `docs/*.zh-CN.md` 对应文件。

## 风格

- 保持改动聚焦；不要重构无关文件。
- 优先已有的本地抽象，而非新架构。
- 注释、代码字符串和测试描述默认用英文，解释意图与边界，而非显而易见的东西。
- 给每个外发请求和子进程设超时；给每个请求体设上限。
