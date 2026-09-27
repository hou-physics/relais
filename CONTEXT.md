# Relais

Relais 是让两个 AI agent 隔着一个人工闸门把分歧对齐、得出结论的消息中转。全项目术语唯一事实源在本文件（原 spec §2 术语表已并入，spec 只引用不重定义）。

## 语言

### 基础（M1 起）

**频道 (channel)**：
一组成员 + 一条消息流。服务器上唯一的组织单位。
_Avoid_：房间、话题、thread

**项目绑定**：
本地项目文件夹通过 `relais init` 与某频道关联的客户端行为。

**信封 (envelope)**：
消息元数据：id、频道、发件人、收件人、时间、回复指向、序号。
_Avoid_：header、meta

**摘要 (summary)**：
给人看的一两句话，网页时间线常显。

**正文 (body)**：
给对方 agent 读的完整 Markdown。

**双钥匙**：
同一个人的两种凭证：网页登录（人的钥匙）与 agent token（agent 的钥匙）。

**串台**：
一个 agent 读到了不是发给它主人的消息正文。本系统在服务器层面杜绝。

**needs-human**：
agent 声明"这件事要人定"，循环立即暂停等人。

**引导 (guidance)**：
人私下给自己 agent 的一句话，不进频道，对方看不见。

### 本地模式（M7 起）

**本地模式 (local mode)**：
Relais 跑在一台机器上、服务一个人两个 agent 的运行形态。服务器在 localhost，两个身份都属于同一个人。
_Avoid_：单机版、离线版

**侧 (side)**：
本地模式下的两个身份之一：`claude` 或 `codex`。每个侧有自己的 agent token 和配置目录。
_Avoid_：账号、用户（本地模式里人只有一个）

**模块 (module)**：
人把项目切出的一块工作。本地模式下一个模块对应一个频道，频道名即模块名。

**子频道 (sub-channel)**：
同一模块里要并行讨论第二个议题时另开的频道，命名 `<模块>-<议题>`。有自己的讨论脑。

**讨论脑 (discussion brain)**：
（M7–M8）绑定在（频道，侧）上的持久无头 agent 会话。**M9 起废止**：两侧都是人开着的对话，见「接入现有对话」。
_Avoid_：hook agent、后台会话

**工作脑 (work brain)**：
（M7–M8）人手头开着的交互式 agent 会话，负责实现。M9 起讨论与实现都在同一个对话里，这个词只在历史文档里出现。
_Avoid_：主会话、前台会话

**议题 (topic)**：
一次"从开题到握手"的讨论生命周期。一个频道同一时间只有一个议题在途。

**开题 (open)**：
工作脑把背景压缩成第一封信投进频道的动作。

**序号 (seq)**：
服务器给每频道消息发的单调递增整数。

**回合 (round)**：
一来一回算一个回合，`round = ceil(seq / 2)`。回合上限只计自由讨论阶段。
_Avoid_：轮次、turn（turn 是服务器的发言权授予，一条消息一个 turn）

**握手 (handshake)**：
两条来自不同侧、连续的 `RESOLVED` 消息，第二条指向第一条且两条的承接方一致。握手成立即议题收敛。
_Avoid_：达成一致、共识（这些是口语，不是状态）

**承接方 (owner)**：
握手结论里指定的开工者：`claude`、`codex` 或 `user`。双方各提名，一致即定，不一致升级 needs-human。
_Avoid_：负责人（负责人是人，承接方可以是 agent）

**结论 (conclusion)**：
握手的第二条消息正文。频道当前只有一份待处理结论。

**监督模式 (supervised)**：
握手后必须人确认才开工。

**甩手模式 (autopilot)**：
握手后不经人确认直接开工。

**开工 (kickoff)**：
把结论投递到承接方的项目目录并通知。投递即开工，实现由工作脑做。
_Avoid_：执行、部署

**规矩 (rules)**：
项目目录里 `relais/RULES.md`，两侧讨论脑都读的项目专属铁律。

### 控制台（M8 起）

**控制台 (console)**：
本地模式下扩展后的网页：时间线之外还管模块、规矩、设置与开题。是人的唯一日常入口。
_Avoid_：管理页、后台

**安装文件 (installer)**：
仓库根目录的双击 `.command` 脚本，负责装二进制、常驻与首次配置。唯一需要碰"文件系统"的动作。

**心跳 (heartbeat)**：
bridge 每轮向服务器报一次活；控制台据此显示某一侧是否在跑。

**先回 (first responder)**：
开题时指定先答的一侧，写在信正文首行 `@claude` / `@codex`；接话规则据此路由。

**开工指令 (kickoff instruction)**：
结论卡片可复制的一句话，贴给承接方工作脑即开工。

### 接入现有对话（M9 起）

**守卫 (daemon)**：
本地模式唯一的常驻进程，即 `relais serve`。扫 outbox 入库、按序号归档、把信投给另一侧。
_Avoid_：bridge（联网模式的词）、后台服务

**门铃 (doorbell)**：
在对话里运行的 `relais wait`：信到了就退出，让对话醒来。只 Claude 侧需要。
_Avoid_：轮询、监听

**接入 (attach)**：
把一个已经开着的对话绑到模块上。Claude 侧 = 运行 wait；Codex 侧 = `relais attach` 登记线程 id。
_Avoid_：绑定、注册

**投递 (delivery)**：
守卫把一封归档信通知到某一侧：Codex 侧 `codex queue`，Claude 侧靠门铃。
_Avoid_：推送、发送（发送是 agent 的动作）

**信箱 (mailbox)**：
项目目录里的 `relais/mail/<模块>/`：归档信、结论、outbox、drafts、接入与等待的标记文件。
_Avoid_：inbox（M7 旧目录）

**待发 (outbox)**：
`relais post` 放信的地方，守卫取走即删。

**协议 (protocol)**：
`relais/PROTOCOL.md`，两侧共读，只规定信怎么走：信封、去哪读、怎么发、三种标记、接入步骤。不规定正文。
_Avoid_：模板、写信规范

**标记 (mark)**：
`relais post` 的三种标志：`--resolved --owner` 提议收敛、`--ack` 附和、`--needs-human` 要人定夺。

**本地控制台 (local console)**：
本地模式独立的一套网页，无登录，只有中文；与联网网页共用底层 API。M8 的「控制台」词条自 M9 起指它。

### 拷问（M10 起，原 M9，因 M9 接入现有对话顺延）

**拷问 (grill)**：
议题开局的固定剧本：双方独立提问 → 合并清单 → 独立作答 → 公开批判 → 自由讨论 → 握手。
_Avoid_：头脑风暴、问答

**密封轮 (sealed round)**：
服务器扣住两侧各一封信、到齐同时揭开的投递方式。用于提问和作答两步。
_Avoid_：盲投、同步轮

**阶段 (phase)**：
拷问剧本里的一步：`questions`、`merge`、`review`、`answers`、`critique`、`discuss`。

**清单 (question list)**：
合并并冻结后的问题列表。每题带裁定方。

**裁定方 (decider)**：
清单里每题谁来定：`agents`（双方讨论收敛）或 `user`（人定，攒到最后一次问）。

**批判信 (critique)**：
作答揭开后各自写的比对：重合项一句话，分歧项逐条列双方立场与建议裁法。

**拷问产物 (grill artifact)**：
`grill-<频道>.md`，每题四栏：问题、claude 答、codex 答、定案与理由。取代普通结论卡片。
