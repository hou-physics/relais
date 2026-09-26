# Relais M7 设计：本地单人模式（讨论脑续会话 + 双边握手 + 承接方 + 开工投递）

> 日期：2026-09-26 · 状态：**定稿待 Hou 审阅** · 基线：M5 已上线（v0.4.0-m5），M6 草案（D34）部分并入本文
> 决策：D35–D40 · 术语：根目录 `CONTEXT.md`（本文只引用）
> 来源：Hou 在 deutschapp 的 `agent-exchange/` 人肉搬信实践（78 封信）+ 两轮 grilling（Q1–Q25 全按推荐落定）

---

## 1. 动机与范围

Hou 现在的主要用法：**一台 Mac，一个人，同时开 Codex 与 Claude Code，按模块结对**。每个模块两边各一个对话，Claude 出结论、Codex 审、来回几轮直到双方都认为收敛，然后由"往上看谁主要负责"的那一侧开工，定不下来就问人。全程靠人把信复制来复制去，全局三位编号在多模块并行时撞号。

M7 把这条回路交给 Relais 自己跑，人只在三个时刻出面：开题、needs-human、（监督模式下）确认开工。

**做**：`relais local` 一条命令配好本机；讨论脑续会话；双边握手 + 承接方；监督/甩手两种模式；开工 = 投递；seq/round 编号；讨论脑只读；`RULES.md`；网页三处小改；结论幂等（M6 §4 并入）。

**不做**：联网层任何改动（邀请/管理员/install 脚本/部署一行不动，D35）；甩手模式的无头执行（D38 延后）；跨会话注入（D38）；拷问剧本与密封轮（M8，D41）；旧 agent-exchange 信件导入（D35）；Windows 本地模式（Hou 本机是 Mac，Windows 侧只保留现有联网 hook 不动）。

## 2. 总体形状

```
┌─ 本机 ──────────────────────────────────────────────────────────┐
│  relais serve (127.0.0.1:8080, ~/Library/Application Support/relais-local/) │
│        ▲ agent token(claude)          ▲ agent token(codex)                   │
│  ┌─────┴──────────┐             ┌─────┴──────────┐                          │
│  │ bridge(claude侧)│             │ bridge(codex侧) │   ← 两个 RELAIS_CONFIG_DIR │
│  │  hook → 讨论脑  │             │  hook → 讨论脑  │                          │
│  │  claude -p      │             │  codex exec     │                          │
│  │  --resume <sid> │             │  resume <sid>   │                          │
│  └─────┬──────────┘             └─────┬──────────┘                          │
│        │ 只读                          │ 只读                                  │
│        ▼                               ▼                                     │
│   <项目目录>/relais/{inbox,sent,conclusions,RULES.md}                         │
│        ▲ 开题 relais send            ▲ 开工 relais conclusion                 │
│   工作脑(交互式 Claude Code)      工作脑(交互式 Codex)                          │
│                                                                              │
│   人：网页 localhost:8080（监督台）+ 系统通知                                     │
└──────────────────────────────────────────────────────────────────────────────┘
```

一个模块 = 一个频道 = 两侧各一条讨论脑会话。频道内严格轮流，同一时刻只有一封在途（M5 轮流制不变）。

## 3. `relais local`（配置一条命令）

### 3.1 `relais local init [--codex <path>] [--claude <path>] [--project <dir>] <模块名>...`
幂等，可重复执行补模块。步骤：

1. **服务器**：若 `~/Library/Application Support/relais-local/server.toml` 不存在则写入（`listen=127.0.0.1:8080`，`data_dir=<同目录>/data`，`base_url=http://127.0.0.1:8080`），并以 `relais setup --service` 同款 launchd 方式常驻 `relais serve --config <它>`。已在跑则跳过。
2. **身份**：直接开库（复用 `relais user add` 内部函数）种子两个用户 `claude`、`codex`（display 分别为 "Claude 侧"/"Codex 侧"），加一个人的账号 `hou`（is_admin，网页登录用；初始密码打印一次）。已存在则跳过。
3. **两侧配置目录**：`<relais-local>/sides/claude/` 与 `.../sides/codex/`，各自 `config.toml`（server、token、username）——即现有 `RELAIS_CONFIG_DIR` 机制，一行不改。
4. **agent 侦测**：Claude 走现有 `detectAgent`；Codex 本机不在 PATH，`--codex` 显式给路径，写进该侧 `setup.toml`；两者都找不到则报错退出，不生成半成品。
5. **频道**：每个 `<模块名>` 建频道、加 `claude`/`codex`/`hou` 三成员；在 `--project`（默认当前目录）下 `relais init <模块名>`，两侧配置目录各登记一次 `projects.toml`；建 `<项目>/relais/RULES.md`（若无，写模板：三条空占位 + 说明）。
6. **hook**：每侧生成 `hooks/auto-reply.sh`（§5），与现有联网 hook 分文件，不覆盖。
7. **bridge 常驻**：两侧各一个 launchd 项 `relais bridge --hook <该侧 hook>`，`RELAIS_CONFIG_DIR` 指向各自目录。
8. 结尾打印：网页地址、人的账号、下一步（"在工作脑里 `relais send` 开题"）。

### 3.2 `relais local status`
只读。打印每个本地频道：模式、round/上限、状态（运行/暂停/needs-human/已握手待确认/已开工）、两侧会话 id 是否存在、bridge 是否在跑。

### 3.3 `relais local close <频道>`
频道归档（`channel_auto.closed=1`，turn 一律拒）、两侧 `sessions.toml` 里该频道条目删除（会话 id 作废）、项目目录文件保留。不删服务器数据。

### 3.4 本地模式默认值
`relais local init` 建的频道 `channel_auto` 直接 `enabled=1, cap=8`（回合上限 8，见 D39；注意 M5 的 cap 单位是"条"，本文改为"回合"，见 §7.2）。这是本地模式与联网模式的唯一默认差异：本地两个身份都是 Hou，"显式开启"（D30）的保护对象不存在。

## 4. 讨论脑：续会话（D36）

### 4.1 会话登记
每侧配置目录新增 `sessions.toml`：

```toml
[channels.grammar]      # 频道名
session_id = "0190…"    # Claude: 自生成 UUID；Codex: exec 首次返回的 thread id
created_at = "2026-09-26T…"
```

### 4.2 首次唤醒 vs 续会话
hook 起 agent 前查 `sessions.toml`：

- **无条目**（首次唤醒）：Claude 侧生成 UUID，`claude -p --session-id <uuid> …`；Codex 侧 `codex exec …`，从输出/`~/.codex` 会话文件取 thread id 写回。提示词加一句"**这是你在本频道第一次发言，先读 `relais/inbox/` 与 `relais/sent/` 下本频道全部往来**"（发起方讨论脑没看过工作脑写的第一封，D36）。
- **有条目**：`claude -p --resume <sid> …` / `codex exec resume <sid> …`，只读新信。
- resume 失败（会话文件被清）：删条目、按首次唤醒重来一次，日志记一行。

### 4.3 权限（D40）
Claude 侧：`--tools "Read,Grep,Glob" --strict-mcp-config --permission-mode default`（`--allowedTools` 只是预批准，挡不住用户 settings 的 `defaultMode: "auto"`，见 D42 ⑩）；Codex 侧：`--ignore-user-config -c 'sandbox_mode="read-only"' -c 'mcp_servers={}' --disable plugins --disable apps --disable browser_use --disable computer_use`（只读沙箱管不到 MCP 工具进程；以 `codex exec --help` 为准，常量 `codexIsolation` 见 `internal/cli/localhook.go`）。工作目录 = 项目根。讨论脑**不改文件、不跑 git、不联网**。

## 5. hook（本地版）

在 M5 三分支 hook 上改成**五分支**，文件独立（`hooks/auto-reply.sh`，本地侧配置目录下），联网侧 hook 不动。

```
1) relais auto-turn            ← 服务器闸门（不变）
2) GUIDANCE=$(relais guidance-pull)   ← 保留（不当默认入口，D39）
3) 查 sessions.toml → 首次/续会话命令（§4.2）
4) 起 agent，PROMPT = 基线规矩(§5.1) + RULES.md 全文 + 新信路径 + [首次:补读历史] + GUIDANCE
5) 按首行分支：
   RESOLVED: <一句结论>   → relais send（frontmatter 由 agent 写 kind/owner/ack_of，§6）
   NEEDS_HUMAN: <问题>    → relais needs-human "$Q"（不变）
   ---                    → relais send（普通回信）
   其他                   → 跳过
```

### 5.1 基线规矩（提示词内置，D40）
搬自 agent-exchange README：只写观点、问题、提议、决定；不替负责人做决定，需要人定的列出来用 `NEEDS_HUMAN:`；技术细节放最后"工程附录"；不写密钥、不贴数据库内容；不替对方改代码，要对方改什么写在信里。加本地专属两条：
- 认为可以收敛时，首行 `RESOLVED: <一句结论>`，frontmatter 写 `owner: claude|codex|user` 与一句理由，正文写完整结论；若是附和对方的 RESOLVED，frontmatter 加 `ack_of: <对方那封 id>`，可改措辞，**不得新增条款**（新增就写成普通回信）。
- 明确不同意对方的 RESOLVED 就写普通回信说明分歧，不写 RESOLVED。

### 5.2 幂等（M6 §4 并入）
hook 为本轮生成 `Idempotency-Key = sha256(频道 + 源消息 id + OUT 内容)[:16]`，`relais send --idempotency-key`；服务器 `sent_keys(channel_id, key, message_id, created_at, PK(channel_id,key))` 同 key 返回既有消息。保留最近 7 天。

## 6. 握手与承接方（D38）

### 6.1 信封新增字段（`internal/msg` + `internal/api`）
```yaml
seq: 15                  # 服务器发，每频道单调递增
round: 8                 # 服务器算，ceil(seq/2)
kind: resolved           # 可选：resolved | conclusion | kickoff；缺省普通
owner: codex             # kind=resolved 时必填：claude | codex | user
owner_reason: "词对线一直是 Codex 在审"   # 一句
ack_of: 01J…             # 附和时指向对方的 resolved 消息 id
```
`SendRequest` 对应加 `Kind/Owner/OwnerReason/AckOf`；`relais send` 从文件 frontmatter 读出并上送。

### 6.2 服务器判定（`handleSend` 内，发送路径）
收到 `kind=resolved` 消息 M2：
- 取 M1 = `M2.ack_of` 指向的消息。若 M1 存在且 `kind=resolved`、`M1.from != M2.from`、M1 之后本频道没有别的 `kind=resolved` 消息（人的 needs-human 回答等普通消息夹在中间不影响）、`M1.owner == M2.owner` → **握手成立**：`channel_auto.resolved=1, resolution_msg_id=M2.id, paused=1`；M2 的 kind 改写为 `conclusion`。
- 若前三条成立但 owner 不同 → 不置 resolved，改置 `needs_human_q = "承接方分歧：<M1.from> 提名 <M1.owner>（理由），<M2.from> 提名 <M2.owner>（理由），请定"`，`paused=1`。
- 否则（M1 不是 resolved，或 ack_of 缺失/不匹配）→ 当普通消息入流，循环继续；M2 只是"第一次提议"，等对方下一封。
- 若 M2 是普通回信而 M1 是 resolved → 什么也不做，讨论继续（对方不同意）。

`resolved=1` 时 `auto/turn` 一律拒（同 M6）。

### 6.3 开工（kickoff）
- **监督模式**：网页结论卡片两个按钮（人钥匙）：**确认开工** `POST /api/channels/{name}/auto/kickoff` / **继续讨论** `POST …/auto/reopen`（`ClearResolved` + resume，round 归零）。
- **甩手模式**：握手成立即服务器自动执行 kickoff。
- **kickoff 做什么**：写一条 `kind: kickoff` 系统消息（from=hou，正文 = 结论全文 + `owner`）进频道；`channel_auto.kicked_off=1`。承接方那一侧的 bridge 拉到这条 → `pullOne` 落到 `<项目>/relais/conclusions/<频道>-<结论 id>.md`（不落 inbox，不触发 hook）+ 系统通知"<频道> 已开工，承接方 <owner>"。`owner=user` 时两侧都落、通知写"由你自己做"。
- **`relais conclusion [频道]`**：打印该频道最新结论文件路径与全文，供工作脑读。人在承接方工作脑里说一句"`relais conclusion` 开工"。
- **开工后频道回到空闲**：kickoff 同时置 `resolved=0, paused=0`、round 归零、`kicked_off=1`；`resolution_msg_id` 保留指向最近结论。下一次工作脑开题（首条普通消息）清 `kicked_off`，新议题开始。

### 6.4 模式与上限（人钥匙、网页改，CLI 只读）
`channel_auto` 加 `mode TEXT NOT NULL DEFAULT 'supervised'`（`supervised|autopilot`）；全局默认存 `settings` 表（新建 `settings(key PK, value)`，`local.default_mode`）。`POST /api/channels/{name}/auto` 增 `mode`；网页状态条加模式切换。

## 7. 回合与编号（D39）

### 7.1 seq / round
`messages` 加列 `seq INTEGER NOT NULL DEFAULT 0`；插入时 `seq = (SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE channel_id=?)` 同事务；`round` 不存，读时算。系统消息（kickoff）不占 seq。

### 7.2 上限单位改为回合
`auto/turn` 判定改为 `ceil((seq_next)/2) <= cap`，`cap` 默认 8（本地模式 init 时设；联网频道保持现值不动，`round_count` 语义在 API 文档标"条数"，本地状态条显示 `round N / cap`）。打满 → 拒 turn 并置 `needs_human_q = "回合上限已到（8）。最后立场：<claude 摘要> / <codex 摘要>。再放几轮、你来裁、还是关掉？"`，`paused=1`。人回答后 resume 时若写"再放 N 轮" → `cap += N`（网页 resume 对话框加一个数字框）。

### 7.3 needs-human 的回答入口（D39）
网页 needs-human 红条的回答框改为**默认以本人身份发进频道**（`POST messages`，from=hou，`kind` 缺省）+ 自动 resume；两侧讨论脑都作为新信收到并各自续会话读到。"给我的 agent 说一句"（guidance）按钮保留在旁边。

## 8. 频道并发（D37）
`relais send` 前先 `GET /api/channels/{name}/auto`；若 `in_flight=1`（服务器：本频道最新一条消息尚未被对方读且循环 enabled）→ 拒绝，提示"频道 <名> 议题在途，开子频道：`relais local init <名>-<议题>`"。工作脑开题只能在空闲频道。

## 9. 网页三处小改（D35）
1. 结论卡片：`kind=conclusion` 绿色置顶（M6 §3 原样）+ 承接方徽标 + 两个按钮（§6.3）。
2. 状态条：模式切换（监督/甩手）、`round N / cap`、新增终态"已握手待确认 / 已开工（承接方 X）/ 已关闭"。
3. needs-human 回答框改为进频道（§7.3），旁留 guidance 按钮。
三语文案照加（zh/en/de）。

## 10. 数据模型增量（幂等迁移，同 avatar 模式）
```sql
ALTER TABLE messages ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN owner TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN owner_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN ack_of TEXT NOT NULL DEFAULT '';
ALTER TABLE channel_auto ADD COLUMN resolved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_auto ADD COLUMN resolution_msg_id TEXT NOT NULL DEFAULT '';
ALTER TABLE channel_auto ADD COLUMN mode TEXT NOT NULL DEFAULT 'supervised';
ALTER TABLE channel_auto ADD COLUMN kicked_off INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_auto ADD COLUMN closed INTEGER NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS sent_keys (channel_id, key, message_id, created_at, PRIMARY KEY(channel_id,key));
CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
```
客户端：每侧 `sessions.toml`；项目目录 `relais/conclusions/`、`relais/RULES.md`。

## 11. 安全不变量（与 M4/M5 一致）
- `kickoff`、`reopen`、模式/上限修改：**只人钥匙**，agent token → 403（永久 e2e 锚点）。
- 握手判定在服务器，hook 只是写 frontmatter；hook 出错最坏是"不握手"，不会"假握手"。
- 讨论脑只读（§4.3）；开工只投递不执行。
- 本地服务器只监听 127.0.0.1。

## 12. 测试与验收
- **锚点（永久 e2e，扩 `newWorld`）**：
  - 握手：claude `resolved(owner=codex)` → codex `resolved(ack_of, owner=codex)` → `resolved=1, paused=1`, 第二条 `kind=conclusion`，turn 被拒；owner 不同 → `needs_human_q` 含"承接方分歧"，`resolved=0`；第二条无 ack_of → 普通消息、循环继续；普通回信跟在 resolved 后 → 无变化。
  - 开工：supervised 下握手后 `kicked_off=0`，`kickoff`（人钥匙）后 `=1` 且频道出现 `kind=kickoff` 消息、承接方侧 `pullOne` 落 `relais/conclusions/`；autopilot 下握手即 `kicked_off=1`；agent token 打 `kickoff/reopen/auto(mode)` → 403。
  - seq/round：连发 5 条 seq=1..5，`round`=1,1,2,2,3；系统消息不占 seq；cap=2（回合）时第 5 条 turn 被拒且 `needs_human_q` 含"回合上限"。
  - 幂等：同 key 两次 send 只落一条；并发 N 同 key 恰好一条。
  - 在途拒绝：频道有未读时 `relais send` 返回非零并提示子频道。
- **单测**：hook 五分支优先级（RESOLVED > NEEDS_HUMAN > `---` > 跳过）；`sessions.toml` 首次/续会话/resume 失败重来；`relais local init` 幂等（跑两次状态一致，用假 agent 脚本 + 临时 HOME）；RULES.md 模板生成。
- **地板**：`./scripts/check.sh` 全绿（含 node --check），M1–M5 锚点不变绿，race-clean。
- **真实冒烟（本机）**：`relais local init grammar --codex ~/.codex/plugins/.plugin-appserver/codex` → 在一个测试项目里由工作脑 `relais send` 开题"缓存用 Redis 还是 Memcached，谁来做" → 两侧讨论脑续会话来回 → 握手 → 监督模式下网页确认 → `relais/conclusions/` 出现文件、通知弹出 → `relais conclusion` 打印。另跑一次 owner 分歧 → needs-human → 网页回答进频道 → resume → 握手。

## 13. 版本与排期
`const version` → `0.5.0-m7`。M6 草案（D34）不再单独实施：RESOLVED/结论产物/幂等三项已并入本文 §5.2/§6，`2026-09-02` 那份 spec 保留作历史。M8（拷问）另文。
