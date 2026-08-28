# sub2api-auto5h

一个独立的 Go sidecar：连接 [sub2api](https://github.com/Wei-Shaw/sub2api)，被动检测 GPT Plus、Team、Business 账号的 5 小时额度窗口，并在窗口重置后向指定账号发送一次最小文本请求，启动新的 5 小时计时。

它不修改 sub2api、不访问 sub2api 数据库，也不会调用会主动探测用量的 `usage?source=active&force=true`。检测只使用只读 quota 接口；激活只使用管理员的精确账号 test 接口。

## 功能

- 单个 Linux 二进制：Go API、持久化调度器、SQLite 与 Vue 管理面板一体交付
- 同步 1–100 个 OpenAI OAuth 账号，自动激活默认关闭
- 仅允许 Plus、Team 与 Business 系列，排除 Pro、Free、Enterprise、未知套餐和影子账号
- 每账号可覆盖文本模型、刷新后延迟、重试次数与退避基数
- 到期预检、外部激活跳过、7d 耗尽阻塞、崩溃恢复、周期去重和完整尝试审计
- 单管理员登录、Argon2id、CSRF、登录限流与加密保存的 sub2api Admin API Key
- systemd 管理；GitHub Actions 发布 Linux amd64/arm64 归档和 SHA256 校验和

## 兼容要求

- sub2api `>= 0.1.183`
- sub2api 管理员 API Key
- OpenAI OAuth 母账号的 `GET /api/v1/admin/openai/accounts/:id/quota` 能力
- Linux amd64 或 arm64，systemd

版本号只作为最低提示。首次设置会实际探测版本、账号分页和 quota 响应结构；能力或结构不匹配时不会保存连接，也不会发送激活请求。

## 安装

从 Release 下载并校验当前架构的归档，或使用安装脚本：

```bash
curl -fsSL https://raw.githubusercontent.com/MengStar-L/sub2api-auto5h/main/scripts/install.sh | sudo sh
```

安装位置：

| 内容 | 路径 |
|---|---|
| 二进制 | `/usr/local/bin/sub2api-auto5h` |
| systemd unit | `/etc/systemd/system/sub2api-auto5h.service` |
| 环境文件 | `/etc/sub2api-auto5h/sub2api-auto5h.env` |
| SQLite | `/var/lib/sub2api-auto5h/app.db` |

服务默认只监听 `127.0.0.1:8090`。查看状态和首次 setup token：

```bash
sudo systemctl status sub2api-auto5h
sudo journalctl -u sub2api-auto5h -n 30 --no-pager
```

Setup token 是一次性 32 字节随机值，有效期 30 分钟。初始化完成后 setup API 会永久关闭。若初始化前重启服务，会产生新的 token，旧 token 立即失效。

### 访问面板

最简单、安全的方式是 SSH 隧道：

```bash
ssh -L 8090:127.0.0.1:8090 your-server
```

随后打开 `http://127.0.0.1:8090`。

如需公网访问，请置于 HTTPS 反向代理之后，并把环境文件中的：

```ini
SUB2API_AUTO5H_COOKIE_SECURE=true
```

设为 `true` 后重启服务。不要把面板直接绑定到公网 HTTP 地址。

## 首次设置

向导需要：

1. journald 中的一次性 setup token
2. 本面板的管理员用户名和至少 12 字节密码
3. sub2api 根地址，例如 `https://sub2api.example.com`，不要附带 `/api` 或 `/v1`
4. sub2api Admin API Key
5. 一个可用的文本模型名称

公网地址必须使用 HTTPS。回环或字面量私网 IP 可以使用 HTTP，但必须显式勾选确认；不支持跳过 TLS 证书验证，也不会跟随 HTTP 重定向，以免泄漏 `x-api-key`。

## 调度语义

quota 的 `primary_window` 和 `secondary_window` 顺序不固定。程序严格按：

- `limit_window_seconds = 18000`：5h
- `limit_window_seconds = 604800`：7d

识别窗口。缺失整个 `rate_limit`、错型、重复或未知时长窗口都会 fail closed，账号显示为“需要处理”，不会发送请求。

启用账号时：

- 已有未来 5h reset：等待 `reset_at + 刷新后延迟`
- 7d 已用尽且 reset 在未来：等待 7d reset
- 结构有效且 5h 窗口为空或过期：立即进入 bootstrap 预检

到期时会再次读取 quota。若 reset 已前移，说明其他流量已经启动新窗口，本周期记录“外部已启动”且发送零次；否则调用：

```http
POST /api/v1/admin/accounts/:id/test
Content-Type: application/json

{"model_id":"<配置文本模型>","prompt":"hi","mode":"default"}
```

只有 SSE 终态 `{"type":"test_complete","success":true}` 表示明确成功。成功后在 10、30、60 秒进行只读验证，验证延迟不会导致再次发送。

### 关于“调用一次”

sub2api test 接口没有幂等键，因此网络层无法保证严格 exactly-once。本项目保证每个持久化周期只接受一个成功结果，并在失败后先查 quota 再决定是否重试；但在 test 超时且 quota 同时不可用时，成功优先策略可能重复发送 `hi`。默认初次尝试后最多重试 3 次，间隔 30、60、120 秒，所有尝试都会审计。

明确的认证、权限、合规确认、账号不存在或 schema 错误永不 fail-open。只有此前验证过 quota schema 的网络失败、429 或 5xx，才允许在 reset 后等待两分钟仍无法读取 quota 时发送一次成功优先请求。

## 安全

- Admin API Key 使用 AES-256-GCM、随机 nonce 与上下文 AAD 加密
- 32 字节主密钥只在 root 管理的 0600 systemd 环境文件中保存
- 主密钥缺失或错误时，`/readyz` 返回 503，自动化停止，程序绝不生成替代密钥
- 管理员密码使用 Argon2id；会话令牌只保存 SHA-256 哈希
- 会话空闲 12 小时、绝对 7 天；写操作要求 SameSite 会话、Origin 与 CSRF 同时通过
- 数据库不保存 sub2api 账号凭据、原始 SSE 或完整敏感响应

## 运维

```bash
sudo systemctl restart sub2api-auto5h
sudo journalctl -u sub2api-auto5h -f
curl -fsS http://127.0.0.1:8090/healthz
curl -fsS http://127.0.0.1:8090/readyz
```

`healthz` 只表示进程可响应；`readyz` 还验证初始化、数据库与主密钥。sub2api 暂时不可达不会让进程 liveness 失败，相关账号会进入额度重试或全局暂停状态。

### 备份与升级

安装脚本用于首次安装和升级。升级前它停止服务，并把数据库及 WAL 文件归档到状态目录，然后原子替换二进制。也可以手动停机备份：

```bash
sudo systemctl stop sub2api-auto5h
sudo tar -C /var/lib/sub2api-auto5h -czf /root/sub2api-auto5h-backup.tar.gz app.db app.db-wal app.db-shm
sudo systemctl start sub2api-auto5h
```

不要在服务运行时只复制 `app.db`，否则可能遗漏 WAL 中的数据。恢复时同时恢复数据库及 WAL/SHM，保持 `sub2api-auto5h:sub2api-auto5h` 所有权和 0600 权限。

## 真实账号上线检查

公开 CI 只使用假 sub2api，不会调用 ChatGPT。部署后建议：

1. 完成设置，只点击“同步账号”和“刷新额度”进行只读检查
2. 核对 Plus 与 Team/Business 账号的 5h/7d 窗口和套餐识别
3. 分别选择一个账号启用，并确认首次真实 `hi` 请求
4. 观察一个完整 reset 周期后再批量启用

sub2api 的 test 成功路径还会清理该账号可恢复的限流运行态。请确认自动请求符合你的账号、组织和上游服务条款；本项目不会重置额度、消耗 reset credits、导出凭据或绕过服务限制。

## 开发与构建

此仓库的正式二进制只由 GitHub Actions 构建。流水线顺序为 Vue 类型检查与单测、Vite 生产构建、Go 格式/vet/单测/race、生产二进制 Playwright 测试、无 CGO 的 Linux amd64/arm64 交叉编译与打包。

设计与实施基线保存在 `.openteams/specs/` 和 `.openteams/plans/`。许可证为 [MIT](LICENSE)。
