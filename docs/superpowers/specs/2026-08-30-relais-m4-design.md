# Relais M4 设计

> 日期：2026-08-30 · 状态：定稿（范围经 Hou 批准："开工 M4"；管理员=仅 hou_physics 经 AskUserQuestion 确认）
> 基线：M2/M3 已上线（v0.2.0-m2m3）。本文只写增量。
> 决策：D25-D27

---

## 1. 目标

给所有者（hou_physics）**自助管理频道与成员**的能力，把"建频道/发邀请/拉成员"从"我 SSH 服务器代做"搬进网页 + CLI；同时修几个 M2/M3 终审延后的真实缺陷。核心安全底线：**管理能力只走"人的钥匙"，agent token 永远拿不到**。

## 2. 权限模型（安全核心）

- `users` 加列 `is_admin INTEGER NOT NULL DEFAULT 0`（幂等 ALTER 迁移，同 avatar）。
- `User` 结构体加 `IsAdmin bool`；`userCols` 增至 7 列；`scanUser` 同步；`api.Me` 加 `IsAdmin bool json:"is_admin"`；`handleMe`/`handleLogin` 填充（网页据此决定是否显示管理入口）。
- **管理操作 = 人的钥匙 + is_admin**。新增中间件 `adminOnly(h)`：`auth` 之后，若 `p.agent`（agent token）→ 403，若 `!p.user.IsAdmin` → 403，否则放行。所有 `/api/admin/*` 路由用 `s.auth(s.adminOnly(...))`。
- 种子：服务器本机命令 `relais admin grant <username> --config <server.toml>`（同 user add 直连 DB）设 is_admin=1。部署时对 hou_physics 跑一次。
- **不变量**：任何 agent token（包括管理员本人的）对 `/api/admin/*` 一律 403。这是永久锚点测试，写进决策日志 D25。

## 3. store 新增

```go
// User 加 IsAdmin bool；scanUser/userCols 含 is_admin。
func (s *Store) SetAdmin(userID int64, admin bool) error
func (s *Store) UserByName(name string) (*User, error)   // 已存在，管理"按用户名加成员"用
type ChannelStat struct{ Name string; Members int }
func (s *Store) AllChannels() ([]ChannelStat, error)      // 全部频道 + 成员数，按 name
func (s *Store) RemoveMember(channelID, userID int64) error // DELETE FROM members
// CreateChannel/ChannelByName/ListMembers/AddMember/CreateInvite 已存在
```

## 4. 管理 API（全部 `auth + adminOnly`）

- `GET  /api/admin/channels` → `[]api.ChannelStat`（全部频道 + 成员数；管理员看**所有**频道，非仅自己所在）
- `POST /api/admin/channels` body `{name}` → 建频道（名唯一，重名 400，名非空/合法字符校验）
- `GET  /api/admin/channels/{name}/members` → `[]api.Member`（管理员可看任意频道）
- `POST /api/admin/channels/{name}/members` body `{username}` → 按已有用户名加成员（用户不存在 404；已是成员则幂等 200）
- `DELETE /api/admin/channels/{name}/members/{username}` → 移除成员（204；不在则 404）
- `POST /api/admin/channels/{name}/invites` → 生成该频道邀请链接 `{url}`（管理员无需是该频道成员——区别于既有 `POST /api/invites` 要求调用者是成员）

DTO 新增：`api.ChannelStat{Name string; Members int}`、`api.AdminMemberRequest{Username string}`、`api.AdminChannelRequest{Name string}`。

## 5. 管理员 CLI（人的钥匙，与 agent token 隔离）

- `relais admin login <服务器>` —— 交互式输入用户名 + 密码（密码用 `golang.org/x/term` 隐藏回显；whitelist 已含 x/crypto，x/term 需新增，见 D27）。POST /api/login，从 `Set-Cookie` 抓 `relais_session` 值，存 `<configDir>/admin.toml` `{server, session}`（0600 权限）。
- 管理命令以 `Cookie: relais_session=<session>` 头发请求（服务器 auth 的 cookie 分支 → 人的钥匙）。**不经过 agent token**。
- 命令：
  - `relais admin channel create <名>` / `relais admin channel list`
  - `relais admin member add <频道> <用户名>` / `relais admin member remove <频道> <用户名>`
  - `relais admin invite <频道>`
- 非管理员登录后调管理命令 → 服务器 403，CLI 打印"你不是管理员"。
- `main.go` 分发 `admin` 子命令到 `internal/cli/admincli.go`（与既有服务器本机 `admin grant` 区分：`admin grant` 仅在有 `--config` 时走本机 DB，其余 admin 子命令走远程 API）。

## 6. 网页管理区（仅 is_admin 可见）

- header 用户菜单加一项 `频道管理`（`me.is_admin` 为真才渲染）。
- 管理视图（同页切换，同 settings-view 模式）：
  - 频道列表（名 + 成员数）+ 「新建频道」输入框。
  - 点某频道 → 成员列表（每人一个「移除」）+ 「添加成员」（输入已有用户名）+ 「生成邀请链接」（弹出可复制的 join URL）。
- 全走管理 API（网页本就是人的钥匙 cookie）。所有动态数据 textContent，无 innerHTML 插值。

## 7. 并入的 M2/M3 缺陷修复

1. **静态资源缓存失效根治**：包一层静态处理，给 app.js/style.css/index/join.html 响应加 `Cache-Control: no-cache` + `ETag: "<version>"`。浏览器每次带 `If-None-Match` 校验；版本号（`const version`，M2 起为 0.2.0…，M4 升到 0.3.0-m4）变了 → ETag 不同 → 自动重取。彻底解决"改了代码用户还看旧版"。
2. **无 CLI 回复协议模板（双向）**：`aiTemplate()` 内容替换为完整双向协议（教 ChatGPT 既读入站消息、又按统一格式回复——即已发给 yanxi 的那段）。按钮文案改「复制 ChatGPT 协议模板」；join 向导无 CLI 分支说明改为"粘进 ChatGPT 自定义指令一次即可"。
3. **Windows 安装向导补 PATH 指引**：join.html 第 2 步加一段 Windows 折叠说明，含把 exe 放进 `~\relais-bin`、`Unblock-File`、加 PATH 的 PowerShell 块，并提示"关掉窗口重开再登录"。
4. **时间戳本地化**：`renderMsg` 时间用 `toLocaleString(localeFor(lang))`（zh→zh-CN / en→en-US / de→de-DE）；切语言时 `refresh()` 已重渲染。
5. **切语言保留已勾选收件人**：`renderToRow` 重建前先记下已勾选的 username 集合，重建后恢复勾选。

## 8. 测试与验收

- **管理员不变量锚点**（永久，e2e + server）：对 `/api/admin/*` 每个端点，(a) 管理员的 agent token → 403，(b) 非管理员(liuyanxi8)人钥匙 → 403，(c) 管理员人钥匙 → 200。
- 频道 CRUD + 按用户名加/移成员 + 管理员邀请（无需是成员）走通。
- 静态资源响应带 `ETag` 且等于 version；`If-None-Match` 命中 → 304。
- 全部 M1/M2/M3 锚点不变绿；check.sh（含 node --check）绿。
- 部署后：hou_physics 网页可见"频道管理"、能建频道/加成员；`relais admin login` + `relais admin channel list` 走通；agent token 打 `/api/admin/channels` 得 403（真实环境复验不变量）。

## 9. 不做（本轮排除）

管理员转让/多管理员 UI（数据模型支持 is_admin 多个，但 UI 只做单管理员视角）；频道删除（避免误删数据，暂不做，需要再单独设计确认流程）；成员角色分级；审计日志。

## 10. 版本

`const version` → `0.3.0-m4`。标签 v0.3.0-m4。
