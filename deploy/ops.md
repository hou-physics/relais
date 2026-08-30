# Relais 运维一页（首次部署 + 日常）

## 首次部署（阿里云轻量服务器（法兰克福节点，境外无需备案），Ubuntu 为例）
1. 买好 VPS 和域名，域名 A 记录指向服务器 IP。
2. 服务器上：`sudo apt install caddy sqlite3`；建用户 `sudo useradd -r relais`；
   `sudo mkdir -p /etc/relais /var/lib/relais/downloads && sudo chown -R relais:relais /var/lib/relais`。
3. 拷 `deploy/server.toml.example` → 服务器 `/etc/relais/server.toml`，改 `base_url` 为你的域名。
4. 拷 `deploy/Caddyfile` 内容进 `/etc/caddy/Caddyfile`（换域名），`sudo systemctl reload caddy`（HTTPS 自动签发）。
5. 拷 `deploy/relais.service` → `/etc/systemd/system/`，`sudo systemctl enable relais`。
6. 本机跑 `deploy/deploy.sh ship user@server`。
7. 服务器上建首个账号与频道：
   `sudo relais user add hou --display Hou --config /etc/relais/server.toml`
   `sudo relais channel create <频道名> --config /etc/relais/server.toml`
   `sudo relais channel add <频道名> hou --config /etc/relais/server.toml`
   `sudo relais invite --channel <频道名> --config /etc/relais/server.toml` → 把链接微信发给同伴。
8. 备份：拷 `deploy/backup.sh` → `/usr/local/bin/relais-backup.sh`，`chmod +x`，
   `sudo crontab -e` 加 `20 4 * * * /usr/local/bin/relais-backup.sh`。
9. 验证实时性：两个浏览器分别登录两个账号互发一条消息，确认秒级出现（SSE 经 Caddy 正常）。

## 管理员

种子授权（部署后跑一次，给首个管理员设权）：
```bash
sudo relais admin grant hou_physics --config /etc/relais/server.toml
```

**Web 网页管理**：管理员登录后，网页顶部出现「频道管理」按钮，可查看全部频道、添加/移除成员。

**CLI 命令**：除 `admin grant` 外均为远程 HTTP 调用（走 `relais admin login` 存下的会话 cookie），任意能连到服务器的机器上都能跑，不需要 `--config`、也不需要装在服务器上。`admin grant` 是唯一的服务器本地命令（直连数据库），必须在能访问 `/etc/relais/server.toml` 的机器（通常就是服务器本身）上执行：
```bash
relais admin login <服务器地址>                                 # 登录（先输用户名密码，存会话），此后无需 --config
relais admin channel list                                       # 列频道
relais admin channel create <名>                                # 创频道
relais admin member add <频道> <用户>                           # 添成员
relais admin member remove <频道> <用户>                        # 移成员
relais admin invite <频道>                                      # 生成邀请链接
relais admin grant <用户> --config /etc/relais/server.toml      # 授管理权（服务器本地，直连 DB）
```

**安全核心**：agent token（CLI 本地发消息用）**无任何管理权** —— 管理功能必须通过人工登录（web cookie 或 admin login session）。这是设计锁定，即使 agent token 泄露也无法操纵频道。

## 日常
- 升级：本机 `deploy/deploy.sh ship user@server`（自动重启，秒级中断）。
- 看日志：`journalctl -u relais -f`。
- 服务挂了：systemd 3 秒自动拉起；手动 `sudo systemctl restart relais`。
- 恢复备份：停服务 → 用 /var/backups/relais 里最近的 .db 覆盖 → 起服务。

## relais bridge（本地桥接，装在每个人自己的电脑上）

- 一次性：每个项目文件夹里 `relais init <频道名>`（会自动登记到本机注册表）。
- 日常：任意目录运行 `relais bridge` —— 一个进程照看全部已登记项目：新消息自动落到对应项目的
  relais/inbox/ 并弹系统通知。`--interval 秒数` 调轮询频率。
- 进阶：`relais bridge --hook '<命令>'` 在每条新消息落盘后执行你的命令（环境变量
  RELAIS_MSG_PATH / RELAIS_MSG_DIR / RELAIS_MSG_FROM / RELAIS_MSG_SUMMARY / RELAIS_MSG_ID）。
  例如接一段自己的脚本去唤起本地 AI —— 工具本身不预置任何 AI 调用。
  ⚠️ Windows：不要在 --hook 命令行里直接写 %RELAIS_MSG_*%（cmd 会在解析前展开，恶意摘要可能注入命令）；请在脚本内部读取环境变量。Unix 的 $VAR 在运行时展开、不会被二次解析，是安全的。

## 自主模式与傻瓜安装

### 一键安装与配置检查

新用户无需手工编辑配置文件：

```bash
relais setup              # 交互式配置：服务器地址、用户名、密码、频道
relais doctor             # 诊断：检查 token 有效性、服务器连通性、频道权限
```

### 自主模式（agent 无人值守对话）

频道管理员在网页端启用自主模式时指定回合上限（例如 cap=2），agent 无需人工粘贴即可自动回复：

```bash
relais auto on/off        # 切换自主模式状态（网页管理界面）
```

### 安全护栏（自动防护，无需配置）

自主模式内置三层自动防护，不依赖 agent 自觉遵守：

1. **回合上限（轮数）**：启用时设定上限（如 cap=2），第 3 轮自动拒绝发言权（无论 agent 是否尝试）。
2. **自动暂停**：agent 输出 `NEEDS_HUMAN: <问题>` 时，系统立即暂停该频道自主模式并通知人工。
3. **默认关闭**：新频道默认自主模式关闭；只有被明确开启的频道才允许 agent 自动回复。

### Agent 自主对话约定

见 `relais agent-guide`（由 `relais setup` 生成，或手工查看 `internal/guide/guide.go` 的"自主模式"章节）：

- 开新话题时进行「上下文交接」：背景摘要 + 本次内容 + 运行模式（auto/assisted）。
- 遇到需要人定夺的事项，输出 `NEEDS_HUMAN: <问题>` 暂停频道。
- 系统在每次发言前已检查发言权配额，agent 无需自己计数。
