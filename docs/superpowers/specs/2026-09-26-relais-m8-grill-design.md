# Relais M9 候选设计（原 M8，2026-09-27 因控制台插队顺延）：拷问剧本（双 agent 开局互相提问）+ 密封轮

> 日期：2026-09-26 · 状态：**候选草案，排在 M8 控制台之后** · 基线：M7（本地模式）
> 决策：D41 · 术语：根目录 `CONTEXT.md`（拷问 / 密封轮 / 阶段 / 清单 / 裁定方 / 批判信 / 拷问产物）
> 来源：Hou 2026-09-26 提出——大模块开局时让双方各自提问、合并、各自作答、比对分歧、讨论裁定（Q26–Q32 全按推荐落定）

---

## 1. 动机
好问题比好答案值钱，尤其在大模块开局。但 M7 的频道严格轮流，后写的一方必然看到先写的一方，被锚定：重合率虚高，"对方没想到的重要问题"出不来。M8 给频道加一种**密封投递**，并在 hook 提示词里编一段固定剧本，让开局这一段按"独立→合并→独立→比对→讨论"走。

## 2. 剧本（六阶段）

| 阶段 `phase` | 谁写 | 投递 | 内容 |
|---|---|---|---|
| `questions` | 双方 | **密封** | 各自独立列出对这个议题必须先回答的问题（每题一句 + 为什么重要） |
| `merge` | 发起方 | 公开 | 合并两份：去重、归类，**不改问题本意、不删对方独有的题**；每题标 `decider: agents\|user` |
| `review` | 对方 | 公开 | 一轮复核：可补漏、可反对某条的删并或 decider 标法；之后清单**冻结** |
| `answers` | 双方 | **密封** | 对冻结清单逐题给自己的推荐答案 + 一句理由（user 题也答，作为给人的推荐） |
| `critique` | 双方 | 公开 | 看对方答案后写批判信：重合项一句话；分歧项逐条"我的 / 对方的 / 分歧在哪 / 我建议怎么裁" |
| `discuss` | 轮流 | 公开 | 进 M7 自由讨论回路，直到握手；user 题攒着 |

**回合计数**：前五阶段不吃回合上限（D41）；`discuss` 起按 M7 §7 计。

**人的位置（D41）**：`decider=user` 的题**不逐题打断**。握手时结论里把全部 user 题打包成一次 needs-human（每题附双方推荐与理由），人一次答完；agents 题讨论后仍定不了的，升级改标 user 并入同一包。人答完 → resume → 双方把人的答案填进产物 → 再握手一次（这次没有 user 题了）→ 开工。

## 3. 密封轮（服务器机制）

### 3.1 消息字段
信封加 `phase`（§2 六值之一，缺省空 = 普通消息）与 `sealed: true|false`。

### 3.2 服务器行为（`handleSend`）
- `sealed=true` 的消息：入库但 `recipients.read_at` 不建、**不进 SSE、不进列表、不给 bridge 拉**，记 `sealed_pending(channel_id, phase, sender_id, message_id)`。
- 同频道同 `phase` 出现**另一侧**的密封消息 → 两条同时"揭开"：删 pending 行、建 recipients、按 seq 顺序推 SSE、bridge 下次拉到两条。揭开是同事务。
- 同侧同 phase 重复密封投递 → 幂等：返回既有那条（用 M7 的 `sent_keys` 语义，key = `sealed:<phase>:<sender>`）。
- 密封期间 `auto/turn`：**两侧都放行一次**（各自写自己那封），不按轮流；揭开后恢复轮流，下一个发言者 = 剧本指定（`merge` 是发起方，`critique` 后由发起方先说）。
- 密封等待超时：不做超时机制。状态条显示"密封中：已收 claude，等 codex"，人看得见卡在哪（D41）。

### 3.3 数据
```sql
ALTER TABLE messages ADD COLUMN phase TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN sealed INTEGER NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS sealed_pending (channel_id, phase, sender_id, message_id, PRIMARY KEY(channel_id, phase, sender_id));
ALTER TABLE channel_auto ADD COLUMN grill_phase TEXT NOT NULL DEFAULT '';   -- 当前阶段，'' = 非拷问
```

## 4. 开题与驱动
- 工作脑开题：`relais send --grill <信>`（frontmatter `kind: grill-open`）。服务器置 `grill_phase=questions`，**两侧** bridge 都拉到开题信（发起方讨论脑也要独立提问，所以这封对两侧都是新信），hook 走密封分支。
- hook 五分支之上加**剧本分支**：`auto-turn` 返回里带 `grill_phase`，hook 把对应阶段的提示词段落拼进 PROMPT，并在 `questions`/`answers` 阶段用 `relais send --sealed --phase <p>`。
- 阶段推进由服务器按表驱动：`questions` 揭开 → `merge`（发起方 turn）→ `review`（对方 turn）→ `answers`（密封）→ 揭开 → `critique`（双方各一封，公开，发起方先）→ `discuss`（M7 回路）。服务器只认顺序，不解析内容；agent 写错阶段的消息按普通消息入流、`grill_phase` 不推进（状态条可见）。

## 5. 拷问产物
握手成立时，结论（第二条 RESOLVED）正文必须是 `grill-<频道>.md` 格式：

```markdown
# <议题>

| # | 问题 | Claude 答 | Codex 答 | 定案与理由 | 裁定方 |
|---|---|---|---|---|---|
| 1 | … | … | … | … | agents |
| 7 | … | … | … | **待人定**（双方推荐：…） | user |

## 承接方
codex —— 理由
```
kickoff 时按 M7 §6.3 落到 `relais/conclusions/grill-<频道>-<id>.md`；网页结论卡片渲染为表格（取代普通结论卡片）。工作脑开工读这份表。

## 6. 网页
状态条新增拷问进度条：六格，当前阶段高亮；密封中显示"已收 X，等 Y"。其余复用 M7。

## 7. 测试与验收（拟）
- 锚点：密封配对——claude 密封 `questions` 后 codex 拉不到、列表不含、SSE 不推；codex 同 phase 密封到达后两条同时可拉且顺序按 seq；同侧重复密封幂等；密封期 turn 两侧各放行一次、揭开后恢复轮流；阶段表驱动顺序正确、错阶段消息不推进；剧本阶段不增 round；密封消息永远不落到对方 `inbox/` 直到揭开（串台锚点延伸）。
- 单测：hook 剧本分支拼接、`--sealed --phase` 上送；产物表格解析（至少能取出 user 题列表打包 needs-human）。
- 真实冒烟：一个测试议题跑完六阶段到握手，产物表格出现在 `relais/conclusions/`，含至少一题 user 打包一次问人。

## 8. 明确不做
超时自动揭开；三方以上密封；服务器解析问题内容做自动去重（合并是 agent 的活）；把拷问用于联网模式（先本地）。

## 9. 反转触发（D41）
密封轮实现若明显复杂于 §3 的估计 → 退回"发起方先提问、对方补充"，产物表加"锚定风险"一栏说明。
