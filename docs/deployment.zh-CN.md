# Scootship 部署、备份与恢复

[English](deployment.md) | **简体中文**

本文是 Scootship center 的真实运维手册，覆盖生产传输、存储、备份与恢复。中心侧 E2 派发记录可能存在，
但本文不启用面向操作员的派发上线，也不授予中心抬高节点本地策略上限的权力。

## 生产边界

Scootship 只有一个可信入站面：center HTTP 服务。生产环境必须显式选择以下两种传输模式之一：

- **直连 HTTPS：** 同时设置 `SCOOTSHIP_TLS_CERT` 与 `SCOOTSHIP_TLS_KEY`，由二进制自身提供 TLS。
- **可信 TLS 反代：** 在反向代理终止 HTTPS，将 Scootship 绑定到私有地址，并设置
  `SCOOTSHIP_BEHIND_TLS_PROXY=1`。

没有 `SCOOTSHIP_DEV=1` 或 `SCOOTSHIP_BEHIND_TLS_PROXY=1` 时，明文 HTTP 会在启动时 fail-closed。
`SCOOTSHIP_DEV=1` 仅限本地开发；它会种子 `admin/admin` 和 `n-dev=dev-token`，不得用于共享 center。

## 文件与权限

使用专用系统用户和私有数据目录：

```sh
sudo useradd --system --home /var/lib/scootship --shell /usr/sbin/nologin scootship
sudo install -d -o scootship -g scootship -m 0700 /var/lib/scootship
sudo install -d -o root -g scootship -m 0750 /etc/scootship
```

重要文件：

| 路径 | 内容 | 必要保护 |
| --- | --- | --- |
| `SCOOTSHIP_DATA_DIR/center.jsonl` | append-only 遥测与审计存储 | 私有数据目录；按敏感数据备份 |
| `SCOOTSHIP_DATA_DIR/operators.json` | 仪表盘操作员记录，包含密码哈希与直接能力 | `0600`，按敏感数据备份 |
| `SCOOTSHIP_DATA_DIR/managed_node_tokens.json` | 中心托管节点 token secret 与撤销状态 | `0600`，按密钥材料备份 |
| `SCOOTSHIP_NODE_TOKENS_FILE` | 可选的静态节点 token JSON | 普通私有文件，不得有组/其他用户/可执行权限 |
| TLS 私钥 | 直连 HTTPS 使用的私钥 | 按私钥处理；不得提交或写入日志 |

静态 token 文件示例：

```sh
sudo tee /etc/scootship/node-tokens.json >/dev/null <<'JSON'
{
  "node-a": "replace-with-node-a-secret"
}
JSON
sudo chown scootship:scootship /etc/scootship/node-tokens.json
sudo chmod 0600 /etc/scootship/node-tokens.json
```

## 环境文件

服务配置放在仓库之外：

```sh
sudo tee /etc/scootship/scootship.env >/dev/null <<'ENV'
SCOOTSHIP_ADDR=127.0.0.1:8080
SCOOTSHIP_BEHIND_TLS_PROXY=1
SCOOTSHIP_DATA_DIR=/var/lib/scootship
SCOOTSHIP_ADMIN_USER=admin
SCOOTSHIP_ADMIN_PASSWORD=replace-on-first-bootstrap-only
SCOOTSHIP_NODE_TOKENS_FILE=/etc/scootship/node-tokens.json
SCOOTSHIP_AUDIT_RETENTION_EVENTS=1000
SCOOTSHIP_TRUSTED_PROXIES=127.0.0.1/32
ENV
sudo chown root:scootship /etc/scootship/scootship.env
sudo chmod 0640 /etc/scootship/scootship.env
```

`SCOOTSHIP_ADMIN_PASSWORD` 只在 `operators.json` 为空时用于 bootstrap 第一个操作员。bootstrap 后，
操作员应从仪表盘管理。不要把真实密码留在 shell 历史里。

bootstrap 操作员和旧版本遗留操作员会获得所有当前内置能力（`fleet:view`、`tokens:manage`、
`operators:manage`），避免升级后锁死治理入口。新建操作员应只授予所需能力。

仪表盘令牌管理由中心侧生成节点 bearer token。创建或轮换后 secret 只显示一次；之后仪表盘和 API
只暴露节点 ID、来源、指纹与鉴权活动。把生成的 secret 写入同一节点 ID 的 edge 配置。当前
`scoot-edge` 客户端会把它作为 `Authorization: Bearer <token>` 发送；本地协议测试时就是设置
`SCOOT_EDGE_TOKEN=<secret>`，同时使用 `--node-id <node>` 和中心 `/telemetry` URL。

## systemd 单元

安装二进制，并用专用用户运行：

```ini
[Unit]
Description=Scootship center
After=network-online.target
Wants=network-online.target

[Service]
User=scootship
Group=scootship
EnvironmentFile=/etc/scootship/scootship.env
ExecStart=/usr/local/bin/scootship serve
Restart=on-failure
RestartSec=5s
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/scootship

[Install]
WantedBy=multi-user.target
```

然后执行：

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now scootship
sudo systemctl status scootship
curl -fsS http://127.0.0.1:8080/healthz
```

若使用直连 HTTPS，把 `SCOOTSHIP_BEHIND_TLS_PROXY=1` 换成 `SCOOTSHIP_TLS_CERT` 与
`SCOOTSHIP_TLS_KEY`，将 `SCOOTSHIP_ADDR` 绑定到目标地址，并用 `https://` 验证。

## 容器镜像

发布 tag 会向 GHCR 发布多架构 Linux 镜像。Docker tag 不带 Git tag 前缀 `v`：

```sh
docker pull ghcr.io/jamiesun/scootship:X.Y.Z
docker pull ghcr.io/jamiesun/scootship:X.Y.Z-alpine
```

镜像默认执行 `scootship serve`，监听 `:8080`，把状态写到 `/data`，并以非 root 用户运行。它保留
二进制相同的传输 fail-closed 行为：必须设置直连 TLS、可信 TLS 反代模式，或仅本地测试时设置
`SCOOTSHIP_DEV=1`。

可信 TLS 反代后的示例：

```sh
sudo tee /etc/scootship/scootship.container.env >/dev/null <<'ENV'
SCOOTSHIP_BEHIND_TLS_PROXY=1
SCOOTSHIP_ADMIN_PASSWORD=replace-on-first-bootstrap-only
SCOOTSHIP_NODE_TOKENS_FILE=/run/secrets/node-tokens.json
ENV
sudo chmod 0600 /etc/scootship/scootship.container.env

docker volume create scootship-data
docker run -d --name scootship \
  -p 127.0.0.1:8080:8080 \
  -v scootship-data:/data \
  -v /etc/scootship/node-tokens.json:/run/secrets/node-tokens.json:ro \
  --env-file /etc/scootship/scootship.container.env \
  ghcr.io/jamiesun/scootship:X.Y.Z
```

若使用直连 HTTPS，还要把证书和私钥以只读方式挂载进容器，并将 `SCOOTSHIP_TLS_CERT` /
`SCOOTSHIP_TLS_KEY` 指向容器内路径。不要把 `/data`、token 文件或 TLS 私钥写进镜像，也不要把真实密钥
留在 shell 历史里。挂载的 token 与 TLS 文件必须能被容器用户（`uid 65532`）读取，但不要设成
world-readable；可按部署方式使用专用组、宿主机 ACL 或 Docker secret。

## 反向代理注意事项

使用反向代理时：

- 在代理层终止公网 HTTPS。
- 将 Scootship 绑定到 `127.0.0.1` 或其他私有地址。
- `SCOOTSHIP_TRUSTED_PROXIES` 只填代理 IP/CIDR。未设置时，伪造的 `X-Forwarded-For` 会被忽略，
  登录限流使用原始连接地址。
- 转发 `Host`、`X-Forwarded-For` 与 `X-Forwarded-Proto`。
- 不要直接暴露明文 Scootship 监听地址。

## 备份

备份包含审计正文、操作员记录，并可能包含 bearer token secret。按生产凭据级别加密保存并限制访问。

推荐的静默备份：

```sh
sudo systemctl stop scootship
sudo tar --numeric-owner --xattrs -czf /secure-backups/scootship-data-$(date +%Y%m%d%H%M%S).tgz \
  -C / var/lib/scootship etc/scootship
sudo systemctl start scootship
```

如果不能停机，应使用底层卷或文件系统快照。存储是 append-only，并且 ack 前会 fsync，但服务写入期间做普通
文件复制可能截到最后一行不完整 JSONL；启动时会跳过损坏的末尾 JSONL 记录，因此优先使用停机或存储快照。

需要备份：

- `SCOOTSHIP_DATA_DIR`
- `/etc/scootship` 或等价的环境/token 配置目录
- 无法从其他系统恢复的 TLS 证书和私钥
- 用于恢复目标的确切二进制版本或 release archive

## 恢复

1. 安装相同或更新且兼容的 `scootship` 二进制。
2. 重建专用 OS 用户和目录。
3. 停止服务。
4. 恢复数据和配置归档。
5. 重新应用属主与权限：

```sh
sudo chown -R scootship:scootship /var/lib/scootship
sudo chmod 0700 /var/lib/scootship
sudo chown -R root:scootship /etc/scootship
sudo chmod 0750 /etc/scootship
sudo chmod 0640 /etc/scootship/scootship.env
sudo chown scootship:scootship /etc/scootship/node-tokens.json 2>/dev/null || true
sudo chmod 0600 /etc/scootship/node-tokens.json 2>/dev/null || true
sudo chmod 0600 /var/lib/scootship/operators.json /var/lib/scootship/managed_node_tokens.json 2>/dev/null || true
```

6. 启动 Scootship 并验证：

```sh
sudo systemctl start scootship
curl -fsS http://127.0.0.1:8080/healthz
sudo journalctl -u scootship -n 100 --no-pager
```

7. 登录仪表盘并检查：

- Fleet 页面能渲染，已知节点从 `center.jsonl` 重放出来。
- token inventory 显示预期节点指纹，不显示 secret。
- 操作员登录可用；如果备份曾暴露，应重置操作员密码。
- 测试 edge 心跳能用预期的每节点 token 通过鉴权。

恢复到新的安全边界后，轮换托管节点 token，以及任何可能随备份暴露的静态 token 文件条目。

## 失败模式

- 启动因明文 HTTP 失败：配置直连 TLS、可信 TLS 反代模式，或本地限定的 dev 模式。
- 启动因 token 文件权限失败：把 token 文件修正为普通私有文件，不得有可执行、组或其他用户权限。
- 仪表盘锁定：`operators.json` 存在但没有可用操作员，或操作员存储为空时未提供 bootstrap 密码。恢复有效的
  operator store，或先移走空/损坏的 operator 文件后，仅用 bootstrap 密码启动一次。
- 恢复后节点无法鉴权：检查 `SCOOTSHIP_NODE_TOKENS_FILE`、`managed_node_tokens.json`，以及该节点是否被
  managed revocation 覆盖。

## 禁止事项

- 不要在生产环境设置 `SCOOTSHIP_DEV=1`。
- 不要把 bearer token、TLS 私钥或真实 bootstrap 密码保存到仓库。
- 不要把备份复制到低信任工单系统或聊天记录。
- 依赖 TLS 反代时，不要暴露明文监听地址。
- 不要把本文档视为面向操作员的派发上线授权；`/jobs/lease` 只返回已持久化且绑定到节点的任务，
  仪表盘仍不暴露派发表单。
