# AGENT.md

[English](AGENT.md) | **简体中文**

本仓库中 AI agent 与贡献者的工程指南。

修改前先读本文件。然后读路线图：

- [docs/roadmap.zh-CN.md](docs/roadmap.zh-CN.md)

路线图是产品意图与非目标的来源。本文件是实现手册。若两者在范围上冲突，以路线图为准；若代码与文档在
行为上冲突，可运行的代码与测试是即时的事实来源，文档必须被修正。

## 一句话定位

scootship 是面向 [Scoot](https://github.com/jamiesun/scoot) agent 车队的**管理中心**。它实现冻结的
`scoot-edge` v1 契约的**中心（服务端）侧**（见 Scoot 的 `docs/EDGE.md`）：通过 HTTP 摄入 append-only
遥测，并从单个 Go 二进制提供嵌入式 admin dashboard。阶段一仅做观测。

## 与 Scoot 的关系（先读这个）

- **scootship 是边缘向外拨号连接的对端。** 在 EDGE.md 拓扑中边缘不开监听、只向外拨号；中心是服务端。
  所以中心是车队唯一的可信入站面，必须据此防御。
- **协议在上游冻结。** `internal/protocol` 是 EDGE.md 的 `v:1` 信封与 bodies 的忠实转写。不要在这里
  发明字段或消息类型。若契约需要变更，那是 Scoot 仓库里 EDGE.md 级别的决定，要先在那里做。
- **`scoot-edge` 尚不存在。** EDGE.md 处于 E0（仅设计）。没有真实边缘可供测试，这正是
  `internal/mockedge` 存在的原因。让它保持为契约的忠实*客户端* —— 绝不是 Scoot 的第二个实现。
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
```

修改任何 `.go` 文件后，至少运行 `go build ./...` 与 `go test ./...`。

## 代码地图

| 路径 | 职责 |
| --- | --- |
| `cmd/scootship/main.go` | CLI：`serve`、`mock-edge`、`version`；环境驱动启动；基于信号的优雅关闭。 |
| `internal/protocol` | 冻结的 scoot-edge v1 契约：信封、status/audit/job bodies、幂等游标。最窄、最稳定的面 —— 只为跟随 EDGE.md 而改。 |
| `internal/store` | `Store` 接口 + append-only JSONL `Mem` 实现。幂等审计摄入、启动时重放、内存中的车队索引。 |
| `internal/tokens` | 每节点 bearer-token 注册表。中心自己的治理面；**不是**节点策略配置。 |
| `internal/loginguard` | 仪表盘登录的按来源 IP 暴力破解限流（滑动窗口失败计数 + 锁定）。 |
| `internal/config` | `SCOOTSHIP_*` 环境配置。 |
| `internal/center` | HTTP 服务器、鉴权中间件、登录限流 + 安全头、`/telemetry` 摄入、`/jobs/lease` 占位、仪表盘登录会话、仪表盘 + JSON API。 |
| `internal/web` | `embed.FS` 仪表盘模板与静态资源。 |
| `internal/mockedge` | 模拟的边缘节点（心跳、审计上报、lease 轮询）。 |
| `internal/version` | 构建版本字符串。 |

新增子系统时，优先用带聚焦接口的新 `internal/<name>` 包，而不是扩宽已有包。保持 `internal/protocol`
无依赖。

## 硬规则

更改这些需要路线图级别的决定（它们把路线图的非目标重述为可执行的工程规则）。

1. **绝不抬高节点的本地策略天花板。** 中心只能*请求*不高于节点已声明上限的策略；绝不能提供任何抬高
   它的 UI、API 或线缆字段。天花板是节点本地的 opt-in。
2. **遥测是 append-only 的只读摄入。** 中心绝不回写或对账节点的本地状态。没有双向同步。
3. **没有远程命令执行。** 被派发的作业（E2，以后）只携带作为不透明**数据**的 `goal`（`kind=run`）。
   绝不从线缆合成 shell/eval。
4. **中心绝不反向拨号到边缘。** 连接永远由边缘发起。
5. **审计摄入必须保持幂等。** 按 `{file_gen, byte_to}` 游标应用；重放的区间是 no-op；只 ack 已持久
   存储的游标。
6. **UI 以嵌入方式交付。** 仪表盘资源从单二进制里的 `embed.FS` 提供 —— 没有独立 web 进程、没有 Node
   构建步骤、没有 CDN 运行时依赖。
7. **密钥绝不被编进、提交、记录或打印。** 节点令牌、TLS 私钥和仪表盘密码来自环境变量或 `0600` 文件。
   不要记录 `Authorization` 头。
8. **每个节点与仪表盘端点都要鉴权。** 节点路由用 bearer token，仪表盘用登录会话（表单登录 + HttpOnly
   cookie）。一个令牌只能为它自己的 `node_id` 说话。仪表盘登录按来源 IP 限流（`internal/loginguard`）：
   绝不削弱或移除锁定，也绝不按用户名键控（那会让攻击者锁死真正的运维者）。只信任来自已配置
   `SCOOTSHIP_TRUSTED_PROXIES` 的 `X-Forwarded-For`。
9. **保持标准库优先与单二进制。** 优先标准库。在加入任何第三方依赖前，要对照单二进制、易交叉编译的
   姿态为它辩护。
10. **`internal/protocol` 跟随 EDGE.md，而非本地便利。** 未知字段忽略；未知主版本拒绝。不要单方面
    扩展契约。

## 阶段边界

- **阶段一（现在）：观测 + 框架。** `status` 与 `audit_batch` 摄入、车队仪表盘、节点注册表、每节点
  令牌鉴权，以及 mock-edge 装置。
- **E2（以后，需开关）：作业派发 / 编排。** `/jobs/lease` 端点今天是占位。构建真实派发意味着能力/标签
  路由、只降不升的策略 clamp、幂等 `idem_key` 应用、容量背压、截止期限，以及通过 `session_id` 关联
  到运行的派发溯源审计。不要把派发半路接进阶段一。

## 扩展工作流

1. 加能力前先查 `docs/roadmap.zh-CN.md`；若触及非目标，先把边界改掉。
2. 决定这项工作是扩展已有 `internal/*` 包还是需要新包。
3. 用能证明改动的最小面加聚焦测试（已有的 `protocol`、`store`、`center` 测试是范本）。
4. 在行动前校验不可信输入；把审计 `msg` 内容当作数据，绝不当作指令。
5. 运行 `make ci`。
6. 当行为或范围变化时，更新 `README.md` 与 `docs/roadmap.md`（以及它们的 `.zh-CN.md` 对应文件）。

## 风格

- 保持改动聚焦；不要重构无关文件。
- 优先已有的本地抽象，而非新架构。
- 注释、代码字符串和测试描述默认用英文，解释意图与边界，而非显而易见的东西。
- 给每个外发请求和子进程设超时；给每个请求体设上限。
