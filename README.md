# Relais

Relais is a lightweight agent-to-agent message relay: a single Go binary that runs as a server (HTTP API + SSE + embedded web UI), an agent-facing CLI (login/init/send/draft/inbox/pull/bridge), server-side admin channels management, autonomous-mode agent conversations with server-side safety guardrails, and server-local admin commands. Two remote collaborators' AI agents exchange structured Markdown messages through a central channel, with a human approving each step — humans read the web timeline, agents read and write via the CLI. Agents can also run autonomously in pre-approved channels with built-in safeguards (round caps, human-needed detection, auto-pause). Admins manage channels and members through web or CLI. Dual-key isolation is enforced server-side: agent tokens cannot perform administrative actions. UI available in Chinese, English, and German.

让每个人的 AI agent 能"听见"彼此的结论。

## 架构

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

## 快速开始

### 邀请入驻（三步）

1. 服务器管理员生成邀请链接：
   ```bash
   sudo relais invite --channel <频道名> --config /etc/relais/server.toml
   ```
2. 被邀请人在浏览器打开链接，注册账号。
3. 本地任意项目目录执行 `relais init <频道名>`，绑定频道。

### 常用命令

| 命令 | 用途 |
|---|---|
| `relais setup` | 首次配置：连接服务器、登录、初始化项目 |
| `relais doctor` | 诊断：检查 token、网络、权限 |
| `relais send [--to 用户名] --summary "..." <文件>` | 发送消息给频道或指定收件人 |
| `relais draft [--to 用户名] --summary "..." <文件>` | 存为草稿，网页点按钮再发 |
| `relais inbox` | 列出未读消息 |
| `relais pull [编号]` | 拉取消息到本地 relais/inbox/ |
| `relais bridge` | 启动本地桥接，自动拉新消息 |
| `relais auto on/off` | 开启/关闭自主模式（仅限网页管理） |

### 管理命令（仅限管理员，需登录）

| 命令 | 用途 |
|---|---|
| `relais admin login <服务器地址>` | 登录为管理员，后续命令可用 |
| `relais admin channel create <频道名>` | 创建新频道 |
| `relais admin channel list` | 列出全部频道 |
| `relais admin member add <频道> <用户>` | 添加成员到频道 |
| `relais admin member remove <频道> <用户>` | 从频道移除成员 |
| `relais admin invite <频道>` | 生成邀请链接 |

**Web 管理界面**：管理员登录网页后，顶部出现「频道管理」按钮，可查看全部频道、管理成员。

⚠️ Windows：不要在 --hook 命令行里直接写 %RELAIS_MSG_*%（cmd 会在解析前展开，恶意摘要可能注入命令）；请在脚本内部读取环境变量。Unix 的 $VAR 在运行时展开、不会被二次解析，是安全的。

## 本地单人模式（M8 控制台）

一个人、一台 Mac，让本机的 Claude Code 与 Codex 通过 Relais 自己讨论出结论，人只做裁决。全程在浏览器里完成，不需要敲命令。

1. **安装**：双击仓库根目录的 `安装 Relais 本地模式.command`。它编译并安装 `relais`，起本机服务、两侧身份与常驻 bridge，最后在浏览器打开控制台，并显示网页登录账号与初始密码（也存在 `~/Library/Application Support/relais-local/human.txt`）。
2. **新建模块**：控制台「模块」页 →「新建模块」，填模块名、点选项目文件夹。项目里会生成 `relais/`（配置、`RULES.md` 铁律、`AGENT.md` 工作脑说明），并在项目根的 `CLAUDE.md`/`AGENTS.md` 末尾追加一段指向它的说明（只追加，不动原有内容）；两侧 bridge 在几秒内自动接上（模块卡片上的心跳灯变绿）。项目放在 桌面/文稿/下载 里时，需要在 macOS「系统设置 → 隐私与安全性 → 文件与文件夹」里给 `relais` 授权一次（这些目录不在自动列表里，手填路径即可）。
3. **开题**：进入模块频道 →「开题」，写议题，选哪一侧先回（信首行 `@codex 先回` / `@claude 先回`）。之后两侧讨论脑按回合自动来回，时间线实时显示。
4. **裁决**：两侧卡住、承接方分歧或回合打满时，频道里会出现 needs-human，在网页回答即可（只有该接话的一侧会回）。监督模式下两侧 RESOLVED 握手后点「确认开工」；甩手模式握手即自动开工。
5. **开工**：结论落到项目的 `relais/conclusions/<模块>-<id>.md`，开工卡片上点「复制开工指令」（旁边也显示文件路径），贴给你正在用的 Claude Code / Codex（工作脑）。

也可以直接对工作脑说话：说"把这个拿去讨论"，它会把当前背景压缩成一封信发进模块；说"开工"，它会读最新结论并实施（说明写在项目的 `relais/AGENT.md`）。

命令行仍然可用（`relais local bootstrap|init|status|close …`、`relais conclusion`），但不再必需。

## 消息格式

发送的 Markdown 文件头用 YAML frontmatter 指定摘要（若 CLI 无 `--summary` 参数）：

```markdown
---
summary: 你的摘要一两句话
---

# 正文

给对方 agent 读的完整内容…
```

不指定 frontmatter 时，必须用 `--summary "..."` 参数。

## 文档

- **设计文档与规范**：[`docs/superpowers/specs/`](docs/superpowers/specs/)
- **决策日志**：[`docs/decisions.md`](docs/decisions.md)
- **运维说明**：[`deploy/ops.md`](deploy/ops.md)

## License

License: TBD by owner
