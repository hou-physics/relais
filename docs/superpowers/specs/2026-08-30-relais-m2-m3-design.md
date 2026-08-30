# Relais M2/M3 设计增补

> 日期：2026-08-30 · 状态：定稿（Hou 口头批准范围与视觉方向 A，委托整夜自动执行）
> 基线：M1 已上线（relais-ai.com，v0.1.0-m1）。本文只写增量；未提及处沿用 M1 spec。
> 决策：D16-D18（docs/decisions.md）

---

## 1. M2 范围

### 1.1 摘要 frontmatter 贯通（自动化的核心）

**约定**：消息 Markdown 文件头（YAML frontmatter）中的 `summary:` 字段是摘要的机器可读来源。已有的 `msg.Parse` 即解析器。

- **网页导入/拖入**：`loadFileIntoBody` 检测文件是否以 `---\n` 开头；是则 `msg` 同构解析（前端用轻量正则解析首个 frontmatter 块）：提取 `summary` → 若摘要框为空则填入（人可改）；**正文框只填 frontmatter 之后的部分**（信封不重复进正文）。解析失败则按纯文本处理，不报错。
- **CLI**：`relais send 文件.md` 未给 `--summary` 时，若文件以 frontmatter 开头且含 `summary:`，用它并以 frontmatter 之后的内容为正文；两者皆无 → 维持现有必填报错。`--summary` 显式给出时永远优先。

### 1.2 草稿审批流（agent 起草，人一键发送）

- **数据**：新表 `drafts (id TEXT PK, channel_id, author_id, to_json TEXT, summary, body_md, in_reply_to, created_at)`。草稿**仅作者可见**（人与 agent 两把钥匙都限作者本人）。
- **CLI**：`relais draft` 与 `relais send` 同参数（含默认收件人规则、frontmatter 摘要），但落为草稿并打印草稿 id 与"到网页确认发送"提示。
- **API**：`GET /api/channels/{name}/drafts`（仅本人的）；`POST /api/channels/{name}/drafts`；`POST /api/drafts/{id}/send`（转正式消息：走 SaveMessage + SSE，删草稿）；`DELETE /api/drafts/{id}`。
- **网页**：时间线底部、发送框上方渲染本人草稿卡片（醒目"草稿"标签 + 摘要 + 可展开正文 + 收件人 + 【发送】【删除】按钮）。发送后卡片消失、消息即时上屏。

### 1.3 三个复制按钮 + 向导无 CLI 分支

- **"复制给 AI 的指令"**（每条消息，收件人含自己时显示）：复制文本
  `请运行 relais pull 拉取消息 <id>，阅读 relais/inbox/ 下对应文件，与我讨论后用 relais draft 起草回复（不要直接 send）。`
- **"复制原文"**（每条消息）：调 `GET /api/messages/{id}` 取 body_md，连同信封拼成完整 frontmatter Markdown 复制到剪贴板。
- **"复制 AI 格式模板"**（发送框区域）：复制一段教任何聊天 AI 生成规范 Markdown 的中文 prompt（含 frontmatter 示例、"摘要给人看/正文给对方 AI 看"的分工说明、可含表格与指令的提示）。
- **join 向导第 3 步**分栏：`用命令行 agent`（现有 guide）/ `不用命令行（网页版 ChatGPT 等）`（上述模板 + "生成后拖进发送框，摘要会自动填好"的说明）。
- 剪贴板统一用 `navigator.clipboard.writeText` + 按钮短暂变"已复制 ✓"反馈。

### 1.4 手动标记已读（D16，合法反转 D15）

- 每条 `unread` 消息在网页上显示【标记已读】小按钮 → `POST /api/messages/{id}/read`（既有 API，人钥匙的收件人可用）→ 局部刷新徽标。不做自动已读。

### 1.5 视觉方向 A：冷静工具风（整体重做 style.css + 局部结构调整）

- **配色**（浅色单主题，全部落 token）：底 `#f7f8fa`；卡片 `#ffffff`；主文字 `#1a1d21`；次级 `#6b7280`；边线 `#e5e7eb`；强调色 `#2563eb`（蓝，克制使用：按钮/链接/未读徽标/自己消息的左缘条）；成功 `#16a34a`；危险 `#dc2626`。禁渐变、禁紫色系。
- **字体**：`Inter, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif`；正文 14px/1.65；摘要 14px 500；元信息 12px；标题仅 header 一处 15px 600。等宽 `ui-monospace` 用于 id/代码。
- **布局**：header 高 48px 白底细分线（左 logo 字标"Relais"，中频道 tab 下划线式，右头像入口）；时间线最大 720px 居中；发送框吸底（sticky）白卡片带上边线；消息卡片扁平化（8px 圆角、1px 边线、无阴影），自己的消息左缘 3px 蓝条而非底色块；展开正文区浅灰底代码块、斑马纹表格。
- **头像**：`users.avatar TEXT`（存单个 emoji 或空）。空则渲染"用户名首字母 + 由用户名哈希出的 6 色之一"圆形；设置页提供 emoji 输入选择。消息卡片、成员列表、header 均显示。M2 不做图片上传。
- **设置页/退出**：header 头像点开下拉：`个人设置` `退出登录`。设置视图（同页切换）：改显示名、选头像 emoji、改密码（需旧密码，`POST /api/settings/password`）、重置 agent token（`POST /api/settings/token`，新 token 只显示一次并提示更新 CLI login）、下方"退出登录"（`POST /api/logout` 删服务器 session + 清 cookie）。
- join 向导与登录页同步换肤。

## 2. M3 范围：`relais bridge` 本地桥接（D18，合法反转 D3）

- **命令**：`relais bridge [--interval 15] [--hook "<命令>"]`，在已 init 的项目目录内运行，前台常驻（Ctrl+C 停）。
- **行为**：每 interval 秒调 `Envelopes(channel, unread)`；对每条新消息执行 `pull` 等价落盘 + 标已读，然后：
  1. **系统通知**：macOS `osascript -e 'display notification ...'`；Windows PowerShell toast（`-Command` 单行）；Linux `notify-send`；失败静默降级为终端打印。
  2. **可选 hook**：设置了 `--hook` 时以 `sh -c`（Windows `cmd /C`）执行，环境变量 `RELAIS_MSG_PATH`（落盘文件绝对路径）、`RELAIS_MSG_FROM`、`RELAIS_MSG_SUMMARY`、`RELAIS_MSG_ID`。hook 失败打印错误但不退出循环。
- **纪律**：bridge 只收不发；网络错误退避重试（连续失败翻倍至上限 5 分钟）；输出每事件一行中文。
- **文档**：AGENT.md 与 ops/README 增补 bridge 用法与"hook 可以接你自己的 AI 命令"示例（示例仅注释性质，不预置任何具体 AI 调用）。

## 3. 不做（本轮明确排除）

图片附件（原 M2 项，顺延）；头像图片上传；自动已读；网页直接唤起本地 AI（bridge+hook 是其地基）；多语言；深色模式（方向 A 单主题，深色待需求）。

## 4. 验收

- 全部 M1 锚点不变绿 + 新增：草稿仅作者可见（他人 agent/人两把钥匙均 404/403）、draft→send 转正走 SSE、frontmatter 摘要在 CLI 与网页两侧生效、密码修改后旧 session 仍有效但旧密码失效、token 重置后旧 token 401。
- 生产部署后浏览器全流程冒烟 + bridge 在本机真实跑通一次（通知可见）。

---

## 5. 增补（Hou 睡前追加，2026-08-30 深夜）

### 5.1 网页来消息通知
- SSE 收到新消息时，若页面不在前台（`document.hidden`）且浏览器通知权限已授予，发浏览器通知（标题=发件人显示名，正文=摘要，点击聚焦页面）。设置页提供"启用桌面通知"按钮触发权限请求；权限被拒则该按钮显示状态并不再打扰。

### 5.2 网页三语言（zh/en/de）
- 全部界面文案抽入词典（zh/en/de 三份），默认跟随 `navigator.language`（zh* → zh；de* → de；其余 → en），header 提供语言切换（下拉：中文/English/Deutsch），选择存 localStorage 覆盖系统默认。join 向导同样三语。CLI 输出保持中文（M2 不做 CLI i18n，见 D20）。

### 5.3 项目注册表与多项目 bridge
- `relais init` 成功后把 `{channel, 绝对路径}` 写入全局注册表 `<配置目录>/projects.toml`（同 channel 重复 init 覆盖路径）。
- `relais bridge` 不再要求在项目目录内运行：读注册表轮询**全部**已注册频道，各自消息落到各自项目的 relais/inbox/（注册表为空时退回 cwd 的 findProject 行为）。hook 环境变量增加 `RELAIS_MSG_DIR`（该项目根）。
- 这就是"本地 PhiNeuro 文件夹自动接入"的机制：文件夹 init 一次即注册，bridge 一个进程守全部项目；网页端"选择项目"即选择频道，消息天然落到对应本地文件夹。

### 5.4 仓库公开
- GitHub 仓库转 public（Hou 指示）。域名出现在 spec 属可接受（证书透明日志本已公开）；密码/IP/token 永不入库。
