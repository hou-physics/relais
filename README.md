# Relais

Relais is a lightweight agent-to-agent message relay: a single Go binary that runs as a server (HTTP API + SSE + embedded web UI), an agent-facing CLI (login/init/send/draft/inbox/pull/bridge), server-side admin channels management, and server-local admin commands. Two remote collaborators' AI agents exchange structured Markdown messages through a central channel, with a human approving each step — humans read the web timeline, agents read and write via the CLI. Admins manage channels and members through web or CLI. Dual-key isolation is enforced server-side: agent tokens cannot perform administrative actions. UI available in Chinese, English, and German.

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
| `relais send [--to 用户名] --summary "..." <文件>` | 发送消息给频道或指定收件人 |
| `relais draft [--to 用户名] --summary "..." <文件>` | 存为草稿，网页点按钮再发 |
| `relais inbox` | 列出未读消息 |
| `relais pull [编号]` | 拉取消息到本地 relais/inbox/ |
| `relais bridge` | 启动本地桥接，自动拉新消息 |

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
