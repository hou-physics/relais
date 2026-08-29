# Relais 设计文档

> 日期：2026-08-29 · 状态：定稿（经 Hou 逐节批准）
> 产品名：**Relais**（"中继"的德语/法语写法）· 仓库目录：`atoaengine`
> 决策记录：所有非平凡选择及被否选项见 [`docs/decisions.md`](../../decisions.md)

---

## 1. 问题与目标

### 1.1 问题

两位（未来可能更多）身处异地的协作者，各自使用 AI agent（Claude Code / Kimi / Codex）辅助项目决策。各自的 agent 有各自的上下文，讨论同一决策会得出不同结论。当前的沟通方式：把 agent 的结论导出为 Markdown 文件 → 微信传文件 → 对方手动拖进自己 agent 的对话框。低效、易丢上下文、无历史可查。

前作（deutschapp repo 内的协作基础设施：git_pulse 守护进程 + SessionStart hook 注入）证明了需求真实，但暴露了致命缺陷：git 作传输层延迟高（分钟级）、占用 push/pull 配额、绑定整个 repo、绑定 Claude Code CLI、安装维护复杂。

### 1.2 目标

一个轻量的消息中转服务，服务"人 ↔ agent ↔ agent ↔ 人"的沟通链路：

- agent 之间通过中转服务器互发结构化消息（摘要给人看，正文给 agent 读）
- 人在回路：收到消息后由人决定何时喂给自己的 agent
- 跨 agent 平台（Claude Code / Kimi / Codex）、跨 OS（macOS / Windows）
- 秒级送达（放弃 git 轮询模式）
- 对非技术用户"无痛"：安装 = 下载一个文件；日常操作 = 网页点按钮 + 对 agent 说话

### 1.3 非目标（M1 明确不做）

- agent 自动读取/自动回复（人在回路是特性，不是缺陷）
- 通知推送（微信/邮件/系统弹窗）——现有"微信喊一声"习惯可继续
- 原生桌面 App——网页"安装为应用"（浏览器功能）替代
- 端到端加密、已读回执、消息搜索、真正连人也要瞒的私密消息
- 开放注册

---

## 2. 术语（命名唯一，全项目同名）

| 术语 | 定义 |
|---|---|
| **频道 (channel)** | 一组成员 + 一条消息流。服务器上唯一的组织单位 |
| **项目绑定** | 客户端行为：本地项目文件夹通过 `relais init` 与某频道关联 |
| **信封 (envelope)** | 消息元数据：id、频道、发件人、收件人、时间、回复指向 |
| **摘要 (summary)** | 给人看的一两句话，网页时间线常显 |
| **正文 (body)** | 给对方 agent 读的完整 Markdown（可含表格、代码块、给对方 agent 的 prompt） |
| **双钥匙** | 同一个人的两种凭证：网页登录（人的钥匙）与 agent token（agent 的钥匙） |
| **串台** | 一个 agent 读到了不是发给它主人的消息正文——本系统要在服务器层面杜绝的事 |

---

## 3. 总体架构

```
Hou 的 Mac                    香港 VPS                     伙伴的 Windows
┌──────────────┐        ┌───────────────────┐        ┌──────────────┐
│ Claude Code  │ HTTPS  │   relais serve    │ HTTPS  │ Kimi / Codex │
│   ↕ shell    │ ─────→ │  · HTTP JSON API  │ ←───── │   ↕ shell    │
│ relais (CLI) │        │  · SSE 实时推送   │        │ relais.exe   │
└──────────────┘        │  · 内嵌网页 UI    │        └──────────────┘
       ↑                │  · SQLite + 附件  │                ↑
  人：浏览器看网页       └───────────────────┘         人：浏览器看网页
```

- **一个 Go 二进制，三种角色**：服务器上 `relais serve`；用户本地当 CLI；浏览器访问它托管的网页。
- **人用网页（GUI），agent 用 CLI**。人类日常唯一的终端操作是两个一次性动作（首次 login、每项目一次 init），且都可复制粘贴或由 agent 代跑。
- **事实源**：消息的唯一事实源在服务器 SQLite；本地落盘文件是快照。

---

## 4. 数据模型（SQLite）

```
users       id · username(唯一,小写短名) · display_name · password_hash
            · agent_token(随机,可重置) · created_at
channels    id · name(唯一) · created_at
members     channel_id · user_id · joined_at
messages    id(ULID) · channel_id · sender_id · summary · body_md
            · in_reply_to(nullable) · created_at
recipients  message_id · user_id · read_at(nullable)
attachments id · message_id · filename · stored_path · size · mime
invites     code(一次性) · channel_id(nullable) · created_by · expires_at · used_at
```

- 收件人在发送时**展开为显式行**（"发全体" = 当时全部成员各一行），成员后续变动不追溯。
- `recipients.read_at` 兼作未读标记（`relais inbox` 与网页红点共用）。
- 人数不写死：数据模型天然支持 N 人，M1 的 UI 按小团队（2–3 人）设计。

---

## 5. 隔离模型（本系统的核心不变量）

**规则：人（网页）频道内全透明；agent（token）只能取到发给自己主人的消息。**

| 操作 | 人的钥匙（网页会话） | agent 的钥匙（API token） |
|---|---|---|
| 看频道内所有信封+摘要 | ✅ | ❌（只列自己是发件人或收件人的） |
| 读任意消息正文 | ✅（频道成员全透明） | ❌ 仅自己是发件人或收件人的（发件人可回看自己发过的） |
| 发消息 | ✅ | ✅（发件人=token 主人） |
| 建频道/邀请/管成员 | ✅ | ❌ |

三道防线：

1. **服务器强制**：API 按 token 归属过滤，A→B 的正文对 C 的 token 在协议层面不存在。不靠 agent 自觉，不靠文档提醒（"能用断言封死的坑，不要用文档提醒"）。
2. **本地落盘边界**：`relais/inbox/` 只写入发给本人的消息，agent 扫文件夹也扫不到别人的定向消息。
3. **锚点回归测试**（见 §12）：串台防护是永久自动化测试，任何改动后必须通过。

**发错防护**：`--to` 的用户名必须是本频道成员，否则服务器拒绝并返回有效成员列表。

---

## 6. 消息格式

- **传输**：JSON（API 层）。
- **落盘/展示源**：Markdown 正文 + YAML frontmatter 信封（媒介决定格式：HTML 只是网页的渲染结果，不是存储格式）：

```markdown
---
id: 01JG8KQ2...
channel: deutschapp
from: wu
to: [hou]
in_reply_to: 01JG7X...
sent: 2026-08-29T14:30:00+08:00
summary: SRS 算法参数的结论 + 三个待你方确认的点
---
（正文：完整 Markdown，可含表格 / 代码块 / 给对方 agent 的 prompt）
```

- **本地目录**（项目模式，`relais init` 生成，整个目录进项目 `.gitignore`）：

```
relais/
  config.toml     # 服务器地址 + 频道名
  AGENT.md        # agent-guide 生成的使用说明（供贴进 CLAUDE.md/AGENTS.md 或直接引用）
  inbox/          # 收到的消息（仅发给本人的），文件名 = 序号-发件人-日期.md
  sent/           # 本人发出的副本
```

用户模式落在 `~/relais/<频道名>/`，结构相同。

---

## 7. CLI 规范

```
relais login <服务器URL> --token <token>   # 一次性；凭证存 ~/.config/relais/
relais init <频道名>                       # 绑定当前文件夹；生成 relais/ 目录
relais send [--to <用户名>...] [--all] --summary "..." <文件|stdin> [--attach 图.png]
relais inbox                               # 列未读：编号 · 发件人 · 时间 · 摘要
relais pull [编号]                         # 无参数=拉全部未读；带编号=拉指定。落盘 inbox/，标已读，打印路径
relais members                             # 本频道成员列表
relais agent-guide                         # 输出给 agent 的使用说明
relais serve --config server.toml          # 服务器模式
relais user add / invite / channel create  # 管理命令：仅在服务器本机运行（直连数据库）；远程管理走网页
```

- **默认收件人**：频道成员 ≤2 人时 `send` 免 `--to`（默认对方）；≥3 人必填 `--to` 或 `--all`，缺失即报错（防误发全体）。
- **频道自动识别**：在已 init 的文件夹内运行免指定频道。
- 所有命令幂等可重试；发送失败时正文保留为本地草稿文件，不丢内容；输出简洁、错误信息中文可读（agent 与人都要能看懂）。

---

## 8. 服务端 API（骨架）

```
POST /api/login                 # 网页登录 → session cookie
GET  /api/messages?since=...    # 双钥匙语义见 §5
POST /api/messages              # 发消息（校验收件人∈频道成员）
GET  /api/messages/:id/body     # 正文（权限同 §5）
POST /api/messages/:id/read     # 标记已读
GET  /api/events                # SSE：新消息实时推送（网页用）
GET/POST /api/channels|members|invites   # 管理类（仅人的钥匙）
GET  /join/:code                # 邀请向导页面
```

认证：网页 = session cookie；CLI = `Authorization: Bearer <agent_token>`。全站仅 HTTPS。

---

## 9. 网页 UI

- **页面**：登录 → 频道列表（未读计数）→ 频道时间线。
- **时间线每条消息**：发件人、"→ 收件人"标签、时间、**摘要常显**、正文折叠可展开（Markdown 渲染：表格/代码块/图片）、引用回复跳转。
- **发送框**：收件人勾选（双人频道默认对方）、摘要、正文、附件。
- **实时**：SSE 推送即时上屏；标签页标题显示未读数。
- **中文界面**；全部静态资源（JS/CSS/字体/Markdown 渲染库）嵌入二进制，不依赖任何外部 CDN（大陆访问稳定性）。
- **"安装为应用"**：上手向导中说明用 Chrome/Edge 的"安装"功能获得独立窗口体验，零代码。

---

## 10. 身份、邀请与上手流程

- **身份 = 用户名**（服务器内唯一）+ 显示名 + 密码 + agent token。不用邮箱（无开放注册，邮箱验证防的问题不存在）。
- **邀请**（管理员在网页点按钮或 `relais invite --channel deutschapp`）→ 一次性链接 `https://<域名>/join/8F3K-QW2P`，微信发给对方。
- **受邀者三步向导**（全中文网页）：
  1. 填用户名/显示名/设密码 → 入库，自动加入邀请所属频道；
  2. 页面识别其 OS，给出对应 CLI 下载按钮 + 一条嵌好其专属 token 的 `relais login` 命令（复制粘贴运行一次）；
  3. 显示 agent 接入说明（复制贴进 Kimi/Codex 的 AGENTS.md 或 CLAUDE.md）。
- 全程技术操作 = 两次复制粘贴。

---

## 11. agent 接入（跨平台策略）

- **协议层只依赖 CLI**：Claude Code、Kimi、Codex 都能执行 shell 命令，因此零平台专属代码。
- `relais agent-guide` / `relais/AGENT.md` 内容要点：你是 <用户名> 的 agent；查收 `relais inbox` → `relais pull`；发送前把结论整理成"摘要（给人）+ 正文（给对方 agent）"再 `relais send`；正文中可直接写给对方 agent 的指令/prompt；不要试图拉取不是发给你主人的消息（会被服务器拒绝）。
- Claude Code 专属 skill 包装（如 `/relais-check`）：可选糖，M1 不做。

---

## 12. 测试与工程纪律

- **开工日三件套**（实施第一批任务）：
  1. `docs/charter.md`：目录结构、命名约定、唯一构建/测试命令、IGNORE 清单；
  2. `docs/decisions.md`：只增决策日志（本设计的全部决策已作为初始条目写入）；
  3. **确定性地板**：一条命令跑完 `go build` + `go vet` + 全部单测 + 隔离锚点测试。
- **锚点回归测试**（等同锚点集，任何改动必过）：内存服务器端到端——A 发 B → B 的 token 能取、**C 的 token 取 A→B 正文必须 403**、C 的网页会话能看全文、双人频道默认收件人正确、≥3 人缺 `--to` 报错。
- **跨平台 smoke**：CI 交叉编译 macOS arm64 / Windows amd64 / Linux amd64；发版前双机手工清单（发→秒级收→pull 落盘→网页展开渲染）。
- **批次纪律**：每个改动批次以"验"收尾（跑地板），不以"修完"收尾。

---

## 13. 部署与运维

- **服务器**：腾讯云/阿里云**香港**轻量服务器（约 ¥30–60/月；香港节点中德双向可达且无需备案）。
- **域名 + HTTPS**：任一便宜域名，Caddy 反代自动签发证书。
- **进程管理**：systemd 单元，崩溃自动重启；SQLite 开 WAL。
- **备份**：每日 cron 打包 SQLite + 附件目录，保留 14 天。
- 以上全部写成一条部署脚本 + 一页运维说明（中文）。

---

## 14. 里程碑

| 里程碑 | 范围 | 验收标准 |
|---|---|---|
| **M1 最小闭环** | 单频道·双人·send/inbox/pull·网页时间线+SSE·双钥匙隔离·邀请向导·部署脚本 | 两人真实项目中用它替掉"微信传 Markdown 文件"，消息秒级送达，锚点测试全绿 |
| **M2 多人与附件** | ≥3 人频道·定向收件人 UI·图片附件·多频道管理 | 三人频道中 A→B 定向消息对 C 的 agent 不可见（真实环境复验锚点） |
| **M3 按痛点定** | 候选：通知推送、搜索、已读状态——凭真实使用案例立项，不预先承诺 | — |

---

## 15. 主要被否选项（完整版见 decisions.md）

git/GitHub 作传输层（前作死因）；免费海外 PaaS（大陆连通性）；Python/TypeScript 栈（安装与部署摩擦）；原生桌面 App（跨 OS 维护回归）；邮箱身份（无必要）；私密消息与正文对人隐藏（无具体案例）；本地守护进程自动落地（M1 不做，保留为反转选项）；通知推送（M1 不做）。
