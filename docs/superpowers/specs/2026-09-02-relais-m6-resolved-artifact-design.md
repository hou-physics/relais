# Relais M6 候选设计：结论终态（RESOLVED）+ 结论产物 + 幂等发送

> 日期：2026-09-02 · 状态：**M6 候选（草案，未定稿）** · 基线：M5 已上线（v0.4.0-m5）
> 来源：借鉴 A2A 协议的几个设计要点，只取思想、不接其线格式（见 §8）
> 相关决策：拟记 D34（M6 候选）

---

## 1. 动机

Relais 的核心场景是"两个 agent 隔着人工闸门把分歧对齐、得出结论"。但 M5 的自主循环**只会因为撞回合上限（cap）或 needs-human 而停**——缺了整个产品最有价值的那个信号：**"他俩谈拢了"**。

借鉴 A2A 的三个要点（A2A 有明确的任务终态、把"产出物 Artifact"和"来回消息 Message"分开、用 messageId 去重）：

1. **RESOLVED 终态**：agent 能显式声明"已达成结论"，循环语义化收敛（而不是钝钝地等 cap）。
2. **结论产物（conclusion artifact）**：把"结论"从"6 条来回"里拎出来单独呈现，人只看这一块。
3. **幂等发送**：给自动发送加去重键，防 hook 重试 / bridge 崩溃重跑导致重复自动回复。

**贯穿原则（我们区别于 A2A 之处）**：agent 只**提议**结论，**人来批准**。A2A 的任务是 server agent 自己判定 completed；我们保留人在环中——agent 说"我觉得我们一致了：X"，循环停下，两边的人**确认**或**打回继续**。

## 2. RESOLVED 终态

### 2.1 信号（复用现有 hook 分支）
M5 的 Windows/Unix hook 现在是三分支（`NEEDS_HUMAN:` → needs-human / 首行 `---` → send / 否则跳过）。**扩成四分支**，新增最高优先级：

- agent 输出首行 `RESOLVED: <一句结论摘要>`，其后正文 = 完整结论（即 §3 的 artifact）。
- hook 侦测到 → 调新的 agent 侧端点 `POST /api/channels/{name}/auto/resolve`（agent token 可调，与 needs-human 同类），body `{summary, conclusion}`。

优先级：`RESOLVED:` > `NEEDS_HUMAN:` > `---`(send) > 跳过。（RESOLVED 与 NEEDS_HUMAN 都是"停循环"的终止/暂停信号，谁在首行谁生效。）

### 2.2 服务器行为（人在环 = 提议待批准）
`auto/resolve` 端点：
- 把结论作为一条**正常消息**发进频道消息流（两边都看得到），frontmatter 标 `kind: conclusion`，summary = 结论摘要。→ 复用现有消息存储，天然进 history，不新建 artifact 子系统。
- 在 `channel_auto` 记：`resolved=1`、`resolution_msg_id=<该消息 id>`（指针）。**同时 `paused=1`**（循环立即停，等人处理）。
- 这是**"已提议结论，待人确认"**状态，不是永久关闭。

### 2.3 store 增量（幂等迁移，同 avatar/is_admin 模式）
```sql
ALTER TABLE channel_auto ADD COLUMN resolved INTEGER NOT NULL DEFAULT 0;
ALTER TABLE channel_auto ADD COLUMN resolution_msg_id TEXT NOT NULL DEFAULT '';
```
方法：`SetResolved(channelID, msgID)`（置 resolved=1, resolution_msg_id, paused=1）；`ClearResolved(channelID)`（resolved=0, resolution_msg_id='')；`GetAuto` 返回值加 `Resolved bool`、`ResolutionMsgID string`。

## 3. 结论产物（conclusion artifact）

不新建 Artifact 子系统——**产物 = 那条 `kind: conclusion` 消息的正文**，由指针 `resolution_msg_id` 定位。价值在**呈现**：人不用往回翻 6 条消息，直接看这一份干净结论。

- 网页时间线里 `kind: conclusion` 的消息**特殊渲染**（绿色卡片、置顶别针图标、"结论"标签）。
- 状态条也引用它（见 §5）。
- 一个频道当前只保留"最新一条待确认结论"（指针覆盖）；历史结论仍在消息流里可回溯。

## 4. 幂等发送（防重复自动回复）

**问题**：自主循环里 hook 重试 / bridge 崩溃重跑，可能把同一条自动回复发两次。turn 闸门只挡回合数，不挡重复。

**方案**（借 A2A 的 messageId 去重）：
- `POST /api/messages`（send）接受可选请求头 `Idempotency-Key`（客户端生成）。
- hook 对本轮**确定性**地生成 key（如 `sha256(channel + 源消息id + OUT文件内容)` 取前 16 位）——重试时同内容 → 同 key。
- 服务器新表 `sent_keys(channel_id, key, message_id, created_at, PRIMARY KEY(channel_id,key))`：同 key 已存在 → **不新建**，返回既有 message（200 + 原 id），幂等。
- 保留期：可只留最近 N 条或近 T 小时（循环是短时的，不必永久）。
- 手动网页发送不强制带 key（可选）；主要护住自动路径。

## 5. 最小网页（复用状态条 + 时间线）

- 状态条新增绿色终态：`✅ 已达成结论（待确认）：<summary>`，数据来自 `resolved` + 指针消息的 summary。
- 两个按钮（人的钥匙）：
  - **确认结论**：调 `POST /api/channels/{name}/auto/resolve/confirm`（人钥匙）→ 频道归档为"已解决"（`resolved` 保留、循环保持停、结论卡片置顶留存）。可选：写一条系统消息"X 确认了结论"。
  - **继续讨论**：调既有 `resume`（或 `.../resolve/reopen`）→ `ClearResolved` + resume（round_count 归零、paused=0），循环重开。
- 结论消息在时间线里绿色置顶卡片呈现（§3）。

## 6. 明确不做（对我们是过度设计，A2A 里但我们不取）

- 完整 8 态状态机、gRPC/protobuf/JSON-RPC 线格式；
- `/.well-known/agent-card.json` 开放发现、签名卡；
- OAuth2/OIDC/mTLS、Webhook 推送；
- 独立的 Artifact 存储子系统（我们用"标记消息 + 指针"就够）；
- 多传输绑定。

（needs_human 类型化、极简能力声明——即"对方是否会自动回"——列为 M6+ 更次要候选，本 spec 不含，待真觉得别扭再加。）

## 7. 测试与验收（拟）

- **锚点（永久 e2e）**：agent 发 `RESOLVED:` 消息 → `auto/resolve` 后 channel_auto `resolved=1 && paused=1`、循环 turn 被拒；`resolve/confirm`（人钥匙）后仍停、结论指针留存；`继续讨论`(reopen/resume) 后 `resolved=0`、turn 放行、round_count 归零。钥匙隔离：`auto/resolve` 走 agent token，`confirm`/`reopen` 只人钥匙（agent token → 403）。
- **幂等锚点**：同 `Idempotency-Key` 连发两次 send → 只落一条消息、两次返回同 id；不同 key → 两条。并发同 key（N goroutine）→ 恰好一条（仿 TestRequestTurnConcurrency）。
- hook 四分支单测（RESOLVED/NEEDS_HUMAN/`---`/跳过 优先级正确；Windows .cmd 仍守 §M5 的括号平衡 + 去引号 + 闸门锚点）。
- 全部 M1–M5 锚点不变绿；check.sh（含 node --check）绿。

## 8. 与 A2A 的关系（只取思想）

本设计**借 A2A 的三个概念**——任务终态（→ RESOLVED）、Artifact 与 Message 分离（→ 结论产物）、messageId 去重（→ 幂等键）——但**不接它的线格式、不实现 A2A server、不发 AgentCard**。Relais 的差异化仍是"人在环中的中心中转 + 傻瓜跨模型 UX"；这里只是把 A2A 里真正契合我们场景的几颗螺丝拧进来。若将来要接 A2A 生态，另走"可选 A2A 端点"那条路，与本 spec 正交。

## 9. 版本
落地时 `const version` → `0.5.0-m6`（暂定）。本文为候选草案，未排期。
