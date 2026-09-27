# Relais M9 设计：接入现有对话（守卫 + 门铃 + 传输协议）与本地控制台重做

> 日期：2026-09-27 · 状态：**定稿待 Hou 审阅** · 基线：main b78911e（M7 + M8 + 两处热修）
> 决策：D45 · 术语：`CONTEXT.md`「接入现有对话（M9 起）」一节 · 原 M9 拷问顺延为 M10
> 来源：Hou 2026-09-27 第一次真实使用后的反馈："我想在两边客户端都能看见对话框，好知道发生了什么，也好直接介入"；"这更像一个进程守卫"；"工具提供一套只管传信不管内容的通信协议"；"尽可能轻量、UI 重做、砍掉本地用不上的功能"。

---

## 1. 目标

M7/M8 把讨论交给两个**无头**讨论脑，人只能在网页看结果。Hou 要的是相反的形状：讨论就发生在他本来就开着的那两个对话里（Claude Code 一个、Codex 一个），他随时能看、能插话；工具只是搬信的。M9 把 Relais 本地模式改成：

- **守卫层**：一个常驻进程，把信从一侧的项目目录搬进数据库、再投到另一侧的对话里。
- **门铃层**：每个对话里跑的一条小命令，让对话知道"信到了"。
- **协议**：一份两侧共读的 `relais/PROTOCOL.md`，只规定信怎么走，不规定信里写什么。
- **控制台**：本地独立一套页面，无登录，只展示"现在在发生什么 / 该你做什么 / 往来与设置"。

**不做**：改联网层（D35）；三方以上；拷问剧本（M10）；原生 app；给信的正文任何模板或小标题。

## 2. 探针结论（2026-09-27 实测，决定架构）

| 侧 | 外部能否往现有对话塞消息 | 采用 |
|---|---|---|
| Codex 桌面版 0.155 | 能：`codex queue --thread <id 或对话名> --message <文本>`。对闲置对话实测 10 秒内被消费并回复。队列落在 `~/.codex/queue_1.sqlite`，线程清单在 `~/.codex/state_*.sqlite` 的 `threads` 表（`cwd`、`name`、`title`、`updated_at_ms`）。 | **推送**：守卫直接 queue |
| Claude Code 2.1 | 不能：会话 socket（`~/.claude/sessions/<pid>.json` 的 `messagingSocketPath`）是带 peer token 的私有协议。运行中的会话可列出（`cwd`、`name`、`status`）。 | **门铃**：对话里后台跑 `relais wait` |

两侧的沙箱都可能禁网（Codex 默认连 127.0.0.1 都要审批）。因此 **agent 侧的命令一律只读写项目目录里的文件，不发 HTTP**；网络只存在于守卫进程与控制台之间。

## 3. 总体形状

```
Claude Code 对话 ──relais post──▶ relais/mail/<模块>/outbox/ ──┐
      ▲ relais wait（盯文件）                                    │ 守卫每 2 秒扫
      │                                                          ▼
      └──── relais/mail/<模块>/NNN-<from>.md ◀── relais serve（本地模式）──▶ SQLite（事实源）
                                                    │                          ▲
Codex 对话 ◀── codex queue --thread ──────────────┘                          │
      └──relais post──▶ outbox/ ─────────────────────────────────────────────┘
控制台（浏览器，回环，无登录）──── /api/… ────▶ relais serve
```

- **事实源**仍是服务器 SQLite；项目目录里的信件文件是投递副本，两侧对话都直接读它。
- **常驻只有一个**：`com.relais.local.server`（`relais serve`）。M7 的两个 bridge 常驻、无头讨论脑、hook 全部取消（§11）。
- 一个项目目录可承载多个模块；一个模块 = 一个频道 = 一个 `relais/mail/<模块>/` 目录。

## 4. 信箱布局与命名（协议的一部分）

```
<项目>/relais/
  PROTOCOL.md                 两侧共读的传输协议（工具生成，§5）
  mail/<模块>/
    001-hou.md                归档信件：三位序号-发件人。序号由服务器发，全模块单调递增
    002-claude.md
    003-codex.md
    007-relais.md             工具发的系统信（开工通知等），发件人 relais
    conclusion-006.md         握手结论（序号 = 服务器生成的那条 kind=conclusion 消息的 seq，与同序号的 006-relais.md 对应）
    outbox/                   待发：relais post 放进来，守卫取走即删
    drafts/                   草稿区，工具不碰
    .wait-claude              Claude 侧正在等信（pid + 开始时间；wait 退出即删）
    .attach-codex             Codex 侧接入登记（thread_id、name、at）
    .cursor-<侧>              该侧已看到的最大序号（wait 退出时写）
```

- 序号三位、不够进位到四位；文件名不含信的标题，标题在信头 `summary:`。
- 旧的 `relais/inbox/`、`relais/sent/`、`relais/conclusions/` 不再写入，也不删（§11）。
- 模块名规则沿用 M7：不含 `/`、`..`、首尾空白；控制台与 CLI 同一校验。

### 4.1 信封（工具填，agent 不填）

归档文件头：

```yaml
---
id: 01M3…            # 服务器 ULID
module: 黑客松巡天智能体主对话
seq: 7
from: codex          # claude | codex | hou | relais
date: 2026-09-27T21:10:03+02:00
kind: letter         # letter | resolved | needs-human | conclusion | kickoff
reply_to: 6          # 对方（或雇主）上一封的序号，自动；首封为空
owner: codex         # 仅 resolved / conclusion / kickoff
ack_of: 5            # 仅附和的 resolved：被附和那封的序号
summary: 正文首行（去掉 # 与前后空白，最多 80 字）
---
<正文原样>
```

`relais post` 写进 outbox 的文件只带 `from:`、`kind:`、`owner:`、`ack_of:` 四个字段中用到的（由命令行标志转出）；其余由守卫入库时补齐后重写为归档文件。正文一个字不动。

## 5. `relais/PROTOCOL.md`（工具生成，每个项目一份）

由 `internal/guide/protocol.go` 生成，`CreateModule` 与升级时写入（幂等覆盖：文件头有 `<!-- relais-protocol v1 -->` 标记才覆盖，否则不动并在控制台提示）。内容**只**有这些段落，中文：

1. **你是谁**：Claude Code 就是 `claude`，Codex 就是 `codex`。命令能从环境自动判断（§6.4），判断不了时要带 `--as`。
2. **接入**：雇主说「接入 relais 模块 X」时——Claude：在后台运行 `relais wait X`；Codex：运行 `relais attach X`。接入后什么都不用再做，信到了会有通知。
3. **收信**：通知里给的是文件路径；只读 `relais/mail/X/` 下的信，按序号读；别的模块的目录不读。
4. **回信**：把正文写成一个 `.md` 文件（建议放 `relais/mail/X/drafts/`），首行是一句摘要，然后运行 `relais post X <文件>`。信封由工具填。三种标记：
   - `--resolved --owner claude|codex|user`：我认为可以收敛，并提名承接方。
   - `--ack`：我附和对方最近一封收敛提议（承接方自动取对方那封的）。
   - `--needs-human`：需要雇主定夺；文件首行就是问题。
5. **发完之后**：Claude：再在后台运行一次 `relais wait X`；Codex：不用做。
6. **收敛怎么算**：双方各一封 `resolved`、第二封带 `--ack`、承接方一致，即握手。结论落在 `conclusion-<序号>.md`，承接方会收到开工通知（发件人 `relais`）。承接方是 `user` 时两侧都只等雇主。
7. **雇主的信**：通知里写明是不是由你先回；不是你就不回，等对方。
8. **不要**：改别人的信、往 `outbox/` 以外的地方写信、自己写 `conclusion-*.md`、发 HTTP 给 Relais。

正文怎么写、信里放什么小标题、先干活还是先回信——协议一句不提（D45）。

`CLAUDE.md` / `AGENTS.md` 的指针块（M8 D44 ①）改为两行：

```
<!-- relais-local -->
本项目接入了 Relais。雇主说「接入 relais 模块 X」时，读 relais/PROTOCOL.md 并照做。
```

## 6. agent 侧命令（纯文件，不联网）

三条命令都从当前目录向上找 `relais/mail/<模块>/`（找不到 → 错误并列出本目录有哪些模块）。

### 6.1 `relais post <模块> <文件> [--resolved --owner X | --ack | --needs-human] [--as claude|codex]`
- 校验：文件非空；`--resolved` 必带 `--owner`；`--ack` 与 `--resolved`/`--owner` 互斥；`--ack` 时 `relais/mail/<模块>/` 里必须存在对方的 `kind: resolved` 信（取序号最大的一封，其 `seq` 写入 `ack_of`）。
- 把文件复制为 `outbox/<侧>-<ULID>.md`（加 `from:`/`kind:`/`owner:`/`ack_of:` 头），原文件不动。
- 输出一行：`已交给 relais（模块 X，来自 claude）。` Claude 侧再加一句 `现在在后台运行：relais wait X`。
- 守卫不在跑时文件照样留在 outbox，守卫回来即取。

### 6.2 `relais wait <模块> [--as claude] [--timeout <时长>]`
- 写 `.wait-claude`（pid、开始时间）。
- 若已有序号大于 `.cursor-claude` 且发件人不是本侧的归档信，立即返回（信在自己回信期间到了也不会漏）。
- 否则每秒扫一次 `mail/<模块>/`，出现符合条件的信即返回。多封一起到就全列出。
- 返回时打印每封一行：`第 7 封 来自 codex：/绝对路径/007-codex.md`，若其中有 `kind: kickoff` 再加一行 `你是承接方，读 conclusion-006.md 开工`（或 `承接方是 codex，本侧不用开工`）；若是雇主的信（`from: hou`）再加一行 `由你先回` 或 `由 codex 先回，本侧不用回`（接话规则同 §7.3，只看文件即可算出：首行 `@side 先回` 优先，否则是最近一封 agent 信的另一侧，尚无 agent 信时 claude 先回）。
- `.wait-claude` 里附带 `CLAUDE_CODE_SESSION_ID`（Claude Code 会设置该环境变量），控制台据此从 `~/.claude/sessions/*.json` 查出对话名显示为 `在等信 · 对话「巡天黑客松主对话」`；查不到只显示时间。
- 退出前写 `.cursor-claude`、删 `.wait-claude`。默认不超时；`--timeout` 到点返回并打印 `没等到新信，继续等请再运行一次`。
- Codex 侧也可以用（`--as codex`），但协议不要求。

### 6.3 `relais attach <模块> [--as codex] [--thread <id 或名>]`
- Codex 侧：不带 `--thread` 时读 `$CODEX_HOME`（默认 `~/.codex`）下最新的 `state_*.sqlite`，取 `threads` 表里 `archived=0` 且 `cwd` 等于项目目录（比较真实路径，兼容 `/private` 前缀）且 `updated_at_ms` 最大的一条——正在执行本命令的对话就是它。写 `.attach-codex`（`thread_id`、`name`、`title` 前 60 字、`at`）。打印 `已接入 Codex 对话「<name 或 title>」`。查不到（表结构变了、没有匹配）→ 错误并提示到控制台里点选。
- Claude 侧：`relais attach X --as claude` 等价于提示"Claude 侧的接入就是运行 relais wait X"，不落文件。
- 只读 Codex 的数据库（`?mode=ro`），永不写。

### 6.4 侧的自动判断
`CLAUDECODE` 环境变量存在 → `claude`；`CODEX_SANDBOX`、`CODEX_SANDBOX_NETWORK_DISABLED` 或 `CODEX_HOME` 任一存在 → `codex`；两者皆无或冲突 → 必须 `--as`，否则报错说明原因。`--as` 永远优先。

## 7. 守卫：`relais serve` 本地模式下的投递循环

`server.toml` 有 `local_dir` 时，`serve` 启动一个 goroutine，每 2 秒一轮，对登记表（§10 `local_modules`）里每个未关闭模块依次做三步。每步独立出错、独立记日志（`~/Library/Logs/com.relais.local.server.log`），不影响其它模块。

### 7.1 收件：扫 `outbox/`
- 按文件名排序逐个处理：解析头 → 校验 `from` 是 `claude|codex` → 调 store 的发消息路径（与 `handleSend` 同一函数，收件人 = 另一侧 + hou；`kind`/`owner`/`ack_of` 照头；`ack_of` 从序号换成消息 id）→ 成功后删 outbox 文件。解析失败的文件改名 `*.rejected` 并在控制台显示原因。
- `kind: needs-human`：入库为普通信 + 调用 M7 的 needs-human 状态（问题 = summary）。
- 回合上限、握手判定、supervised/autopilot、kickoff 全部沿用 M7 服务器逻辑（`EvaluateHandshake`、`channel_auto`）。**取消**在途拒绝与严格轮流：本地频道同一侧连发两封照收（D45）。
- 幂等：outbox 文件名里的 ULID 作为 `sent_key`，重复扫到不重复入库。

### 7.2 归档：把新消息写成文件
- 取该频道 `seq > local_modules.archived_seq` 的消息（来源不限：outbox、控制台、握手产物），按 seq 写 `NNN-<from>.md`（§4.1 信封），写完推进 `archived_seq`。
- `kind: conclusion` 的信额外写 `conclusion-<seq>.md`（正文 + `owner`），一次写成、`rename` 落位。
- `kind: conclusion` 与 `kind: kickoff` 的归档文件 `from:` 一律写 `relais`（数据库里的发件人可能是点确认的人或完成握手的 agent，对读信的一侧没有意义）。
- 雇主在控制台写的信也走这里：`NNN-hou.md`。

### 7.3 投递：通知对方
对每封新归档信，收件人里的每一侧：
- **claude**：什么都不做（wait 盯文件）。
- **codex**：有 `.attach-codex` 则执行 `<codex 路径> queue --thread <thread_id> --message <通知文本>`，超时 20 秒。通知文本由工具生成（协议层，不含正文）：
  - 来信：`Relais 模块「X」第 7 封来信（来自 claude）：<绝对路径>。读它，按 relais/PROTOCOL.md 回信。`
  - 雇主的信：`雇主在 Relais 模块「X」说话了（第 8 封）：<路径>。由你先回。` / `…先看，这封由 claude 先回，你不用回。`（接话规则沿用 D42 ⑦ + M8 §6 的 `@side 先回`）
  - 开工：`Relais 模块「X」已握手，你是承接方：读 <conclusion 路径> 开工。` / `…承接方是 claude，你不用开工。`
  - 失败（非零退出、超时、无 `.attach-codex`）：记 `local_modules.codex_delivery_error`，控制台显示并给「重新投递」按钮，桌面通知一次。
- 每封信投递结果记在 `local_deliveries(message_id, side, status, error, at)`，控制台据此显示"已投给 Codex，等回信 3 分钟"。

### 7.4 状态（控制台"现在"一栏的事实来源，`GET /api/local/modules`）
每个模块返回：`name, dir, mode, round, round_cap, state, last_seq, last_from, last_at, waiting_for, claude{waiting, wait_since, cursor}, codex{attached, thread_name, attached_at, last_delivery{status,error,at}}, needs_human{question,since}, pending_conclusion{seq, owner, awaiting_confirm}`。
- `claude.waiting` = `.wait-claude` 存在且 pid 活着（`kill -0`）。
- `waiting_for` = 最后一封 agent 信的另一侧；雇主的信按接话规则；握手后 = 承接方或 `user`。
- `state`：`未接入`（两侧都没接）/ `讨论中` / `等你`（needs-human、待确认开工、回合到顶、投递失败）/ `已握手` / `已开工` / `已关闭`。

### 7.5 桌面通知
只在"需要你"时弹（needs-human、待确认开工、回合到顶、投递失败、outbox 文件被拒），走 M8 热修后的 osascript argv 路径。设置项 `notify_every_letter` 打开时每封信也弹一条。

## 8. 本地 API 与鉴权

- **回环免钥匙**：`local_dir` 非空时，来自回环地址、不带 `Authorization` 的请求视为人（`hou`）；带 agent token 的仍按 token；非回环一律 401（原样）。无 `local_dir`（线上）不存在这条捷径（永久锚点）。单人本机，任何本地进程都能以 hou 身份发言——接受（D45）。
- 现有 `/api/local/*`（模块增/列/关、repos、settings）保留并改造；`heartbeat` 删除（没有 bridge 了）。
- 新增：

| 方法 路径 | 作用 |
|---|---|
| `PATCH /api/local/modules/{name}` `{name?, mode?, round_cap?}` | 改名 / 改模式 / 改上限。改名 = 改频道名 + 把 `relais/mail/<旧>` 整目录改名（目标已存在 → 409）；`PROTOCOL.md` 无需改（不含模块名） |
| `POST /api/local/modules/{name}/reopen` | 重开已关闭模块（`closed_at` 清空，频道 `closed=0`，auto 重新 enabled） |
| `DELETE /api/local/modules/{name}?files=1` | 删模块：删频道与消息记录、登记表行；`files=1` 时才删 `relais/mail/<模块>/`；指针块与 PROTOCOL.md 留着（其它模块可能还用） |
| `GET /api/local/conversations?side=codex&dir=…` | 列该目录下 Codex 未归档线程（id、name、title、updated_at），最新在前，最多 20；只读 `state_*.sqlite`，读不到返回空数组 + `error` 字段 |
| `GET /api/local/conversations?side=claude&dir=…` | 列 `~/.claude/sessions/*.json` 里 `cwd` 匹配的运行中会话（sessionId、name、status）；仅供显示，不登记 |
| `POST /api/local/modules/{name}/attach` `{side:"codex", thread}` | 写 `.attach-codex`（同 §6.3）；`side:"claude"` → 400 |
| `POST /api/local/modules/{name}/redeliver` | 对最后一封应投给 codex 的信重跑 §7.3 |
| `GET /api/local/state` | 守卫是否在跑（进程即服务器，恒 true，返回版本、启动时间、`codex_path` 是否可执行）|

模块创建 `POST /api/local/modules` 增加可选 `codex_thread`（创建时顺手接入）。所有本地端点仍要求回环（agent token 也可，人视角无差别）。

## 9. 控制台（独立一套页面）

`internal/server/web/local/{index.html,app.js,style.css}`；`local_dir` 非空时 `/` 服务这一套，否则服务原联网页面（不动，含三语与登录）。本地页面**只有中文**，无登录页、无邀请、无管理员、无账号设置。页面加载不请求任何外网资源。

### 9.1 信息架构
- **顶栏**：`RELAIS · 本地`（等宽大写小标签）+ 守卫状态点 + 「新建模块」+ 「设置」。
- **左栏 模块列表**：每行 名称 / 状态标签 / `第 3/8 回合` / 最近活动时间。当前选中高亮。空列表时整页只有一张"新建第一个模块"卡。
- **主栏**（选中模块）：
  1. **现在**：一句话状态（"等 Codex 回信 · 第 4 回合 · 上封信来自 Claude，3 分钟前"）；下方两行接入状态：`CLAUDE  在等信 · 从 21:03 起` / `没在等 → 在 Claude 对话里说：接入 relais 模块 X`（带复制按钮）；`CODEX  已接入「查找 iOS 方向调整记录」` / `未接入 → 在 Codex 对话里说：… 或 [从列表选]`。有人该回信时显示一条渐变进度条（唯一用渐变的地方）。
  2. **该你做**（薄荷底，只在有事时出现）：确认开工（显示承接方与结论摘要）/ 回答问题（textarea + 发送，发出即 resume）/ 继续（回合到顶）/ 重新投递（投递失败，附错误）/ outbox 文件被拒（附原因与文件名）。
  3. **往来**：信件卡片按序号，卡片头 `007 · CODEX · 21:10` + 类型标签（收敛提议 / 附和 / 需要你 / 结论 / 开工），摘要一行，展开显示正文（markdown）。底部写信框：textarea + 「先回」下拉（Claude / Codex）+ 发出。
  4. **本模块**：模式（监督 / 甩手）、回合上限、改名、关闭 / 重开、删除（确认框，勾选"同时删除信件文件"）。
- **新建模块**抽屉：名字、目录（repos 下拉 + 手填，沿用 M8）、可选"现在接入 Codex 对话"（列表来自 `/api/local/conversations`）。创建后主栏显示两侧的接入提示。
- **设置**抽屉：`codex` 命令路径（自动侦测顺序：`PATH` → `/Applications/ChatGPT.app/Contents/Resources/codex` → `~/.codex/plugins/.plugin-appserver/codex`；「测试」按钮跑 `codex --version`）、默认模式、默认回合上限、每封信都通知（开关）。
- 实时：沿用现有 SSE 推送刷新往来；状态栏每 5 秒拉一次 `modules`。

### 9.2 视觉（照 Hou 贴的参考改成工具台）
- 令牌：画布 `#ffffff`，墨 `#010120`，细线 `#ebebeb`，次级文字 `#5c5c6b`，薄荷 `#c8f6f9`（只用于"该你做"卡底），渐变 `linear-gradient(90deg,#fc4c02,#ef2cc1,#bdbbff)`（只用于进行中指示条），圆角 4px，间距 4px 基数（4/8/12/16/24/32），无阴影。
- 字体：`Inter, -apple-system, "PingFang SC", sans-serif`，正文 400、标题 500、字距 -0.01em；标签与序号 `"JetBrains Mono", "SF Mono", Menlo, monospace` 大写 11px 字距 0.08em。不引入网络字体。
- 按钮：主按钮墨底白字胶囊；次按钮细线白底；危险动作红字细线。
- 状态标签配色只用墨/灰两档 + 薄荷底一档（"等你"）；不用红绿黄堆。
- 深色：`prefers-color-scheme: dark` 时画布 `#010120`、墨 `#ffffff`、细线 `#26264a`、薄荷保持。
- 断点：< 900px 时模块列表折成顶部横向滚动条。

## 10. 数据模型增量（幂等迁移）

```sql
CREATE TABLE IF NOT EXISTS local_modules (
  channel_id TEXT PRIMARY KEY,
  dir TEXT NOT NULL,
  archived_seq INTEGER NOT NULL DEFAULT 0,
  codex_thread_id TEXT NOT NULL DEFAULT '',
  codex_thread_name TEXT NOT NULL DEFAULT '',
  codex_attached_at TEXT NOT NULL DEFAULT '',
  codex_delivery_error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  closed_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS local_deliveries (
  message_id TEXT NOT NULL, side TEXT NOT NULL, status TEXT NOT NULL, error TEXT NOT NULL DEFAULT '', at TEXT NOT NULL,
  PRIMARY KEY (message_id, side)
);
```
- `.attach-codex` 是登记的来源（agent 侧只能写文件），守卫每轮把它同步进 `local_modules`（文件更新时间比 DB 新才同步）；控制台点选走 API 直接写文件再同步，路径统一。
- 两侧的 `projects.toml`、`sessions.toml`、`hooks/`、agent token 不再需要；`sides/` 目录升级时保留不读（§11）。
- settings 表 `local.claude_path` 删除；`local.codex_path`、`local.default_mode`、`local.default_cap`、`local.notify_every_letter` 保留/新增。

## 11. 砍掉、迁移、升级

**删除的代码**：`localhook.go`、`localprompt.go`、`session.go` 中的本地讨论脑续会话、`auto-reply.sh` 生成、`relais local-prompt` / `relais session` 子命令、bridge 里的本地分支（登记表重读、心跳、`RELAIS_CHANNEL`、hook 直接 exec 的分支保留给联网 hook 用）、`guide.LocalText`、`/api/local/heartbeat`、控制台的规矩编辑与「复制开工指令」、`relais/AGENT.md` 本地段落的生成。联网 bridge 与 hook 机制原样保留。

**安装脚本 / `relais local bootstrap`**：
- 只装一个 launchd：`com.relais.local.server`；发现 `com.relais.local.bridge.claude|codex` 则 `bootout` 并删 plist。
- 不再侦测 `claude` 路径；`codex` 路径按 §9.1 顺序侦测，找不到不弹文件框，写空并在控制台提示。
- 不再生成也不再显示密码；`human.txt` 若存在保留不读。弹窗只显示网址。
- 升级时对登记表里每个模块：写/更新 `PROTOCOL.md`（带标记才覆盖）、更新指针块为两行版（识别 `<!-- relais-local -->` 标记替换整块）、建 `relais/mail/<模块>/{outbox,drafts}`。旧 `inbox/`、`sent/`、`conclusions/` 不动。
- 现有 `channel_auto` 与消息记录保留；`local_modules` 由 `sides/claude/projects.toml` 一次性导入（模块名 → 目录），`archived_seq` 设为该频道当前最大 seq（旧信不重新归档）。

**`relais local init/status/close`**：保留为控制台 API 的薄壳；`init` 不再接 `--claude/--codex`。

## 12. 安全不变量
- 无 `local_dir` 的服务器：没有回环免钥匙、没有 `/api/local/*`（锚点：不带钥匙的回环请求 → 401；`/api/local/modules` → 404）。
- 有 `local_dir` 的服务器只监听回环（M8 ⑤ 原样）。
- 守卫只在登记表里的目录下读写，且只碰 `relais/mail/<模块>/`、`relais/PROTOCOL.md`、`CLAUDE.md`/`AGENTS.md` 的标记块；模块目录名经 `filepath.Clean` 且必须仍在项目 `relais/mail/` 下。
- `codex queue` 以 `exec.Command` 参数数组调用，通知文本不经 shell。
- 读 Codex / Claude 的数据库与会话文件一律只读；失败降级为"列表为空"，不影响投递。
- 删除模块默认不删文件。

## 13. 测试与验收

**单测**（Go，`go test ./...`）：
- `post`：四种标志 → outbox 头正确；`--ack` 找不到对方 resolved → 错误；侧自动判断表（环境变量矩阵）；找不到模块目录 → 错误列表。
- `wait`：临时目录里先放一封 → 立即返回；后放一封 → 返回并写 cursor；本侧的信不触发；kickoff 信附承接方提示；`--timeout`。
- `attach`：临时 sqlite 建 `threads` 表三行（不同 cwd、archived、时间）→ 选中正确一行；`/private` 前缀等价；表缺列 → 明确错误。
- 守卫：outbox 入库分配 seq、删除源文件、`*.rejected` 分支；归档文件信封逐字段；`conclusion-<seq>.md` 生成；投递用假 `codex` 脚本记录 argv（thread id、通知文本）并验证失败路径写 `codex_delivery_error`；`.attach-codex` 同步；同侧连发两封照收。
- 服务器：回环免钥匙 = hou、非回环 401、线上无捷径（锚点）；改名移动目录、目标存在 409；删除默认留文件；reopen；conversations 两侧列表（假 `HOME`）。
- 协议文本：`PROTOCOL.md` 含 §5 八段、不含"背景/观点/问题"等内容小标题（防回归）；指针块两行、幂等替换。
- 网页：`index.html` 不含 `login`、`invite`、`admin` 字样；不含 `http(s)://` 外链；关键 id 存在。
- 安装脚本 `bash -n`；bootstrap 幂等且卸旧 bridge plist（假 `launchctl`）。

**真实冒烟**（本机，隔离目录：临时 `RELAIS_LOCAL_DIR`、`--no-service`、假 `codex` 脚本、临时项目目录，绝不留 launchd 与会话记录）：新建模块 → 控制台写第一封（`@codex 先回`）→ 假 codex 收到 queue 参数 → 手工以 codex 身份 `post` 一封 → `wait --as claude` 返回该信 → `post --resolved` / `post --ack` 握手 → `conclusion-*.md` 与 kickoff 信出现、假 codex 收到开工通知 → 改名后目录跟着改 → 删除模块文件仍在。

**真机验收**（Hou 的机器，正式安装后）：在他现有的一对对话上跑一个真实议题到握手，全程在两个对话里可见、可插话；控制台"现在"一栏与对话里的事实一致。

## 14. 版本与反转触发
- 版本 `0.7.0-m9`；`2026-09-26-relais-m8-grill-design.md` 标题改为 M10，D41 注明。
- 反转触发：① Claude Code 若把长时间后台命令强杀（wait 频繁被杀）→ 改为 `wait --timeout 20m` 循环 + 协议里加"被杀就再跑"一句，或换 Claude Code hook 通知；② Codex 升级导致 `codex queue` 或 `threads` 表变化 → attach 退回控制台点选 + `--thread`，投递退回门铃（Codex 侧也跑 `wait`）；③ 单人本机的免钥匙若出现第二个用户需求 → 恢复人钥匙。
