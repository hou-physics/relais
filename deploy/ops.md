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

**CLI 后台命令**（需装在服务器或运维机上，`/etc/relais/server.toml` 可访问）：
```bash
relais admin login <用户名> --config /etc/relais/server.toml    # 登录
relais admin channel list                                       # 列频道
relais admin channel create <名>                                # 创频道
relais admin channel delete <名>                                # 删频道
relais admin member add <频道> <用户>                           # 添成员
relais admin member remove <频道> <用户>                        # 移成员
relais admin grant <用户> --config /etc/relais/server.toml      # 授管理权
relais admin revoke <用户> --config /etc/relais/server.toml     # 撤管理权
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
