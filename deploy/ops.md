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

## 日常
- 升级：本机 `deploy/deploy.sh ship user@server`（自动重启，秒级中断）。
- 看日志：`journalctl -u relais -f`。
- 服务挂了：systemd 3 秒自动拉起；手动 `sudo systemctl restart relais`。
- 恢复备份：停服务 → 用 /var/backups/relais 里最近的 .db 覆盖 → 起服务。
