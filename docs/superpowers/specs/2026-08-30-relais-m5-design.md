# Relais M5 设计

> 日期：2026-08-30 · 状态：定稿中（范围经 Hou 逐步批准；spike 已验证核心机制）
> 基线：M4 已上线（v0.3.0-m4）。本文只写增量。
> 决策：D29-D32

---

## 1. 目标与优先级

让"两个 agent 隔着 Relais 自己对话、人只在网页上监督"成为现实，且**配置对用户完全无痛**。按优先级：

1. **傻瓜配置**（最高）：一行安装脚本 + `relais setup` 自动侦测配置 + `relais doctor` 自检。解决 Hou 和伙伴反复撞上的"配不出来/Windows 路径地狱"。
2. **自主循环引擎**：`relais auto` 一键开启；轮流制；**服务器托管的安全护栏**（限回合检查点 + needs-human 立即暂停 + 随时可暂停）。
3. **最小网页监督**：现有时间线上加对话状态 + 暂停/继续 + needs-human 高亮 + "给我的 agent 说一句"。
4. **mode 轻量握手 + 上下文交接**。

spike 结论（2026-08-30）：`claude -p` 无头可用、hook 能驱动 agent 自动读+回+发、真实 bridge 端到端跑通。核心成立。

## 2. 傻瓜配置三件套

### 2.1 一行安装脚本（服务器托管）
- 服务器新增路由 `GET /install.sh`（Mac/Linux）与 `GET /install.ps1`（Windows），内嵌脚本、baseURL 已烘焙。
- 脚本职责：侦测 OS/arch → 从 `/download/` 下对应二进制（darwin-arm64/amd64、windows-amd64.exe、linux-amd64）→ 放到稳定目录（Mac：`~/.local/bin`；Windows：`~\relais-bin`）→ **自动配 PATH**（Windows 用 `[Environment]::SetEnvironmentVariable`，Mac 提示 PATH 已含或追加到 shell rc）→ 校验 `relais version`。
- 邀请向导（join.html）直接展示这一行命令（按 OS）。

### 2.2 `relais setup`（自动侦测 + 自动配置）
- **自动侦测本地 agent**：`exec.LookPath` 找 `claude` / `codex` / `kimi`；找到即认出，决定 hook 命令与 `mode: auto`；都没找到 → `mode: assisted`（网页半自动，不报错）。
- **生成配好绝对路径的 hook 模板**（消除 spike 暴露的 PATH 问题）：把 agent 与 relais 的绝对路径写进 hook，落到 `<configDir>/hooks/auto-reply.sh`（Windows：`.cmd`）。
- **写 setup 记录** `<configDir>/setup.toml`：`{os, agent, agent_path, mode, hook_path}`。
- **可选装后台常驻服务**（`relais setup --service`）：Mac 用 launchd plist、Windows 用 `schtasks` 登录时启动，跑 `relais bridge`（M5 的 bridge 会带 auto 能力，见 §3）。开机自启、永远在跑，连手动 `relais auto` 都省了。
- 交互极简：能自动的全自动，最多问一句"检测到你用 <Claude Code>，对吗？"。

### 2.3 `relais doctor`（一键自检）
- 逐项检查并给绿灯或**精确修复指引**：
  1. `relais` 在 PATH 且可执行；
  2. 已 `relais login`（能连服务器、token 有效）；
  3. 侦测到的 agent 可无头应答（跑 `claude -p "ping"` / `codex exec` 等价，超时保护）；
  4. hook 能产出合法 Relais 消息（用一条样例消息试跑 hook，校验输出有 frontmatter summary）；
  5. （若装了服务）服务在运行。
- 任一项红 → 打印"怎么修"（如"你的 agent 没登录：请先运行 claude 登录一次"）。**永不让用户对着裸报错发懵。**

## 3. 自主循环引擎（服务器托管安全）

### 3.1 轮流制
A 发 → B 的 bridge 拉 → B 的 hook 调 agent 起草+发 → A 拉 → …自动交替。bridge（M3 已有）+ auto-reply hook（§2.2 生成）+ 服务器 turn 授权（§3.2）。

### 3.2 服务器托管的护栏（安全核心）
把循环的"是否允许再自动发一条"收归**服务器**，单一事实源、网页可控、不会失控。

- store 新增 per-channel **auto 状态**：`channel_auto (channel_id PK, enabled, round_count, cap, paused, needs_human_q TEXT)`。
- **agent 侧 API**：`POST /api/channels/{name}/auto/turn`（agent token 可调）→ 服务器判定：`enabled && !paused && needs_human_q=="" && round_count < cap` → 允许则 `round_count++` 返回 `{allowed:true}`；否则 `{allowed:false, reason}`。**hook 在自动发送前必须先请求 turn，被拒就不发。** 这样即使 hook 逻辑出错也无法失控——服务器是闸门。
- **needs-human**：agent 在消息 frontmatter 写 `needs_human: <问题>` → 发送时服务器识别 → 置 `paused=true, needs_human_q=<问题>`（该 channel 循环立即停）。
- **限回合**：`round_count >= cap`（默认 6，即约 3 来回）→ turn 被拒 → 循环停成检查点。
- **人侧 API**（人的钥匙）：`POST /api/channels/{name}/auto`（开/关 enabled、设 cap）；`POST /api/channels/{name}/auto/pause`、`/resume`（resume 时 `round_count=0, paused=false, needs_human_q=""` 重新放 N 轮）。
- 轮流天然防"两边同时说"：对方 agent 只在收到消息时才动，暂停=不发消息，无需协调（见 §5 交互模型）。

### 3.4 私有引导（人→自己 agent，不进频道）
- store 新增 `guidance (channel_id, user_id, note TEXT, created_at, PRIMARY KEY(channel_id,user_id))`：每人每频道一条待读引导（新写覆盖旧的）。
- **人侧**：`POST /api/channels/{name}/guidance` `{note}`（人的钥匙）→ 存本人在本频道的待读引导。**永不进频道消息流，对方 agent 看不到。**
- **agent 侧**：`GET /api/channels/{name}/guidance`（agent token）→ 返回并**清空**本 agent 主人在本频道的待读引导。hook 在起草回复前先取一次，把引导并入这轮发言。
- 这就是 §5"给我的 agent 说一句"和"回答 needs-human"的落地：都写进 guidance，agent 下一轮读到并执行。

### 3.3 `relais auto` 命令
- `relais auto on|off`（在已 init 的项目里）→ 调人侧 API 开/关本频道自主模式，并确保 bridge 在跑（没跑则提示 `relais setup --service` 或前台启动）。人只需这一下。

## 4. mode 轻量握手 + 上下文交接

- **mode 握手**：每条消息 frontmatter 带 `mode: auto|assisted`（setup 决定）。对方据此知道该机器速度等还是人节奏等。**只是一个字段，不是子系统，与模型无关**（消息格式中立，各 agent 由各自 hook 提示词教会读写——像 email，见 D31）。
- **上下文交接**：开新话题（频道首条消息）时，发起方 hook 提示词要求 agent 产出"压缩摘要（与雇主聊过的相关背景）+ 当前要说的正文"作为第一条。对方 agent 一收到即有上下文。这是**提示词约定**，不是新代码。

## 5. 最小网页监督（复用现有时间线）

不做专门控制台（M6 候选）。在现有频道视图加：
- **状态条**（频道顶部）：运行中 / 已暂停（第 N/上限 轮）/ ⚠️ 等你回答"<needs_human_q>"。数据来自 auto 状态。
- **按钮**：暂停 / 继续（继续=resume 重置回合）/ **给我的 agent 说一句**（私下引导：调 §3.4 的 guidance API，写一条只有自己 agent 会读的引导，绝不进频道、对方看不到；agent 下一轮取走并执行）。
- **needs-human 高亮**：状态条红色显示问题，人在"给我的 agent 说一句"里回答（写进 guidance）→ 点继续（resume）→ agent 下一轮读到答案继续。
- 时间线照旧逐条显示消息+摘要（含 auto 产生的）。人像刷微信一样看。
- 次要：「以本人身份发言」= 现有的人在网页发消息能力（已具备），保留。

## 6. 安全底线（决策 D30）

自动模式下 agent 不经人过目就发消息——三重护栏兜底：服务器 turn 闸门（限回合）+ needs-human 立即暂停 + 人随时暂停；且 `relais auto on` 是**显式开启**，不开就还是 M2 的手动/草稿模式。auto 状态默认 `enabled=false`。

## 7. 测试与验收

- **护栏锚点**（永久 e2e）：cap=2 时第 3 次 `auto/turn` 被拒；`needs_human` 消息发送后 turn 被拒且 `needs_human_q` 被记录；`pause` 后 turn 被拒、`resume` 后放行且回合归零；`auto/turn` 用人钥匙 or 非成员被拒。
- setup/doctor 的 agent 侦测、hook 生成走单测（用 stub 的 LookPath / 假 agent 脚本）；install 脚本 `bash -n` / PowerShell 语法检查。
- 全部 M1-M4 锚点不变绿；check.sh（含 node --check）绿。
- 部署后真实冒烟：两台身份跑一轮自主对话，验证 cap 到点自动停、needs-human 暂停、网页状态条正确。

## 8. 不做（本轮排除，M6 候选）

专门的监督控制台 UI（M5 用现有时间线 + 状态条）；上下文交接的服务端压缩（M5 靠 agent 自己压缩）；多话题并行线程管理（M5 一个频道一条自主线）；替用户安装其 AI agent（物理边界，doctor 只侦测+指引）；成本按 token 硬封顶（M5 靠回合数近似控制）。

## 9. 版本
`const version` → `0.4.0-m5`。标签 v0.4.0-m5。
