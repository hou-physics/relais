# Relais M7 Implementation Plan（本地单人模式）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Hou 本机的 Codex 与 Claude Code 按模块自动互相讨论到双边握手、定出承接方、按监督/甩手模式把结论投递到项目目录，人只在开题、needs-human、确认开工三处出面。

**Architecture:** store 给消息加 seq/kind/owner/ack_of 与幂等键，给 channel_auto 加 resolved/mode/kicked_off/closed，握手判定与开工都在 store 单事务内；server 在发送路径触发握手判定，加 kickoff/reopen/mode 三个人钥匙端点；cli 加 `relais local`（本机一条命令配好 serve + 两侧身份 + 频道 + hook + launchd）、每侧 `sessions.toml` 续会话、`relais local-prompt` 出讨论脑提示词、`relais conclusion`；bridge 把 `kind=kickoff` 消息落到 `relais/conclusions/` 且不触发 hook；网页加结论卡片、开工/继续按钮、模式开关、needs-human 回答进频道。

**Tech Stack:** Go ≥1.22，无新增依赖；shell hook；launchd；原生 JS 网页。

**Spec:** `docs/superpowers/specs/2026-09-26-relais-m7-local-mode-design.md`（必读）；术语 `CONTEXT.md`；决策 D35–D40。M1–M5 为基线。

## Global Constraints

- 无新增 Go 依赖（白名单：modernc.org/sqlite、golang.org/x/crypto、golang.org/x/term、gopkg.in/yaml.v3、BurntSushi/toml、oklog/ulid/v2）。
- **联网层一行不动（D35）**：邀请、管理员、install 脚本、部署脚本、Windows hook 不改。所有新增走新文件或新增分支。
- **M1–M5 全部锚点一行不改、不变绿即回退本任务**；每任务结束 `./scripts/check.sh` 必须绿。
- **安全不变量（§11）**：`kickoff`/`reopen`/`mode` 只人钥匙，agent token → 403；握手判定只在服务器；讨论脑只读（`--allowedTools "Read,Grep,Glob"` / codex `sandbox_mode="read-only"`）；本地服务器只监听 127.0.0.1。
- **执行期实现选择（记入 D42，Task 8）**：① 服务器内部 `cap`/`round_count` 仍按"条（turn）"计以保 M5 锚点不动，回合语义由 `relais local init` 设 `cap=16`（=8 回合）与网页/CLI 显示 `ceil(n/2)` 实现；② kickoff 消息收件人 = 频道全部成员（两侧都落 conclusions/，比只发承接方更简单且对 `owner=user` 天然正确）；③ RESOLVED 信号 = 首行 `RESOLVED: <一句>` + 其后 frontmatter（`owner`/`owner_reason`/`ack_of`），hook 把首行转为 `--kind resolved --summary`。
- 生产库已存在：所有迁移 `ALTER TABLE … ADD COLUMN` 须吞 "duplicate column"，`CREATE TABLE IF NOT EXISTS`。
- 前端动态数据一律 `textContent`，禁止拼 innerHTML；三语文案 zh/en/de 同步加。
- commit 前缀 feat:/fix:/test:/docs:/chore:；末尾 `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`。
- `const version` → `0.5.0-m7`（main.go 与 internal/server/static.go 同步，Task 8）。

## File Structure

```
internal/store/store.go         # 迁移 + Message 新字段 + SaveMessageOpts/幂等 + 握手/开工/模式/关闭（Task 1、2）
internal/store/store_test.go    # 对应单测
internal/api/types.go           # Message/SendRequest/AutoState 新字段 + ModeRequest（Task 3）
internal/server/api.go          # handleSend：Idempotency-Key、kind 校验、握手、甩手开工、清 kicked_off（Task 3）
internal/server/auto.go         # autoKickoff/autoReopen/autoMode + autoState 扩展（Task 3）
internal/server/server.go       # 三条新路由（Task 3）
internal/msg/format.go          # Envelope 新字段 + ExtractHeader（Task 4）
internal/cli/send.go            # --kind/--idempotency-key/header 解析/在途拒绝（Task 4）
internal/cli/client.go          # SendWithKey（Task 4）
internal/cli/inbox.go           # pullOne：kickoff → conclusions/（Task 4）
internal/cli/bridge.go          # pollOnce：kickoff 不跑 hook（Task 4）
internal/cli/conclusion.go      # relais conclusion（Task 4）
internal/cli/session.go         # sessions.toml + relais session get/set/clear（Task 5）
internal/cli/localprompt.go     # relais local-prompt（Task 5）
internal/cli/localhook.go       # writeLocalHook：claude/codex 续会话五分支 hook（Task 5）
internal/cli/local.go           # relais local init/status/close（Task 6）
internal/cli/service.go         # +installPlist 通用化（Task 6）
internal/cli/config.go          # saveGlobalTo/registerProjectIn（Task 6）
internal/cli/initcmd.go         # 抽 initProject（Task 6）
internal/server/web/{index.html,app.js,style.css}  # 结论卡片/按钮/模式/回答框（Task 7）
e2e/e2e_test.go                 # M7 锚点（Task 8）
main.go                         # 新子命令分发 + version（Task 5、6、8）
docs/decisions.md               # D42 执行期偏离（Task 8）
```

---

### Task 1: store —— 消息新字段、seq、幂等发送

**Files:**
- Modify: `internal/store/store.go`（schema 常量、`Open` 迁移、`Message` 结构、`SaveMessage`、`envelopeQuery`、`ListEnvelopes`、`GetMessage`）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  ```go
  type SaveOpts struct{ Kind, Owner, OwnerReason, AckOf, IdemKey string }
  func (s *Store) SaveMessageOpts(channelID, senderID int64, toIDs []int64, summary, body, inReplyTo string, o SaveOpts) (*Message, error)
  // Message 新增字段：Seq int; Kind, Owner, OwnerReason, AckOf string
  func Round(seq int) int // ceil(seq/2)，seq<=0 → 0
  ```
- `SaveMessage` 旧签名保留，内部调 `SaveMessageOpts(..., SaveOpts{})`。

- [ ] **Step 1: 写失败的测试**

在 `internal/store/store_test.go` 末尾追加（文件已有 `testStore(t)` helper）：

```go
func TestSeqAndKindFields(t *testing.T) {
	st := testStore(t)
	u, _ := st.CreateUser("a", "a", "pw")
	v, _ := st.CreateUser("b", "b", "pw")
	ch, _ := st.CreateChannel("c")
	st.AddMember(ch.ID, u.ID)
	st.AddMember(ch.ID, v.ID)
	m1, err := st.SaveMessageOpts(ch.ID, u.ID, []int64{v.ID}, "s1", "b1", "", SaveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := st.SaveMessageOpts(ch.ID, v.ID, []int64{u.ID}, "s2", "b2", "", SaveOpts{Kind: "resolved", Owner: "codex", OwnerReason: "因为", AckOf: m1.ID})
	if m1.Seq != 1 || m2.Seq != 2 {
		t.Fatalf("seq 应为 1,2: %d %d", m1.Seq, m2.Seq)
	}
	if m2.Kind != "resolved" || m2.Owner != "codex" || m2.OwnerReason != "因为" || m2.AckOf != m1.ID {
		t.Fatalf("kind 字段未贯通: %+v", m2)
	}
	list, _ := st.ListEnvelopes(ch.ID, u.ID, false, false)
	if len(list) != 2 || list[1].Seq != 2 || list[1].Kind != "resolved" {
		t.Fatalf("列表应带 seq/kind: %+v", list)
	}
	if Round(1) != 1 || Round(2) != 1 || Round(3) != 2 || Round(0) != 0 {
		t.Fatal("Round 计算错")
	}
	// 另一频道 seq 独立
	ch2, _ := st.CreateChannel("c2")
	st.AddMember(ch2.ID, u.ID)
	m3, _ := st.SaveMessageOpts(ch2.ID, u.ID, nil, "x", "y", "", SaveOpts{})
	if m3.Seq != 1 {
		t.Fatalf("新频道 seq 应从 1 起: %d", m3.Seq)
	}
}

func TestIdempotentSend(t *testing.T) {
	st := testStore(t)
	u, _ := st.CreateUser("a", "a", "pw")
	ch, _ := st.CreateChannel("c")
	st.AddMember(ch.ID, u.ID)
	m1, err := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "k1"})
	if err != nil || m2.ID != m1.ID {
		t.Fatalf("同 key 应返回同一条: %v %v", err, m2)
	}
	m3, _ := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "k2"})
	if m3.ID == m1.ID {
		t.Fatal("不同 key 应新建")
	}
	// 并发同 key 恰好一条
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "race"})
			if err == nil {
				ids <- m.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("并发同 key 应恰好一条，得到 %d", len(seen))
	}
}
```
（在 import 里加 `"sync"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run 'TestSeqAndKindFields|TestIdempotentSend' -v`
Expected: 编译错误 `undefined: SaveOpts`。

- [ ] **Step 3: 实现**

`internal/store/store.go`：

(a) schema 常量末尾（`guidance` 表之后）追加：
```sql
CREATE TABLE IF NOT EXISTS sent_keys (
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  key TEXT NOT NULL,
  message_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (channel_id, key)
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

(b) `Open` 里在 `is_admin` 迁移之后加一个循环（把已有两条也可以并进去，但不要改它们的行为）：
```go
	for _, ddl := range []string{
		`ALTER TABLE messages ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN owner TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN owner_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN ack_of TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE channel_auto ADD COLUMN resolved INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE channel_auto ADD COLUMN resolution_msg_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE channel_auto ADD COLUMN mode TEXT NOT NULL DEFAULT 'supervised'`,
		`ALTER TABLE channel_auto ADD COLUMN kicked_off INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE channel_auto ADD COLUMN closed INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(ddl); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
```

(c) `Message` 结构加字段：
```go
	Seq         int
	Kind        string // "" | resolved | conclusion | kickoff
	Owner       string // claude | codex | user（kind 非空时）
	OwnerReason string
	AckOf       string
```
并加：
```go
// Round 把频道序号换算成回合（一来一回 = 1）。
func Round(seq int) int {
	if seq <= 0 {
		return 0
	}
	return (seq + 1) / 2
}

type SaveOpts struct {
	Kind, Owner, OwnerReason, AckOf string
	IdemKey                         string // 非空则幂等：同频道同 key 只落一条
}
```

(d) 把 `SaveMessage` 改成薄壳，新写 `SaveMessageOpts`：
```go
func (s *Store) SaveMessage(channelID, senderID int64, toIDs []int64, summary, body, inReplyTo string) (*Message, error) {
	return s.SaveMessageOpts(channelID, senderID, toIDs, summary, body, inReplyTo, SaveOpts{})
}

func (s *Store) SaveMessageOpts(channelID, senderID int64, toIDs []int64, summary, body, inReplyTo string, o SaveOpts) (*Message, error) {
	id := ulid.Make().String()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if o.IdemKey != "" {
		var existing string
		err := tx.QueryRow(`SELECT message_id FROM sent_keys WHERE channel_id=? AND key=?`, channelID, o.IdemKey).Scan(&existing)
		if err == nil {
			tx.Rollback()
			return s.GetMessage(existing, senderID, true)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO sent_keys (channel_id, key, message_id, created_at) VALUES (?,?,?,?)`,
			channelID, o.IdemKey, id, now()); err != nil {
			// 并发下另一事务先插入了同 key：让它赢
			tx.Rollback()
			var winner string
			if e2 := s.db.QueryRow(`SELECT message_id FROM sent_keys WHERE channel_id=? AND key=?`, channelID, o.IdemKey).Scan(&winner); e2 == nil {
				return s.GetMessage(winner, senderID, true)
			}
			return nil, err
		}
	}
	createdAt := now()
	var replyVal any
	if inReplyTo != "" {
		replyVal = inReplyTo
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE channel_id=?`, channelID).Scan(&seq); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO messages (id, channel_id, sender_id, summary, body_md, in_reply_to, created_at, seq, kind, owner, owner_reason, ack_of)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, id, channelID, senderID, summary, body, replyVal, createdAt, seq, o.Kind, o.Owner, o.OwnerReason, o.AckOf); err != nil {
		return nil, err
	}
	for _, uid := range toIDs {
		if _, err := tx.Exec(`INSERT INTO recipients (message_id, user_id) VALUES (?,?)`, id, uid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMessage(id, senderID, true)
}
```
注意：SQLite 写事务串行化（busy_timeout 5s），`MAX(seq)+1` 在同一写事务内是安全的。

(e) `envelopeQuery` 的 SELECT 列在 `m.created_at,` 之后加 `m.seq, m.kind, m.owner, m.owner_reason, m.ack_of,`（保持 EXISTS 子查询在最后），`ListEnvelopes` 的 `Scan` 对应加 `&m.Seq, &m.Kind, &m.Owner, &m.OwnerReason, &m.AckOf`（放在 `&created` 之后、`&unread` 之前）。`GetMessage` 的 SELECT 同样加这五列（在 `m.created_at` 之后），Scan 对应加。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store/ -v -race`
Expected: 全部 PASS（含旧测试）。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): 消息 seq/kind/owner/ack_of 字段 + 幂等发送 sent_keys + settings 表（M7 Task 1）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: store —— 握手判定、开工、重开、模式、关闭、回合上限转人

**Files:**
- Modify: `internal/store/store.go`（`AutoState`、`GetAuto`、`RequestTurn`、新方法）
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  ```go
  // AutoState 新增：Mode string; Resolved bool; ResolutionMsgID string; KickedOff, Closed, InFlight bool
  type HandshakeResult int
  const ( HandshakeNone HandshakeResult = iota; HandshakeDone; HandshakeOwnerConflict )
  func (s *Store) EvaluateHandshake(channelID int64, m2ID string) (HandshakeResult, error)
  func (s *Store) Kickoff(channelID, actorID int64) (*Message, error)   // 返回 kind=kickoff 的新消息
  func (s *Store) Reopen(channelID int64) error
  func (s *Store) SetMode(channelID int64, mode string) error            // supervised|autopilot
  func (s *Store) CloseChannel(channelID int64) error
  func (s *Store) ClearKickedOff(channelID int64) error
  func (s *Store) SetSetting(key, value string) error
  func (s *Store) GetSetting(key string) (string, error)                 // 无则 ""
  ```
- `RequestTurn` 新增拒绝原因："频道已关闭"、"已握手，等人处理"；到 cap 时同时 `SetNeedsHuman(上限提示)`。

- [ ] **Step 1: 写失败的测试**

```go
func seedDuo(t *testing.T) (*Store, *Channel, *User, *User) {
	t.Helper()
	st := testStore(t)
	a, _ := st.CreateUser("claude", "Claude 侧", "pw")
	b, _ := st.CreateUser("codex", "Codex 侧", "pw")
	ch, _ := st.CreateChannel("m1")
	st.AddMember(ch.ID, a.ID)
	st.AddMember(ch.ID, b.ID)
	if err := st.SetAutoEnabled(ch.ID, true, 16); err != nil {
		t.Fatal(err)
	}
	return st, ch, a, b
}

func TestHandshake(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	m1, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "结论", "X", "", SaveOpts{Kind: "resolved", Owner: "codex", OwnerReason: "r"})
	if r, _ := st.EvaluateHandshake(ch.ID, m1.ID); r != HandshakeNone {
		t.Fatalf("第一次提议不应握手: %v", r)
	}
	// 人的普通消息夹在中间不影响
	st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "补充", "y", "", SaveOpts{})
	m2, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "附和", "X'", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	r, err := st.EvaluateHandshake(ch.ID, m2.ID)
	if err != nil || r != HandshakeDone {
		t.Fatalf("应握手: %v %v", r, err)
	}
	a1, _ := st.GetAuto(ch.ID)
	if !a1.Resolved || a1.ResolutionMsgID != m2.ID || !a1.Paused {
		t.Fatalf("握手后状态错: %+v", a1)
	}
	got, _ := st.GetMessage(m2.ID, a.ID, false)
	if got.Kind != "conclusion" {
		t.Fatalf("第二条应改写为 conclusion: %q", got.Kind)
	}
	if ok, reason, _ := st.RequestTurn(ch.ID); ok || reason == "" {
		t.Fatal("握手后 turn 必须拒")
	}
}

func TestHandshakeRejects(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	m1, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	// owner 不同 → 冲突转人
	m2, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "claude", OwnerReason: "我熟", AckOf: m1.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m2.ID); r != HandshakeOwnerConflict {
		t.Fatalf("owner 不同应冲突: %v", r)
	}
	a1, _ := st.GetAuto(ch.ID)
	if a1.Resolved || !strings.Contains(a1.NeedsHumanQ, "承接方分歧") {
		t.Fatalf("冲突应转 needs-human: %+v", a1)
	}
	st.ResumeAuto(ch.ID)
	// 同侧自 ack → 不算
	m3, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m3.ID); r != HandshakeNone {
		t.Fatal("同侧不能自握手")
	}
	// 中间又出现新的 resolved（m3）→ 对旧 m1 的 ack 失效
	m4, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m4.ID); r != HandshakeNone {
		t.Fatal("ack 指向的不是最新提议，不应握手")
	}
	// 无 ack_of → 只是新提议
	m5, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	if r, _ := st.EvaluateHandshake(ch.ID, m5.ID); r != HandshakeNone {
		t.Fatal("无 ack_of 不握手")
	}
}

func TestKickoffReopenModeClose(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	m1, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "结论正文", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	m2, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "最终结论", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	st.EvaluateHandshake(ch.ID, m2.ID)
	if _, err := st.Kickoff(ch.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	k, err := st.Kickoff(ch.ID, a.ID)
	if err == nil {
		t.Fatalf("未 resolved 时 Kickoff 应报错: %+v", k)
	}
	a1, _ := st.GetAuto(ch.ID)
	if !a1.KickedOff || a1.Resolved || a1.Paused || a1.RoundCount != 0 || a1.ResolutionMsgID != m2.ID {
		t.Fatalf("kickoff 后状态错: %+v", a1)
	}
	list, _ := st.ListEnvelopes(ch.ID, b.ID, true, true)
	last := list[len(list)-1]
	if last.Kind != "kickoff" || last.Owner != "codex" || last.Seq != 0 {
		t.Fatalf("kickoff 消息应 kind=kickoff、带 owner、不占 seq: %+v", last)
	}
	full, _ := st.GetMessage(last.ID, b.ID, true)
	if full.Body != "最终结论" || len(full.To) != 2 {
		t.Fatalf("kickoff 正文应是结论且发全体: %+v", full)
	}
	if err := st.ClearKickedOff(ch.ID); err != nil {
		t.Fatal(err)
	}
	// reopen
	st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "x", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	m4, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "x", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: ""})
	_ = m4
	st.SetNeedsHuman(ch.ID, "q")
	if err := st.Reopen(ch.ID); err != nil {
		t.Fatal(err)
	}
	a2, _ := st.GetAuto(ch.ID)
	if a2.Resolved || a2.Paused || a2.NeedsHumanQ != "" || a2.RoundCount != 0 {
		t.Fatalf("reopen 后状态错: %+v", a2)
	}
	// mode
	if err := st.SetMode(ch.ID, "autopilot"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(ch.ID, "bogus"); err == nil {
		t.Fatal("非法 mode 应报错")
	}
	a3, _ := st.GetAuto(ch.ID)
	if a3.Mode != "autopilot" {
		t.Fatalf("mode 未生效: %+v", a3)
	}
	// close
	st.CloseChannel(ch.ID)
	if ok, reason, _ := st.RequestTurn(ch.ID); ok || !strings.Contains(reason, "关闭") {
		t.Fatal("关闭后 turn 必须拒")
	}
	// settings
	st.SetSetting("local.default_mode", "autopilot")
	if v, _ := st.GetSetting("local.default_mode"); v != "autopilot" {
		t.Fatal("settings 读写错")
	}
	if v, _ := st.GetSetting("nope"); v != "" {
		t.Fatal("缺省应空")
	}
}

func TestCapHitTurnsToHuman(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	st.SetAutoEnabled(ch.ID, true, 2)
	st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "claude 立场", "x", "", SaveOpts{})
	st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "codex 立场", "y", "", SaveOpts{})
	st.RequestTurn(ch.ID)
	st.RequestTurn(ch.ID)
	if ok, _, _ := st.RequestTurn(ch.ID); ok {
		t.Fatal("到 cap 应拒")
	}
	a1, _ := st.GetAuto(ch.ID)
	if !strings.Contains(a1.NeedsHumanQ, "回合上限") || !strings.Contains(a1.NeedsHumanQ, "claude 立场") || !strings.Contains(a1.NeedsHumanQ, "codex 立场") {
		t.Fatalf("到 cap 应转人并摆出双方立场: %q", a1.NeedsHumanQ)
	}
}

func TestInFlight(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	if s, _ := st.GetAuto(ch.ID); s.InFlight {
		t.Fatal("空频道不在途")
	}
	m, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "x", "", SaveOpts{})
	if s, _ := st.GetAuto(ch.ID); !s.InFlight {
		t.Fatal("未读时应在途")
	}
	st.MarkRead(m.ID, b.ID)
	if s, _ := st.GetAuto(ch.ID); s.InFlight {
		t.Fatal("已读后不在途")
	}
}
```
（import 加 `"strings"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run 'TestHandshake|TestKickoff|TestCapHit|TestInFlight' -v`
Expected: 编译错误 `undefined: HandshakeNone` 等。

- [ ] **Step 3: 实现**

`internal/store/store.go`：

(a) `AutoState` 加字段：
```go
	Mode            string // supervised | autopilot
	Resolved        bool
	ResolutionMsgID string
	KickedOff       bool
	Closed          bool
	InFlight        bool // 最新一条（按 seq）仍有收件人未读
```

(b) `GetAuto` 改为：
```go
func (s *Store) GetAuto(channelID int64) (AutoState, error) {
	a := AutoState{Enabled: false, Cap: 6, Mode: "supervised"}
	var en, paused, resolved, kicked, closed int
	err := s.db.QueryRow(`SELECT enabled, round_count, cap, paused, needs_human_q, mode, resolved, resolution_msg_id, kicked_off, closed
		FROM channel_auto WHERE channel_id=?`, channelID).
		Scan(&en, &a.RoundCount, &a.Cap, &paused, &a.NeedsHumanQ, &a.Mode, &resolved, &a.ResolutionMsgID, &kicked, &closed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	if err == nil {
		a.Enabled, a.Paused, a.Resolved, a.KickedOff, a.Closed = en == 1, paused == 1, resolved == 1, kicked == 1, closed == 1
	}
	var unread int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM recipients r WHERE r.read_at IS NULL AND r.message_id =
		(SELECT id FROM messages WHERE channel_id=? AND seq>0 ORDER BY seq DESC LIMIT 1)`, channelID).Scan(&unread); err != nil {
		return a, err
	}
	a.InFlight = unread > 0
	return a, nil
}
```

(c) `RequestTurn`：在 `SELECT` 里多取 `closed, resolved`，并在 `en != 1` 判断之前加：
```go
	if closed == 1 {
		return false, "频道已关闭", nil
	}
	if resolved == 1 {
		return false, "已握手，等人处理", nil
	}
```
把 `if round >= cap { return false, "已到检查点（等待人确认继续）", nil }` 改为：
```go
	if round >= cap {
		tx.Rollback()
		q := s.capHitQuestion(channelID, cap)
		if err := s.SetNeedsHuman(channelID, q); err != nil {
			return false, "", err
		}
		return false, "已到回合上限（等待人决定）", nil
	}
```
（竞态分支 `n == 0` 同样改成调 `capHitQuestion` + `SetNeedsHuman`，先 `tx.Rollback()`。）新增：
```go
// capHitQuestion 组装"回合上限已到"的 needs-human 文案，附最近两位发言者各自最后一句摘要。
func (s *Store) capHitQuestion(channelID int64, cap int) string {
	rows, err := s.db.Query(`SELECT u.username, m.summary FROM messages m JOIN users u ON u.id=m.sender_id
		WHERE m.channel_id=? AND m.seq>0
		  AND m.seq = (SELECT MAX(seq) FROM messages WHERE channel_id=m.channel_id AND sender_id=m.sender_id)
		ORDER BY m.seq DESC LIMIT 2`, channelID)
	positions := ""
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var who, sum string
			if rows.Scan(&who, &sum) == nil {
				positions += who + "：" + sum + " / "
			}
		}
	}
	return fmt.Sprintf("回合上限已到（%d 回合）。最后立场：%s再放几轮、你来裁、还是关掉？", Round(cap), positions)
}
```
（子查询按每发言者取其最新一条；两位发言者各一条。import 需要 `"fmt"`。）

(d) 握手：
```go
type HandshakeResult int

const (
	HandshakeNone          HandshakeResult = iota // 不构成握手（首次提议 / ack 无效）
	HandshakeDone                                 // 握手成立：resolved=1, paused=1, 第二条改 conclusion
	HandshakeOwnerConflict                        // 前三条件成立但 owner 不同 → needs-human
)

// EvaluateHandshake 在 m2（kind=resolved）入库后判定是否与它 ack_of 指向的提议构成握手（spec §6.2）。
func (s *Store) EvaluateHandshake(channelID int64, m2ID string) (HandshakeResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return HandshakeNone, err
	}
	defer tx.Rollback()
	var m2Sender int64
	var m2Kind, m2Owner, m2Reason, ackOf string
	var m2Seq int
	if err := tx.QueryRow(`SELECT sender_id, kind, owner, owner_reason, ack_of, seq FROM messages WHERE id=? AND channel_id=?`, m2ID, channelID).
		Scan(&m2Sender, &m2Kind, &m2Owner, &m2Reason, &ackOf, &m2Seq); err != nil {
		return HandshakeNone, err
	}
	if m2Kind != "resolved" || ackOf == "" {
		return HandshakeNone, nil
	}
	var m1Sender int64
	var m1Kind, m1Owner, m1Reason string
	var m1Seq int
	err = tx.QueryRow(`SELECT sender_id, kind, owner, owner_reason, seq FROM messages WHERE id=? AND channel_id=?`, ackOf, channelID).
		Scan(&m1Sender, &m1Kind, &m1Owner, &m1Reason, &m1Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return HandshakeNone, nil
	}
	if err != nil {
		return HandshakeNone, err
	}
	if m1Kind != "resolved" || m1Sender == m2Sender {
		return HandshakeNone, nil
	}
	var newer int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id=? AND kind='resolved' AND seq>? AND seq<?`, channelID, m1Seq, m2Seq).Scan(&newer); err != nil {
		return HandshakeNone, err
	}
	if newer > 0 {
		return HandshakeNone, nil
	}
	if m1Owner != m2Owner {
		n1, _ := s.usernameByIDTx(tx, m1Sender)
		n2, _ := s.usernameByIDTx(tx, m2Sender)
		q := fmt.Sprintf("承接方分歧：%s 提名 %s（%s），%s 提名 %s（%s），请定", n1, m1Owner, m1Reason, n2, m2Owner, m2Reason)
		if _, err := tx.Exec(`UPDATE channel_auto SET paused=1, needs_human_q=? WHERE channel_id=?`, q, channelID); err != nil {
			return HandshakeNone, err
		}
		return HandshakeOwnerConflict, tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE messages SET kind='conclusion' WHERE id=?`, m2ID); err != nil {
		return HandshakeNone, err
	}
	if _, err := tx.Exec(`UPDATE channel_auto SET resolved=1, resolution_msg_id=?, paused=1 WHERE channel_id=?`, m2ID, channelID); err != nil {
		return HandshakeNone, err
	}
	return HandshakeDone, tx.Commit()
}

func (s *Store) usernameByIDTx(tx *sql.Tx, id int64) (string, error) {
	var n string
	err := tx.QueryRow(`SELECT username FROM users WHERE id=?`, id).Scan(&n)
	return n, err
}
```

(e) 开工 / 重开 / 模式 / 关闭 / settings：
```go
var ErrNotResolved = errors.New("频道尚未握手，无结论可开工")

// Kickoff 把当前结论投递为一条 kind=kickoff 消息（发全体成员，不占 seq），并让频道回到空闲。
func (s *Store) Kickoff(channelID, actorID int64) (*Message, error) {
	a, err := s.GetAuto(channelID)
	if err != nil {
		return nil, err
	}
	if !a.Resolved || a.ResolutionMsgID == "" {
		return nil, ErrNotResolved
	}
	var body, owner string
	if err := s.db.QueryRow(`SELECT body_md, owner FROM messages WHERE id=?`, a.ResolutionMsgID).Scan(&body, &owner); err != nil {
		return nil, err
	}
	members, err := s.ListMembers(channelID)
	if err != nil {
		return nil, err
	}
	var to []int64
	for _, m := range members {
		to = append(to, m.ID)
	}
	id := ulid.Make().String()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO messages (id, channel_id, sender_id, summary, body_md, in_reply_to, created_at, seq, kind, owner, owner_reason, ack_of)
		VALUES (?,?,?,?,?,NULL,?,0,'kickoff',?,'',?)`, id, channelID, actorID, "开工 · 承接方 "+owner, body, now(), owner, a.ResolutionMsgID); err != nil {
		return nil, err
	}
	for _, uid := range to {
		if _, err := tx.Exec(`INSERT INTO recipients (message_id, user_id) VALUES (?,?)`, id, uid); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(`UPDATE channel_auto SET kicked_off=1, resolved=0, paused=0, round_count=0, needs_human_q='' WHERE channel_id=?`, channelID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMessage(id, actorID, false)
}

func (s *Store) Reopen(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET resolved=0, resolution_msg_id='', paused=0, round_count=0, needs_human_q='' WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) SetMode(channelID int64, mode string) error {
	if mode != "supervised" && mode != "autopilot" {
		return fmt.Errorf("mode 只能是 supervised 或 autopilot")
	}
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, mode) VALUES (?,?)
		ON CONFLICT(channel_id) DO UPDATE SET mode=excluded.mode`, channelID, mode)
	return err
}

func (s *Store) CloseChannel(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET closed=1, paused=1 WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) ClearKickedOff(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET kicked_off=0 WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
```
注意 `Kickoff` 里 `GetMessage(id, actorID, false)`：actor 是人钥匙路径时是成员，agent 路径（甩手模式由服务器代调）时 actor 是发第二条 RESOLVED 的 agent，也是成员，`agentKey=false` 走成员检查即可。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/store/ -v -race`
Expected: 全 PASS。特别确认 M5 的 `TestRequestTurn*` 仍绿（cap 语义未变）。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/store/
git commit -m "feat(store): 双边握手判定 + 开工/重开/模式/关闭 + 回合上限转人 + in_flight（M7 Task 2）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: server —— 发送路径握手 + kickoff/reopen/mode 端点 + 状态扩展

**Files:**
- Modify: `internal/api/types.go`（`Message`、`SendRequest`、`AutoState`、新增 `ModeRequest`）
- Modify: `internal/server/api.go`（`toAPI`、`handleSend`）
- Modify: `internal/server/auto.go`（`autoState`、新增 `autoKickoff`/`autoReopen`/`autoMode`）
- Modify: `internal/server/server.go`（三条路由）
- Test: `internal/server/auto_test.go`

**Interfaces:**
- Consumes：Task 1/2 的 `SaveMessageOpts`、`EvaluateHandshake`、`Kickoff`、`Reopen`、`SetMode`、`ClearKickedOff`、`store.Round`、`store.ErrNotResolved`。
- Produces（HTTP）：
  - `POST /api/channels/{name}/messages` 接受请求头 `Idempotency-Key`；body 新字段 `kind`（"" 或 "resolved"）、`owner`、`owner_reason`、`ack_of`；响应 `Message` 带 `seq`、`round`、`kind`、`owner`、`owner_reason`、`ack_of`。
  - `POST /api/channels/{name}/auto/kickoff`（人钥匙）→ 200 + kickoff 消息；未握手 → 409。
  - `POST /api/channels/{name}/auto/reopen`（人钥匙）→ 204。
  - `POST /api/channels/{name}/auto/mode` body `{"mode":"supervised|autopilot"}`（人钥匙）→ 204。
  - `GET /api/channels/{name}/auto` 增 `mode`、`resolved`、`resolution_msg_id`、`resolution_summary`、`owner`、`kicked_off`、`closed`、`in_flight`、`round`（=Round(round_count)）、`round_cap`（=Round(cap)）。

- [ ] **Step 1: 写失败的测试**

`internal/server/auto_test.go` 追加：

```go
func TestHandshakeHTTP(t *testing.T) {
	ts, st, users := newTestServer(t)
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto", api.AutoConfigRequest{Enabled: true, Cap: 16})
	// hou 的 agent 提议 RESOLVED owner=codex
	r1, m1 := agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"wu"}, Summary: "结论A", Body: "X", Kind: "resolved", Owner: "codex", OwnerReason: "熟"})
	if r1.StatusCode != 200 || m1.Kind != "resolved" || m1.Seq != 1 || m1.Round != 1 {
		t.Fatalf("提议应 200 且带 kind/seq/round: %d %+v", r1.StatusCode, m1)
	}
	var stt api.AutoState
	json.NewDecoder(humanDo(t, ts, cookie, "GET", "/api/channels/deutschapp/auto", nil).Body).Decode(&stt)
	if stt.Resolved {
		t.Fatal("单边不应 resolved")
	}
	// wu 的 agent 附和
	_, m2 := agentSend(t, ts, users["wu"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"hou"}, Summary: "同意", Body: "X'", Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	if m2.Kind != "conclusion" {
		t.Fatalf("握手后第二条应返回 kind=conclusion: %+v", m2)
	}
	json.NewDecoder(humanDo(t, ts, cookie, "GET", "/api/channels/deutschapp/auto", nil).Body).Decode(&stt)
	if !stt.Resolved || stt.ResolutionMsgID != m2.ID || stt.ResolutionSummary != "同意" || stt.Owner != "codex" || stt.Mode != "supervised" {
		t.Fatalf("握手状态错: %+v", stt)
	}
	// agent 打人钥匙端点 → 403
	for _, p := range []string{"/auto/kickoff", "/auto/reopen", "/auto/mode"} {
		if r := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/channels/deutschapp"+p, api.ModeRequest{Mode: "autopilot"}); r.StatusCode != 403 {
			t.Fatalf("agent %s 应 403, got %d", p, r.StatusCode)
		}
	}
	// 监督模式：人确认开工
	rk := humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto/kickoff", nil)
	if rk.StatusCode != 200 {
		t.Fatalf("kickoff 应 200, got %d", rk.StatusCode)
	}
	var k api.Message
	json.NewDecoder(rk.Body).Decode(&k)
	if k.Kind != "kickoff" || k.Owner != "codex" || k.Body != "X'" {
		t.Fatalf("kickoff 消息错: %+v", k)
	}
	json.NewDecoder(humanDo(t, ts, cookie, "GET", "/api/channels/deutschapp/auto", nil).Body).Decode(&stt)
	if !stt.KickedOff || stt.Resolved || stt.Paused {
		t.Fatalf("开工后状态错: %+v", stt)
	}
	if r := humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto/kickoff", nil); r.StatusCode != 409 {
		t.Fatalf("重复 kickoff 应 409, got %d", r.StatusCode)
	}
	// 下一条普通消息清 kicked_off
	agentSend(t, ts, users["hou"].AgentToken, "deutschapp", api.SendRequest{To: []string{"wu"}, Summary: "新议题", Body: "y"})
	json.NewDecoder(humanDo(t, ts, cookie, "GET", "/api/channels/deutschapp/auto", nil).Body).Decode(&stt)
	if stt.KickedOff || !stt.InFlight {
		t.Fatalf("新议题后应清 kicked_off 且在途: %+v", stt)
	}
	_ = st
}

func TestAutopilotKicksOffOnHandshake(t *testing.T) {
	ts, _, users := newTestServer(t)
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto", api.AutoConfigRequest{Enabled: true, Cap: 16})
	if r := humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto/mode", api.ModeRequest{Mode: "autopilot"}); r.StatusCode != 204 {
		t.Fatalf("mode 应 204, got %d", r.StatusCode)
	}
	_, m1 := agentSend(t, ts, users["hou"].AgentToken, "deutschapp", api.SendRequest{To: []string{"wu"}, Summary: "s", Body: "X", Kind: "resolved", Owner: "claude"})
	agentSend(t, ts, users["wu"].AgentToken, "deutschapp", api.SendRequest{To: []string{"hou"}, Summary: "s", Body: "X", Kind: "resolved", Owner: "claude", AckOf: m1.ID})
	var stt api.AutoState
	json.NewDecoder(humanDo(t, ts, cookie, "GET", "/api/channels/deutschapp/auto", nil).Body).Decode(&stt)
	if !stt.KickedOff || stt.Resolved {
		t.Fatalf("甩手模式握手即开工: %+v", stt)
	}
	// 时间线里有 kickoff 消息
	rs := agentDo(t, ts, users["wu"].AgentToken, "GET", "/api/channels/deutschapp/messages?unread=1", nil)
	var list []api.Message
	json.NewDecoder(rs.Body).Decode(&list)
	if len(list) == 0 || list[len(list)-1].Kind != "kickoff" {
		t.Fatalf("应有未读 kickoff: %+v", list)
	}
}

func TestSendKindValidationAndIdempotency(t *testing.T) {
	ts, _, users := newTestServer(t)
	if r, _ := agentSend(t, ts, users["hou"].AgentToken, "deutschapp", api.SendRequest{To: []string{"wu"}, Summary: "s", Body: "b", Kind: "kickoff"}); r.StatusCode != 400 {
		t.Fatalf("客户端不得指定 kickoff, got %d", r.StatusCode)
	}
	if r, _ := agentSend(t, ts, users["hou"].AgentToken, "deutschapp", api.SendRequest{To: []string{"wu"}, Summary: "s", Body: "b", Kind: "resolved"}); r.StatusCode != 400 {
		t.Fatalf("resolved 缺 owner 应 400, got %d", r.StatusCode)
	}
	if r, _ := agentSend(t, ts, users["hou"].AgentToken, "deutschapp", api.SendRequest{To: []string{"wu"}, Summary: "s", Body: "b", Kind: "resolved", Owner: "bob"}); r.StatusCode != 400 {
		t.Fatalf("owner 非法应 400, got %d", r.StatusCode)
	}
	// 幂等头
	body, _ := json.Marshal(api.SendRequest{To: []string{"wu"}, Summary: "s", Body: "b"})
	send := func() api.Message {
		req, _ := http.NewRequest("POST", ts.URL+"/api/channels/deutschapp/messages", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+users["hou"].AgentToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "k-1")
		resp, _ := http.DefaultClient.Do(req)
		var m api.Message
		json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	a, b := send(), send()
	if a.ID == "" || a.ID != b.ID {
		t.Fatalf("同 Idempotency-Key 应同 id: %q %q", a.ID, b.ID)
	}
}
```
（import 需要 `"bytes"`、`"net/http"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/ -run 'TestHandshakeHTTP|TestAutopilot|TestSendKind' -v`
Expected: 编译错误 `unknown field Kind`。

- [ ] **Step 3: 实现**

(a) `internal/api/types.go`：
```go
// Message 加：
	Seq         int    `json:"seq"`
	Round       int    `json:"round"`
	Kind        string `json:"kind,omitempty"`
	Owner       string `json:"owner,omitempty"`
	OwnerReason string `json:"owner_reason,omitempty"`
	AckOf       string `json:"ack_of,omitempty"`
// SendRequest 加：
	Kind        string `json:"kind,omitempty"`
	Owner       string `json:"owner,omitempty"`
	OwnerReason string `json:"owner_reason,omitempty"`
	AckOf       string `json:"ack_of,omitempty"`
// AutoState 加：
	Mode              string `json:"mode"`
	Resolved          bool   `json:"resolved"`
	ResolutionMsgID   string `json:"resolution_msg_id,omitempty"`
	ResolutionSummary string `json:"resolution_summary,omitempty"`
	Owner             string `json:"owner,omitempty"`
	KickedOff         bool   `json:"kicked_off"`
	Closed            bool   `json:"closed"`
	InFlight          bool   `json:"in_flight"`
	Round             int    `json:"round"`
	RoundCap          int    `json:"round_cap"`
// 新增：
type ModeRequest struct {
	Mode string `json:"mode"`
}
```

(b) `internal/server/api.go` `toAPI` 补：
```go
	out.Seq, out.Round = m.Seq, store.Round(m.Seq)
	out.Kind, out.Owner, out.OwnerReason, out.AckOf = m.Kind, m.Owner, m.OwnerReason, m.AckOf
```
`handleSend` 改为：
```go
func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	switch req.Kind {
	case "":
	case "resolved":
		if req.Owner != "claude" && req.Owner != "codex" && req.Owner != "user" {
			writeErr(w, http.StatusBadRequest, "kind=resolved 必须带 owner（claude|codex|user）")
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "kind 只能为空或 resolved（conclusion/kickoff 由服务器设定）")
		return
	}
	_, toIDs, ok := s.validateOutgoing(w, ch, &req)
	if !ok {
		return
	}
	m, err := s.st.SaveMessageOpts(ch.ID, p.user.ID, toIDs, req.Summary, req.Body, req.InReplyTo, store.SaveOpts{
		Kind: req.Kind, Owner: req.Owner, OwnerReason: req.OwnerReason, AckOf: req.AckOf,
		IdemKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if req.Kind == "" {
		_ = s.st.ClearKickedOff(ch.ID)
	}
	if req.Kind == "resolved" {
		res, err := s.st.EvaluateHandshake(ch.ID, m.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "服务器内部错误")
			return
		}
		if res == store.HandshakeDone {
			if st, err := s.st.GetAuto(ch.ID); err == nil && st.Mode == "autopilot" {
				if k, err := s.st.Kickoff(ch.ID, p.user.ID); err == nil {
					s.publish(ch.ID, toAPI(k, ch.Name, false))
				}
			}
			m, _ = s.st.GetMessage(m.ID, p.user.ID, true) // 取回 kind=conclusion
		}
	}
	s.publish(ch.ID, toAPI(m, ch.Name, false))
	writeJSON(w, http.StatusOK, toAPI(m, ch.Name, true))
}
```

(c) `internal/server/auto.go`：`autoState` 改为：
```go
func (s *Server) autoState(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	st, err := s.st.GetAuto(ch.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	out := api.AutoState{Enabled: st.Enabled, RoundCount: st.RoundCount, Cap: st.Cap, Paused: st.Paused, NeedsHumanQ: st.NeedsHumanQ,
		Mode: st.Mode, Resolved: st.Resolved, ResolutionMsgID: st.ResolutionMsgID, KickedOff: st.KickedOff, Closed: st.Closed,
		InFlight: st.InFlight, Round: store.Round(st.RoundCount), RoundCap: store.Round(st.Cap)}
	if st.ResolutionMsgID != "" {
		if m, err := s.st.GetMessage(st.ResolutionMsgID, p.user.ID, false); err == nil {
			out.ResolutionSummary, out.Owner = m.Summary, m.Owner
		}
	}
	writeJSON(w, http.StatusOK, out)
}
```
（import 加 `"errors"` 与 store 包。）新增：
```go
func (s *Server) autoKickoff(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "确认开工仅限人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	k, err := s.st.Kickoff(ch.ID, p.user.ID)
	if errors.Is(err, store.ErrNotResolved) {
		writeErr(w, http.StatusConflict, "频道尚未握手，没有可开工的结论")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	s.publish(ch.ID, toAPI(k, ch.Name, false))
	writeJSON(w, http.StatusOK, toAPI(k, ch.Name, true))
}

func (s *Server) autoReopen(w http.ResponseWriter, r *http.Request, p principal) {
	s.autoHumanMutate(w, r, p, func(chID int64) error { return s.st.Reopen(chID) })
}

func (s *Server) autoMode(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "切换模式仅限人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.ModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if err := s.st.SetMode(ch.ID, req.Mode); err != nil {
		writeErr(w, http.StatusBadRequest, "%s", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
```
(d) `server.go` 路由，在 `auto/needs-human` 行后加：
```go
	mux.HandleFunc("POST /api/channels/{name}/auto/kickoff", s.auth(s.autoKickoff))
	mux.HandleFunc("POST /api/channels/{name}/auto/reopen", s.auth(s.autoReopen))
	mux.HandleFunc("POST /api/channels/{name}/auto/mode", s.auth(s.autoMode))
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/server/ -v -race`
Expected: 全 PASS（含 M1–M5 旧测试）。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/api internal/server
git commit -m "feat(server): 发送路径握手判定 + kickoff/reopen/mode 端点 + Idempotency-Key + 状态扩展（M7 Task 3）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: msg + cli —— frontmatter 新字段、`relais send` 扩展、kickoff 落 conclusions/、`relais conclusion`

**Files:**
- Modify: `internal/msg/format.go`（`Envelope` 新字段、新增 `Header`/`ExtractHeader`）
- Modify: `internal/cli/send.go`（`--kind`、`--idempotency-key`、header 解析、在途拒绝、`sent/` 副本带新字段）
- Modify: `internal/cli/client.go`（`SendWithKey`）
- Modify: `internal/cli/inbox.go`（`pullOne` 分流 kickoff）
- Modify: `internal/cli/bridge.go`（`pollOnce` 对 kickoff 不跑 hook、通知文案；`runHook` 加 `RELAIS_CHANNEL` 环境变量）
- Modify: `internal/cli/config.go`（`findProject` 优先读 `RELAIS_CHANNEL`：一个项目目录可承载多个模块频道，D37）
- Modify: `internal/cli/auto.go`（`auto status` 显示回合）
- Create: `internal/cli/conclusion.go`
- Modify: `main.go`（`conclusion` 分发）
- Test: `internal/msg/format_test.go`、`internal/cli/send_test.go`、`internal/cli/bridge_test.go`

**Interfaces:**
- Produces：
  ```go
  // msg
  type Header struct{ Summary, Kind, Owner, OwnerReason, AckOf string }
  func ExtractHeader(data []byte) (Header, string, bool)   // 宽松：无 frontmatter → ok=false, body=原文
  // Envelope 新增：Seq int `yaml:"seq,omitempty"`; Round int `yaml:"round,omitempty"`; Kind, Owner, OwnerReason, AckOf string（均 omitempty）
  // cli
  func (c *Client) SendWithKey(channel string, req api.SendRequest, idemKey string) (*api.Message, error)
  func RunConclusion(args []string) error   // 打印最新结论文件路径 + 全文
  // pullOne：kind=kickoff → <root>/relais/conclusions/<channel>-<id>.md，返回该路径
  ```
- `relais send` 新 flag：`--kind resolved`、`--idempotency-key <k>`；frontmatter 可含 `kind/owner/owner_reason/ack_of`；flag 优先于 frontmatter。
- bridge：`kind=kickoff` → conclusions/ 不跑 hook；`kind=conclusion` → 落 inbox、标已读、**不跑 hook**（握手已成立，无需回复）；其余照旧。
- 在途拒绝：仅当频道 `auto.enabled && in_flight` 时拒绝，错误文案含"开子频道"。
- **多模块共用一个项目目录**：`relais/config.toml` 只记一个默认频道；环境变量 `RELAIS_CHANNEL` 非空时 `findProject` 返回的 `ProjectConfig.Channel` 以它为准。bridge 调 hook 时设 `RELAIS_CHANNEL=<该消息的频道>`，于是 hook 里的 `auto-turn/session/local-prompt/send/needs-human` 全部落在正确频道。工作脑开题用 `RELAIS_CHANNEL=<模块> relais send …`。

- [ ] **Step 1: 写失败的测试**

`internal/msg/format_test.go` 追加：
```go
func TestExtractHeader(t *testing.T) {
	h, body, ok := ExtractHeader([]byte("---\nsummary: 结论\nkind: resolved\nowner: codex\nowner_reason: 熟\nack_of: 01ABC\n---\n\n正文"))
	if !ok || h.Summary != "结论" || h.Kind != "resolved" || h.Owner != "codex" || h.OwnerReason != "熟" || h.AckOf != "01ABC" || body != "正文" {
		t.Fatalf("header 解析错: %+v %q %v", h, body, ok)
	}
	if _, body, ok := ExtractHeader([]byte("纯文本")); ok || body != "纯文本" {
		t.Fatal("无 frontmatter 应 ok=false 且原文返回")
	}
	env := Envelope{ID: "x", Seq: 3, Round: 2, Kind: "conclusion", Owner: "codex"}
	out := string(Render(env, "b"))
	for _, want := range []string{"seq: 3", "round: 2", "kind: conclusion", "owner: codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render 缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(Render(Envelope{ID: "y"}, "b")), "kind:") {
		t.Fatal("空字段应 omitempty")
	}
}
```
`internal/cli/send_test.go` 追加（用文件里现成的 `setupCLITest(t, username, channel)`：真 store + 真 httptest 服务器，用户 hou/wu/sun，频道 duo(hou,wu)/trio）：
```go
func TestSendResolvedFromFrontmatterAndFlags(t *testing.T) {
	st, users, root := setupCLITest(t, "hou", "duo")
	duo, _ := st.ChannelByName("duo")
	st.SetAutoEnabled(duo.ID, true, 16)
	f := filepath.Join(root, "out.md")
	os.WriteFile(f, []byte("---\nowner: codex\nowner_reason: 熟\nack_of: \n---\n\n完整结论"), 0o644)
	if err := RunSend([]string{"--kind", "resolved", "--summary", "谈拢了", "--idempotency-key", "k9", f}); err != nil {
		t.Fatal(err)
	}
	// 同键再发一次 → 服务器只落一条
	if err := RunSend([]string{"--kind", "resolved", "--summary", "谈拢了", "--idempotency-key", "k9", f}); err != nil {
		t.Fatal(err)
	}
	msgs, _ := st.ListEnvelopes(duo.ID, users["wu"].ID, false, false)
	if len(msgs) != 1 || msgs[0].Kind != "resolved" || msgs[0].Owner != "codex" || msgs[0].OwnerReason != "熟" || msgs[0].Summary != "谈拢了" || msgs[0].Seq != 1 {
		t.Fatalf("上送字段/幂等错: %+v", msgs)
	}
	full, _ := st.GetMessage(msgs[0].ID, users["wu"].ID, false)
	if full.Body != "完整结论" {
		t.Fatalf("正文应剥掉 frontmatter: %q", full.Body)
	}
	copies, _ := os.ReadDir(filepath.Join(root, "relais", "sent"))
	data, _ := os.ReadFile(filepath.Join(root, "relais", "sent", copies[0].Name()))
	if !strings.Contains(string(data), "seq: 1") || !strings.Contains(string(data), "kind: resolved") || !strings.Contains(string(data), "owner: codex") {
		t.Fatalf("sent 副本应含 seq/kind/owner: %s", data)
	}
}

func TestSendRejectsWhenInFlight(t *testing.T) {
	st, _, root := setupCLITest(t, "hou", "duo")
	duo, _ := st.ChannelByName("duo")
	st.SetAutoEnabled(duo.ID, true, 16)
	f := filepath.Join(root, "x.md")
	os.WriteFile(f, []byte("hi"), 0o644)
	if err := RunSend([]string{"--summary", "第一封", f}); err != nil {
		t.Fatal(err)
	}
	err := RunSend([]string{"--summary", "第二封", f})
	if err == nil || !strings.Contains(err.Error(), "子频道") {
		t.Fatalf("在途应拒绝并提示子频道: %v", err)
	}
	// auto 未开启的频道不受限（M1 锚点行为）
	st.SetAutoEnabled(duo.ID, false, 16)
	if err := RunSend([]string{"--summary", "第三封", f}); err != nil {
		t.Fatalf("auto 关闭时不应拒绝: %v", err)
	}
}
```
`internal/cli/bridge_test.go` 追加：
```go
func TestFindProjectHonorsChannelEnv(t *testing.T) {
	_, _, _ = setupCLITest(t, "hou", "duo")
	_, proj, err := findProject()
	if err != nil || proj.Channel != "duo" {
		t.Fatalf("默认应为 config.toml 的频道: %+v %v", proj, err)
	}
	t.Setenv("RELAIS_CHANNEL", "trio")
	_, proj, _ = findProject()
	if proj.Channel != "trio" {
		t.Fatalf("RELAIS_CHANNEL 应覆盖: %+v", proj)
	}
}

func TestRunHookPassesChannelEnv(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "env")
	runHook("echo \"$RELAIS_CHANNEL\" > "+marker, "/p", t.TempDir(), api.Message{ID: "1", Channel: "m9", From: "x"})
	data, _ := os.ReadFile(marker)
	if strings.TrimSpace(string(data)) != "m9" {
		t.Fatalf("hook 应收到 RELAIS_CHANNEL=m9: %q", data)
	}
}

func TestPollOnceRoutesKickoffToConclusions(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	duo, _ := st.ChannelByName("duo")
	st.SetAutoEnabled(duo.ID, true, 16)
	m1, _ := st.SaveMessageOpts(duo.ID, users["wu"].ID, []int64{users["hou"].ID}, "s", "结论草", "", store.SaveOpts{Kind: "resolved", Owner: "codex"})
	m2, _ := st.SaveMessageOpts(duo.ID, users["hou"].ID, []int64{users["wu"].ID}, "s", "结论全文", "", store.SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	st.EvaluateHandshake(duo.ID, m2.ID)
	if _, err := st.Kickoff(duo.ID, users["wu"].ID); err != nil {
		t.Fatal(err)
	}
	// hou 侧先把 m1 拉掉（它是发给 hou 的普通 resolved），只留 kickoff 待处理
	st.MarkRead(m1.ID, users["hou"].ID)
	c, _, _ := newClient()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	n, err := pollOnce(c, []bridgeTarget{{Channel: "duo", Dir: proj}}, "touch "+marker, nil)
	if err != nil || n != 1 {
		t.Fatalf("应落 1 条: %d %v", n, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("kickoff 不应触发 hook")
	}
	// conclusion（m2 发给 wu 的那封）在 wu 侧同样不跑 hook
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	saveGlobal(&GlobalConfig{Server: c.Server, Token: users["wu"].AgentToken, Username: "wu"})
	cw, _, _ := newClient()
	wuProj := t.TempDir()
	if n, err := pollOnce(cw, []bridgeTarget{{Channel: "duo", Dir: wuProj}}, "touch "+marker, nil); err != nil || n < 1 {
		t.Fatalf("wu 应拉到 conclusion+kickoff: %d %v", n, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("conclusion 不应触发 hook")
	}
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "conclusions"))
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "duo-") {
		t.Fatalf("应落到 conclusions/duo-<id>.md: %v", entries)
	}
	data, _ := os.ReadFile(filepath.Join(proj, "relais", "conclusions", entries[0].Name()))
	if !strings.Contains(string(data), "结论全文") || !strings.Contains(string(data), "owner: codex") || !strings.Contains(string(data), "kind: kickoff") {
		t.Fatalf("结论文件内容错: %s", data)
	}
	if inbox, _ := os.ReadDir(filepath.Join(proj, "relais", "inbox")); len(inbox) != 0 {
		t.Fatal("kickoff 不应落 inbox")
	}
	if err := RunConclusion(nil); err != nil {
		t.Fatal(err)
	}
}
```
（bridge_test.go import 需要 `"github.com/hou-physics/relais/internal/store"` 与 `"github.com/hou-physics/relais/internal/api"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/msg/ ./internal/cli/ -run 'TestExtractHeader|TestSendResolved|TestSendRejects|TestPollOnceRoutes' -v`
Expected: 编译错误（`ExtractHeader` 未定义等）。

- [ ] **Step 3: 实现**

(a) `internal/msg/format.go`：`Envelope` 加
```go
	Seq         int    `yaml:"seq,omitempty"`
	Round       int    `yaml:"round,omitempty"`
	Kind        string `yaml:"kind,omitempty"`
	Owner       string `yaml:"owner,omitempty"`
	OwnerReason string `yaml:"owner_reason,omitempty"`
	AckOf       string `yaml:"ack_of,omitempty"`
```
新增：
```go
type Header struct {
	Summary     string `yaml:"summary"`
	Kind        string `yaml:"kind"`
	Owner       string `yaml:"owner"`
	OwnerReason string `yaml:"owner_reason"`
	AckOf       string `yaml:"ack_of"`
}

// ExtractHeader 宽松解析 agent 写的 frontmatter（只认这几个键，其余忽略）；无 frontmatter 时 ok=false 且 body=原文。
func ExtractHeader(data []byte) (Header, string, bool) {
	var h Header
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return h, s, false
	}
	rest := s[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return h, s, false
	}
	if err := yaml.Unmarshal([]byte(rest[:idx]), &h); err != nil {
		return h, s, false
	}
	body := strings.TrimPrefix(rest[idx+len("\n---\n"):], "\n")
	return h, body, true
}
```
（`ExtractSummary` 保留不动。）

(b) `internal/cli/client.go`：把 `do` 改成 `doH(method, path string, headers map[string]string, in, out any)`，`do` 变成 `return c.doH(method, path, nil, in, out)`；`doH` 在设 Authorization 后 `for k, v := range headers { req.Header.Set(k, v) }`。新增：
```go
func (c *Client) SendWithKey(channel string, req api.SendRequest, idemKey string) (*api.Message, error) {
	var m api.Message
	var h map[string]string
	if idemKey != "" {
		h = map[string]string{"Idempotency-Key": idemKey}
	}
	err := c.doH("POST", "/api/channels/"+url.PathEscape(channel)+"/messages", h, req, &m)
	return &m, err
}
```

(c) `internal/cli/send.go` `prepareOutgoing`：加 flag
```go
	kind := fs.String("kind", "", "消息类别：空或 resolved（握手提议/附和）")
	idemKey := fs.String("idempotency-key", "", "幂等键（自动路径用；同键只落一条）")
```
`outgoing` 加字段 `idemKey string`。把"处理摘要"那段替换为：
```go
	hdr, rest, hasHdr := msg.ExtractHeader(body)
	bodyStr := string(body)
	summaryVal := *summary
	if hasHdr {
		bodyStr = rest
		if summaryVal == "" {
			summaryVal = hdr.Summary
		}
	}
	if summaryVal == "" {
		return nil, fmt.Errorf("--summary 必填（或在文件头 frontmatter 写 summary: 字段）：给人看的一两句话")
	}
	kindVal := *kind
	if kindVal == "" {
		kindVal = hdr.Kind
	}
	// 在途拒绝（D37）：只对开启了自主循环的频道生效
	if st, err := c.AutoGet(proj.Channel); err == nil && st.Enabled && st.InFlight {
		return nil, fmt.Errorf("频道 %q 有议题在途（上一封还没被对方读完）。并行讨论请开子频道：relais local init %s-<议题>", proj.Channel, proj.Channel)
	}
	req := api.SendRequest{To: recipients, Summary: summaryVal, Body: bodyStr, InReplyTo: *reply,
		Kind: kindVal, Owner: hdr.Owner, OwnerReason: hdr.OwnerReason, AckOf: hdr.AckOf}
```
并把返回的 `outgoing{...}` 里 `req: req, idemKey: *idemKey`。`RunSend` 里 `o.client.Send(...)` 改 `o.client.SendWithKey(o.proj.Channel, o.req, o.idemKey)`；`sent/` 副本的 `msg.Envelope{...}` 加 `Seq: sent.Seq, Round: sent.Round, Kind: sent.Kind, Owner: sent.Owner, OwnerReason: sent.OwnerReason, AckOf: sent.AckOf`。注意 `draft.go` 也调 `prepareOutgoing`，保持兼容（草稿路径 `AutoGet` 失败即忽略，见上面 `err == nil` 判断）。

(d) `internal/cli/inbox.go` `pullOne`：`env` 加同样六个字段；在 `filename := ...` 之前分流：
```go
	if full.Kind == "kickoff" {
		dir := filepath.Join(root, "relais", "conclusions")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		path := filepath.Join(dir, fmt.Sprintf("%s-%s.md", full.Channel, full.ID))
		if err := os.WriteFile(path, msg.Render(env, full.Body), 0o644); err != nil {
			return "", err
		}
		if err := c.MarkRead(full.ID); err != nil {
			return "", err
		}
		return path, nil
	}
```

(e0) `internal/cli/config.go` `findProject`：在 `return dir, &cfg, nil` 之前加：
```go
			if ch := os.Getenv("RELAIS_CHANNEL"); ch != "" {
				cfg.Channel = ch
			}
```
`internal/cli/bridge.go` `runHook` 的 `cmd.Env` 列表加一项 `"RELAIS_CHANNEL="+m.Channel,`。

(e) `internal/cli/bridge.go` `pollOnce`：把 `runHook(hook, path, tgt.Dir, envMsg)` 一行改为：
```go
			if envMsg.Kind == "kickoff" {
				fmt.Printf("[%s] 已开工 · 承接方 %s → %s\n  在工作脑里执行：relais conclusion\n", tgt.Channel, envMsg.Owner, path)
				continue
			}
			if envMsg.Kind == "conclusion" {
				// 握手已成立的那封不需要回复（甩手模式下服务器已同时 kickoff，若跑 hook 讨论脑会对结论再回一封）
				fmt.Printf("[%s] 已握手：%s（承接方 %s）\n", tgt.Channel, envMsg.Summary, envMsg.Owner)
				continue
			}
			runHook(hook, path, tgt.Dir, envMsg)
```
（通知已在 `notify` 调用里发出，summary 即"开工 · 承接方 X"。）

(f) `internal/cli/conclusion.go`：
```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunConclusion 打印本项目频道最新一份结论（relais/conclusions/<频道>-<id>.md）。
func RunConclusion(args []string) error {
	root, proj, err := findProject()
	if err != nil {
		return err
	}
	channel := proj.Channel
	if len(args) == 1 {
		channel = args[0]
	}
	dir := filepath.Join(root, "relais", "conclusions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("频道 %q 还没有结论（目录 %s 不存在）", channel, dir)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), channel+"-") && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("频道 %q 还没有结论", channel)
	}
	sort.Strings(names) // ULID 时间有序，最后一个最新
	p := filepath.Join(dir, names[len(names)-1])
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fmt.Printf("结论文件: %s\n\n%s\n", p, data)
	return nil
}
```
`main.go` 用法串加 `conclusion`，分发 `case "conclusion": return cli.RunConclusion(args[1:])`。

(g) `internal/cli/auto.go` `status` 分支：`state = fmt.Sprintf("开启（第 %d/%d 回合）", (st.RoundCount+1)/2, (st.Cap+1)/2)`，并在 `NeedsHumanQ` 之后加：
```go
		if st.Resolved {
			state += " · 已握手待确认：" + st.ResolutionSummary + "（承接方 " + st.Owner + "）"
		}
		if st.KickedOff {
			state += " · 已开工（relais conclusion 查看）"
		}
		state += " · 模式 " + st.Mode
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/msg/ ./internal/cli/ -v -race`
Expected: 全 PASS。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/msg internal/cli main.go
git commit -m "feat(cli): send 支持 kind/owner/ack_of 与幂等键、在途拒绝；kickoff 落 conclusions/；relais conclusion（M7 Task 4）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: cli —— 续会话登记、讨论脑提示词、本地五分支 hook

**Files:**
- Create: `internal/cli/session.go`（`sessions.toml` 读写 + `relais session get|set|clear`）
- Create: `internal/cli/localprompt.go`（`relais local-prompt [--first]`）
- Create: `internal/cli/localhook.go`（`writeLocalHook(dir string, info SetupInfo) (string, error)`）
- Modify: `main.go`（`session`、`local-prompt` 分发）
- Test: `internal/cli/session_test.go`、`internal/cli/localprompt_test.go`、`internal/cli/localhook_test.go`

**Interfaces:**
- Produces：
  ```go
  func sessionGet(dir, channel string) (string, error)          // 无则 ""
  func sessionSet(dir, channel, id string) error
  func sessionClear(dir, channel string) error
  func RunSession(args []string) error                          // get | set <id> | clear；频道取自 findProject；dir 取自 configDir()
  func localPrompt(side, channel, msgPath, rules, guidance string, first bool) string
  func RunLocalPrompt(args []string) error                      // 读 RELAIS_MSG_PATH、<root>/relais/RULES.md、guidance-pull，打印提示词
  func writeLocalHook(dir string, info SetupInfo) (string, error) // 写 <dir>/hooks/auto-reply.sh
  ```
- hook 契约（agent 输出 → 分支）：首行 `RESOLVED: <一句>` → `relais send --kind resolved --summary "<一句>" <去掉首行的文件>`；首行 `NEEDS_HUMAN:` → `relais needs-human`；首行 `---` → `relais send`；否则跳过。每次 send 带 `--idempotency-key $(sha256 of 频道+RELAIS_MSG_ID+输出)`。
- 会话：Claude `-p --session-id <uuid>`（首次）/ `-p --resume <uuid>`（续），`--allowedTools "Read,Grep,Glob"`；Codex 首次 `exec --json -o <OUT> -c 'sandbox_mode="read-only"' -C <root> "<prompt>"`，从 JSONL 里 `grep -o '"thread_id":"[^"]*"'` 取 id；续 `exec resume -c 'sandbox_mode="read-only"' <id> "<prompt>"`，stdout 即回复。resume 非零退出 → 清条目、按首次重来一次。

- [ ] **Step 1: 写失败的测试**

`internal/cli/session_test.go`：
```go
package cli

import "testing"

func TestSessionRegistry(t *testing.T) {
	dir := t.TempDir()
	if id, err := sessionGet(dir, "m1"); err != nil || id != "" {
		t.Fatalf("空登记应返回空: %q %v", id, err)
	}
	if err := sessionSet(dir, "m1", "uuid-1"); err != nil {
		t.Fatal(err)
	}
	if err := sessionSet(dir, "m2", "uuid-2"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(dir, "m1"); id != "uuid-1" {
		t.Fatalf("读回错: %q", id)
	}
	if err := sessionSet(dir, "m1", "uuid-1b"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(dir, "m1"); id != "uuid-1b" {
		t.Fatal("应覆盖")
	}
	if err := sessionClear(dir, "m1"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(dir, "m1"); id != "" {
		t.Fatal("clear 后应空")
	}
	if id, _ := sessionGet(dir, "m2"); id != "uuid-2" {
		t.Fatal("clear 不应影响其他频道")
	}
	if err := sessionClear(dir, "nope"); err != nil {
		t.Fatalf("clear 不存在的应幂等: %v", err)
	}
}
```
`internal/cli/localprompt_test.go`：
```go
package cli

import (
	"strings"
	"testing"
)

func TestLocalPromptContents(t *testing.T) {
	p := localPrompt("claude", "grammar", "/x/relais/inbox/1.md", "不碰 8000 端口", "优先速度", true)
	for _, want := range []string{
		"claude", "grammar", "/x/relais/inbox/1.md",
		"不碰 8000 端口",           // RULES.md 原文
		"优先速度",                 // guidance
		"relais/inbox", "relais/sent", // 首次补读历史
		"RESOLVED:", "NEEDS_HUMAN:", "owner:", "ack_of:", "owner_reason:",
		"不得新增条款", "不替对方改代码", "不写密钥", "工程附录",
		"summary:",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("提示词缺 %q", want)
		}
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", false), "relais/sent") {
		t.Fatal("续会话不应要求补读历史")
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", false), "雇主引导") {
		t.Fatal("无 guidance 时不应出现引导段")
	}
}
```
`internal/cli/localhook_test.go`：
```go
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalHookClaude(t *testing.T) {
	dir := t.TempDir()
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: "/opt/claude", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hp)
	h := string(data)
	for _, want := range []string{
		"auto-turn", "local-prompt", "session get", "session set", "session clear",
		"--session-id", "--resume", `--allowedTools "Read,Grep,Glob"`, "/opt/claude",
		"RESOLVED:", "NEEDS_HUMAN:", "--kind resolved", "--idempotency-key", "needs-human",
		"uuidgen",
	} {
		if !strings.Contains(h, want) {
			t.Fatalf("claude hook 缺 %q", want)
		}
	}
	if !strings.HasPrefix(h, "#!/bin/sh\n") {
		t.Fatal("应是 sh 脚本")
	}
	st, _ := os.Stat(hp)
	if st.Mode()&0o100 == 0 {
		t.Fatal("hook 应可执行")
	}
	// 分支优先级：RESOLVED 的判断必须出现在 NEEDS_HUMAN 之前
	if strings.Index(h, "^RESOLVED:") > strings.Index(h, "^NEEDS_HUMAN:") {
		t.Fatal("RESOLVED 分支必须先于 NEEDS_HUMAN")
	}
}

func TestLocalHookCodex(t *testing.T) {
	dir := t.TempDir()
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "codex", AgentPath: "/opt/codex", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hp)
	h := string(data)
	for _, want := range []string{"exec resume", "--json", "thread_id", `sandbox_mode="read-only"`, "/opt/codex", "-o "} {
		if !strings.Contains(h, want) {
			t.Fatalf("codex hook 缺 %q", want)
		}
	}
	if strings.Contains(h, "--allowedTools") {
		t.Fatal("codex hook 不该有 claude 参数")
	}
}

// 用桩 relais（记录调用参数）+ 桩 agent（按 FAKE_OUT 输出）真的跑一遍 hook，验证五分支与会话登记。
func TestLocalHookBranchesExecute(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\ncase \"$1\" in\n  session) [ \"$2\" = get ] && printf '%s' \"${FAKE_SID:-}\";;\n  local-prompt) printf 'P';;\nesac\nexit 0\n"), 0o755)
	stubAgent := filepath.Join(dir, "claude")
	os.WriteFile(stubAgent, []byte("#!/bin/sh\ncat \"$FAKE_OUT\"\n"), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: stubAgent, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	run := func(agentOut, sid string) string {
		os.Remove(log)
		out := filepath.Join(dir, "out.txt")
		os.WriteFile(out, []byte(agentOut), 0o644)
		cmd := exec.Command("sh", hp)
		cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+proj, "RELAIS_MSG_PATH="+out,
			"RELAIS_MSG_ID=01X", "RELAIS_CHANNEL=m1", "FAKE_OUT="+out, "FAKE_SID="+sid)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook 失败: %v %s", err, b)
		}
		b, _ := os.ReadFile(log)
		return string(b)
	}
	// RESOLVED 分支：首次唤醒 → local-prompt --first、session set、send --kind resolved
	got := run("RESOLVED: 谈拢了\n---\nowner: codex\n---\n正文", "")
	for _, want := range []string{"auto-turn", "local-prompt --first", "session set ", "send --kind resolved --summary 谈拢了 --idempotency-key "} {
		if !strings.Contains(got, want) {
			t.Fatalf("RESOLVED 分支缺 %q:\n%s", want, got)
		}
	}
	// 续会话：不带 --first；NEEDS_HUMAN 分支
	got = run("NEEDS_HUMAN: 预算多少\n", "sid-1")
	if strings.Contains(got, "--first") || !strings.Contains(got, "needs-human 预算多少") || strings.Contains(got, "send ") {
		t.Fatalf("NEEDS_HUMAN 分支错:\n%s", got)
	}
	// 普通回信分支
	got = run("---\nsummary: 回\n---\n正文", "sid-1")
	if !strings.Contains(got, "send --idempotency-key ") || strings.Contains(got, "--kind") {
		t.Fatalf("普通分支错:\n%s", got)
	}
	// 格式不符 → 不 send 不 needs-human
	got = run("随便说点什么", "sid-1")
	if strings.Contains(got, "send ") || strings.Contains(got, "needs-human") {
		t.Fatalf("不符分支不应发送:\n%s", got)
	}
}

func TestLocalHookRejectsUnknownAgent(t *testing.T) {
	if _, err := writeLocalHook(t.TempDir(), SetupInfo{Agent: "kimi", AgentPath: "/k"}); err == nil {
		t.Fatal("本地模式只支持 claude/codex")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run 'TestSessionRegistry|TestLocalPrompt|TestLocalHook' -v`
Expected: 编译错误（`sessionGet` 等未定义）。

- [ ] **Step 3: 实现 session.go**

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// sessions.toml：每（频道）一条讨论脑会话 id（本侧配置目录下，D36）。
type sessionEntry struct {
	SessionID string `toml:"session_id"`
	CreatedAt string `toml:"created_at"`
}

type sessionRegistry struct {
	Channels map[string]sessionEntry `toml:"channels"`
}

func sessionsPath(dir string) string { return filepath.Join(dir, "sessions.toml") }

func loadSessions(dir string) (*sessionRegistry, error) {
	reg := &sessionRegistry{Channels: map[string]sessionEntry{}}
	if _, err := toml.DecodeFile(sessionsPath(dir), reg); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if reg.Channels == nil {
		reg.Channels = map[string]sessionEntry{}
	}
	return reg, nil
}

func saveSessions(dir string, reg *sessionRegistry) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(sessionsPath(dir), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(reg)
}

func sessionGet(dir, channel string) (string, error) {
	reg, err := loadSessions(dir)
	if err != nil {
		return "", err
	}
	return reg.Channels[channel].SessionID, nil
}

func sessionSet(dir, channel, id string) error {
	reg, err := loadSessions(dir)
	if err != nil {
		return err
	}
	reg.Channels[channel] = sessionEntry{SessionID: id, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	return saveSessions(dir, reg)
}

func sessionClear(dir, channel string) error {
	reg, err := loadSessions(dir)
	if err != nil {
		return err
	}
	delete(reg.Channels, channel)
	return saveSessions(dir, reg)
}

// RunSession 供 hook 调：relais session get | set <id> | clear（频道 = 当前项目绑定的频道）。
func RunSession(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais session <get|set <id>|clear>")
	}
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	switch args[0] {
	case "get":
		id, err := sessionGet(dir, proj.Channel)
		if err != nil {
			return err
		}
		fmt.Print(id)
		return nil
	case "set":
		if len(args) != 2 || args[1] == "" {
			return fmt.Errorf("用法: relais session set <id>")
		}
		return sessionSet(dir, proj.Channel, args[1])
	case "clear":
		return sessionClear(dir, proj.Channel)
	default:
		return fmt.Errorf("未知 session 子命令 %q", args[0])
	}
}
```

- [ ] **Step 4: 实现 localprompt.go**

```go
package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localPrompt 组装讨论脑一轮的提示词（D40 基线规矩 + 项目 RULES.md + 本轮信息）。
func localPrompt(side, channel, msgPath, rules, guidance string, first bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是 Relais 本地模式里「%s 侧」的讨论脑，负责频道（模块）「%s」。对方是另一个 AI（%s 侧）。你们的雇主是同一个人。\n\n",
		side, channel, otherSide(side))
	b.WriteString("## 规矩（必须遵守）\n")
	b.WriteString("1. 信里只写观点、问题、提议、决定。大段参考材料给路径，不整段粘贴。\n")
	b.WriteString("2. 不替雇主做决定：需要人定的事，用 NEEDS_HUMAN 停下来问。\n")
	b.WriteString("3. 正文写给不懂技术的人也能读；技术细节放最后的「工程附录」。\n")
	b.WriteString("4. 不写密钥、不贴数据库内容、不写个人数据。\n")
	b.WriteString("5. 不替对方改代码，也不改任何文件：你只读仓库。要对方改什么，写在信里。\n")
	b.WriteString("6. 明确不同意对方的 RESOLVED 时，写普通回信说明分歧，不要写 RESOLVED。\n")
	if strings.TrimSpace(rules) != "" {
		b.WriteString("\n## 本项目铁律（relais/RULES.md）\n")
		b.WriteString(strings.TrimSpace(rules))
		b.WriteString("\n")
	}
	b.WriteString("\n## 本轮\n")
	if first {
		fmt.Fprintf(&b, "这是你在本频道第一次发言。先读 relais/inbox/ 与 relais/sent/ 下信头 channel: %s 的全部往来（文件名以日期开头），再读新信。\n", channel)
	}
	fmt.Fprintf(&b, "新信在文件 %s。当前目录是项目根，可用只读工具查看代码。同目录下可能混有其他模块的信，只看信头 channel: %s 的。\n", msgPath, channel)
	if strings.TrimSpace(guidance) != "" {
		b.WriteString("\n## 雇主引导（优先遵循）\n")
		b.WriteString(strings.TrimSpace(guidance))
		b.WriteString("\n")
	}
	b.WriteString("\n## 输出格式（只输出以下三种之一，不要解释、不要代码围栏）\n")
	b.WriteString("A) 认为议题已可收敛（双方立场一致，无未决点）：\n")
	b.WriteString("RESOLVED: <一句话结论>\n---\nowner: claude|codex|user\nowner_reason: <一句：谁一直主要负责这条线、为何由它开工；定不下来写 user>\nack_of: <若是附和对方的 RESOLVED，填对方那封的 id（信头 id: 字段）；自己首次提议留空>\n---\n<完整结论正文：做什么、不做什么、验收标准。附和时可改措辞，不得新增条款；要新增就写成普通回信>\n\n")
	b.WriteString("B) 需要雇主定夺或需要事实澄清：\nNEEDS_HUMAN: <一行问题>\n\n")
	b.WriteString("C) 普通回信：\n---\nsummary: <一句话摘要>\n---\n<正文>\n")
	return b.String()
}

func otherSide(side string) string {
	if side == "claude" {
		return "codex"
	}
	return "claude"
}

// RunLocalPrompt 供本地 hook 调：relais local-prompt [--first]，读 RELAIS_MSG_PATH、项目 RULES.md、私有引导，打印提示词。
func RunLocalPrompt(args []string) error {
	fs := flag.NewFlagSet("local-prompt", flag.ContinueOnError)
	first := fs.Bool("first", false, "本频道首次唤醒（补读历史）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, proj, err := findProject()
	if err != nil {
		return err
	}
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	msgPath := os.Getenv("RELAIS_MSG_PATH")
	if msgPath == "" {
		return fmt.Errorf("RELAIS_MSG_PATH 未设置（此命令由 bridge hook 调用）")
	}
	rules, _ := os.ReadFile(filepath.Join(root, "relais", "RULES.md"))
	guidance, _ := c.GuidancePull(proj.Channel) // 取不到就当空，不阻塞本轮
	fmt.Print(localPrompt(cfg.Username, proj.Channel, msgPath, string(rules), guidance, *first))
	return nil
}
```

- [ ] **Step 5: 实现 localhook.go**

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// writeLocalHook 生成本地模式的五分支 hook（D36 续会话 + D40 只读 + §5 五分支）。
// 与联网 hook（setup.go writeHook）分文件、互不影响。
func writeLocalHook(dir string, info SetupInfo) (string, error) {
	var agentFirst, agentResume string
	switch info.Agent {
	case "claude":
		agentFirst = `"$AGENT" -p --session-id "$SID" --allowedTools "Read,Grep,Glob" "$PROMPT" > "$OUT" 2>"$ERR"`
		agentResume = `"$AGENT" -p --resume "$SID" --allowedTools "Read,Grep,Glob" "$PROMPT" > "$OUT" 2>"$ERR"`
	case "codex":
		// 首次：--json 事件流进 $EV（含 thread_id），最终回复经 -o 写入 $OUT
		agentFirst = `"$AGENT" exec --json -o "$OUT" -c 'sandbox_mode="read-only"' -C "$RELAIS_MSG_DIR" "$PROMPT" > "$EV" 2>"$ERR"` + "\n" +
			`  SID="$(grep -o '"thread_id":"[^"]*"' "$EV" | head -1 | sed 's/.*:"//;s/"$//')"`
		agentResume = `"$AGENT" exec resume -c 'sandbox_mode="read-only"' "$SID" "$PROMPT" > "$OUT" 2>"$ERR"`
	default:
		return "", fmt.Errorf("本地模式只支持 claude 或 codex，得到 %q", info.Agent)
	}
	relais, _ := os.Executable()
	if relais == "" {
		relais = "relais"
	}
	hd := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		return "", err
	}
	newSID := `SID="$(uuidgen | tr 'A-Z' 'a-z')"`
	if info.Agent == "codex" {
		newSID = `SID=""` // codex 的 id 由首次运行产出
	}
	script := "#!/bin/sh\n" +
		"# Relais 本地模式 hook（" + info.Agent + " 侧）：续会话 + 只读 + 五分支。由 relais local init 生成。\n" +
		"AGENT=\"" + info.AgentPath + "\"\n" +
		"RELAIS=\"${RELAIS_BIN:-" + relais + "}\"\n" +
		"export PATH=\"" + filepath.Dir(info.AgentPath) + ":" + filepath.Dir(relais) + ":$PATH\"\n" +
		"cd \"$RELAIS_MSG_DIR\" || exit 1\n" +
		"# 1) 服务器闸门\n" +
		"\"$RELAIS\" auto-turn || { echo \"auto: 已暂停/到上限/需人处理，本条不自动回复\"; exit 0; }\n" +
		"# 2) 会话：有则续，无则新建（首次要补读历史）\n" +
		"SID=\"$(\"$RELAIS\" session get)\"\n" +
		"FIRST=\"\"\n" +
		"if [ -z \"$SID\" ]; then FIRST=\"--first\"; " + newSID + "; fi\n" +
		"PROMPT=\"$(\"$RELAIS\" local-prompt $FIRST)\"\n" +
		"OUT=\"$(mktemp)\"; ERR=\"$(mktemp)\"; EV=\"$(mktemp)\"\n" +
		"# 3) 起讨论脑（只读）\n" +
		"if [ -n \"$FIRST\" ]; then\n" +
		"  " + agentFirst + "\n" +
		"  RC=$?\n" +
		"else\n" +
		"  " + agentResume + "\n" +
		"  RC=$?\n" +
		"  if [ $RC -ne 0 ]; then\n" +
		"    echo \"auto: 续会话失败（$SID），改为新建会话重来一次\"\n" +
		"    \"$RELAIS\" session clear\n" +
		"    " + newSID + "\n" +
		"    PROMPT=\"$(\"$RELAIS\" local-prompt --first)\"\n" +
		"    " + agentFirst + "\n" +
		"    RC=$?\n" +
		"  fi\n" +
		"fi\n" +
		"if [ $RC -ne 0 ]; then echo \"auto: agent 退出码 $RC，本条跳过\"; cat \"$ERR\"; rm -f \"$OUT\" \"$ERR\" \"$EV\"; exit 0; fi\n" +
		"[ -n \"$SID\" ] && \"$RELAIS\" session set \"$SID\"\n" +
		"# 4) 幂等键：频道 + 源消息 + 输出内容\n" +
		"KEY=\"$( { printf '%s' \"$RELAIS_MSG_ID\"; cat \"$OUT\"; } | shasum -a 256 | cut -c1-16)\"\n" +
		"# 5) 五分支（优先级：RESOLVED > NEEDS_HUMAN > --- > 跳过）\n" +
		"if head -1 \"$OUT\" | grep -q '^RESOLVED:'; then\n" +
		"  S=\"$(head -1 \"$OUT\" | sed 's/^RESOLVED: *//')\"\n" +
		"  BODY=\"$(mktemp)\"; tail -n +2 \"$OUT\" > \"$BODY\"\n" +
		"  \"$RELAIS\" send --kind resolved --summary \"$S\" --idempotency-key \"$KEY\" \"$BODY\"\n" +
		"  rm -f \"$BODY\"\n" +
		"elif head -1 \"$OUT\" | grep -q '^NEEDS_HUMAN:'; then\n" +
		"  Q=\"$(head -1 \"$OUT\" | sed 's/^NEEDS_HUMAN: *//')\"\n" +
		"  \"$RELAIS\" needs-human \"$Q\"\n" +
		"elif head -1 \"$OUT\" | grep -q '^---'; then\n" +
		"  \"$RELAIS\" send --idempotency-key \"$KEY\" \"$OUT\"\n" +
		"else\n" +
		"  echo \"auto: agent 输出格式不符，跳过本条\"\n" +
		"fi\n" +
		"rm -f \"$OUT\" \"$ERR\" \"$EV\"\n"
	hp := filepath.Join(hd, "auto-reply.sh")
	if err := os.WriteFile(hp, []byte(script), 0o755); err != nil {
		return "", err
	}
	return hp, nil
}
```
说明：幂等键 = 源消息 ULID（全局唯一，已隐含频道）+ 输出内容的 sha256 前 16 位。`--session-id` 需要小写 UUID，故 `uuidgen | tr`。`RELAIS="${RELAIS_BIN:-<exe>}"` 让测试能用桩替换 relais。

- [ ] **Step 6: main.go 分发**

用法串加 `session|local-prompt`；加：
```go
	case "session":
		return cli.RunSession(args[1:])
	case "local-prompt":
		return cli.RunLocalPrompt(args[1:])
```

- [ ] **Step 7: 跑测试确认通过**

Run: `go test ./internal/cli/ -v -race`；另外手工语法检查生成物：
```bash
cd /tmp && RELAIS_CONFIG_DIR=$(mktemp -d) && go run /Users/hou.astro/atoaengine/.claude/worktrees/multi-ai-collaboration-discussion-c46b7c -c 'x' >/dev/null 2>&1; true
```
（上面这行可略；关键是单测里再加一步：把生成的 hook 用 `sh -n` 检查。）在 `TestLocalHookClaude` 与 `TestLocalHookCodex` 末尾各加：
```go
	if out, err := exec.Command("sh", "-n", hp).CombinedOutput(); err != nil {
		t.Fatalf("hook 语法错误: %v %s", err, out)
	}
```
（import `"os/exec"`。）
Expected: 全 PASS。

- [ ] **Step 8: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/cli main.go
git commit -m "feat(cli): 讨论脑续会话登记 + local-prompt 提示词 + 本地五分支只读 hook（M7 Task 5）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: cli —— `relais local init | status | close`

**Files:**
- Create: `internal/cli/local.go`
- Modify: `internal/cli/config.go`（抽 `saveGlobalTo(dir, cfg)`、`registerProjectIn(dir, channel, root)`，原函数改为薄壳）
- Modify: `internal/cli/setup.go`（抽 `saveSetupTo(dir, info)`）
- Modify: `internal/cli/initcmd.go`（抽 `initProject(root, server, channel, username) (string, error)`，`RunInit` = HTTP 校验 + 调它）
- Modify: `internal/cli/service.go`（新增 `installPlist(label string, args []string, env map[string]string) (string, error)`）
- Modify: `main.go`（`local` 分发）
- Test: `internal/cli/local_test.go`

**Interfaces:**
- Consumes：Task 2 的 `SetAutoEnabled/SetMode/CloseChannel/GetAuto/GetSetting/SetSetting`；Task 5 的 `writeLocalHook`、`sessionClear`。
- Produces：
  ```go
  func localDir() (string, error)   // $RELAIS_LOCAL_DIR 或 os.UserConfigDir()/relais-local
  func RunLocal(args []string) error
  // relais local init [--claude <path>] [--codex <path>] [--project <dir>] [--listen 127.0.0.1:8080] [--no-service] <模块名>...
  // relais local status
  // relais local close <频道>
  ```
- 目录布局（全部在 localDir 下）：`server.toml`、`data/relais.db`、`sides/claude/{config.toml,setup.toml,projects.toml,sessions.toml,hooks/auto-reply.sh}`、`sides/codex/…`、`human.txt`（人的账号与初始密码，只在首次创建时写）。
- 幂等：重复执行不重建用户/频道/hook 以外的东西；hook 每次重写（保证与二进制版本一致）。

- [ ] **Step 1: 写失败的测试**

`internal/cli/local_test.go`：
```go
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/hou-physics/relais/internal/store"
)

func TestLocalInitIsIdempotent(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	proj := t.TempDir()
	args := []string{"init", "--claude", "/bin/echo", "--codex", "/bin/cat", "--project", proj, "--no-service", "grammar", "reader"}
	if err := RunLocal(args); err != nil {
		t.Fatal(err)
	}
	if err := RunLocal(args); err != nil {
		t.Fatalf("第二次应幂等: %v", err)
	}
	// 服务器配置
	var sc ServerConfig
	if _, err := toml.DecodeFile(filepath.Join(ld, "server.toml"), &sc); err != nil || sc.Listen != "127.0.0.1:8080" || sc.DataDir != filepath.Join(ld, "data") {
		t.Fatalf("server.toml 错: %+v %v", sc, err)
	}
	// 库：三用户、两频道各三成员、auto 开启 cap=16 supervised
	st, err := store.Open(filepath.Join(ld, "data", "relais.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, n := range []string{"claude", "codex", "hou"} {
		if _, err := st.UserByName(n); err != nil {
			t.Fatalf("用户 %s 应存在: %v", n, err)
		}
	}
	hou, _ := st.UserByName("hou")
	if !hou.IsAdmin {
		t.Fatal("hou 应是管理员（网页登录用）")
	}
	for _, chName := range []string{"grammar", "reader"} {
		ch, err := st.ChannelByName(chName)
		if err != nil {
			t.Fatalf("频道 %s 应存在", chName)
		}
		if ms, _ := st.ListMembers(ch.ID); len(ms) != 3 {
			t.Fatalf("频道 %s 应 3 成员: %v", chName, ms)
		}
		a, _ := st.GetAuto(ch.ID)
		if !a.Enabled || a.Cap != 16 || a.Mode != "supervised" {
			t.Fatalf("本地频道默认 enabled cap=16 supervised: %+v", a)
		}
	}
	// 两侧配置目录
	for _, side := range []string{"claude", "codex"} {
		d := filepath.Join(ld, "sides", side)
		var g GlobalConfig
		if _, err := toml.DecodeFile(filepath.Join(d, "config.toml"), &g); err != nil || g.Username != side || g.Server != "http://127.0.0.1:8080" || g.Token == "" {
			t.Fatalf("%s config.toml 错: %+v %v", side, g, err)
		}
		u, _ := st.UserByName(side)
		if g.Token != u.AgentToken {
			t.Fatalf("%s token 应与库一致", side)
		}
		var si SetupInfo
		toml.DecodeFile(filepath.Join(d, "setup.toml"), &si)
		if si.Agent != side || si.Mode != "auto" || !strings.HasSuffix(si.HookPath, "auto-reply.sh") {
			t.Fatalf("%s setup.toml 错: %+v", side, si)
		}
		if _, err := os.Stat(si.HookPath); err != nil {
			t.Fatalf("%s hook 应存在", side)
		}
		ps, _ := loadProjectsIn(d)
		if len(ps) != 2 {
			t.Fatalf("%s projects.toml 应登记 2 个频道（重复 init 不重复登记）: %v", side, ps)
		}
	}
	// 项目目录
	if _, err := os.Stat(filepath.Join(proj, "relais", "RULES.md")); err != nil {
		t.Fatal("应生成 RULES.md")
	}
	var pc ProjectConfig
	toml.DecodeFile(filepath.Join(proj, "relais", "config.toml"), &pc)
	if pc.Channel != "grammar" { // 第一个模块作默认绑定；其余模块靠 RELAIS_CHANNEL（Task 4）
		t.Fatalf("项目绑定错: %+v", pc)
	}
	// 人的账号只写一次
	if data, err := os.ReadFile(filepath.Join(ld, "human.txt")); err != nil || !strings.Contains(string(data), "hou") {
		t.Fatalf("human.txt 应含账号: %v", err)
	}
}

func TestLocalInitRequiresBothAgents(t *testing.T) {
	t.Setenv("RELAIS_LOCAL_DIR", t.TempDir())
	err := RunLocal([]string{"init", "--claude", "/bin/echo", "--codex", "/nonexistent/codex", "--project", t.TempDir(), "--no-service", "m1"})
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("codex 路径不存在应报错: %v", err)
	}
}

func TestLocalStatusAndClose(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	proj := t.TempDir()
	if err := RunLocal([]string{"init", "--claude", "/bin/echo", "--codex", "/bin/cat", "--project", proj, "--no-service", "m1"}); err != nil {
		t.Fatal(err)
	}
	sessionSet(filepath.Join(ld, "sides", "claude"), "m1", "sid-1")
	if err := RunLocal([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if err := RunLocal([]string{"close", "m1"}); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(filepath.Join(ld, "data", "relais.db"))
	defer st.Close()
	ch, _ := st.ChannelByName("m1")
	if a, _ := st.GetAuto(ch.ID); !a.Closed {
		t.Fatal("close 后应 closed")
	}
	if id, _ := sessionGet(filepath.Join(ld, "sides", "claude"), "m1"); id != "" {
		t.Fatal("close 应作废会话 id")
	}
	if err := RunLocal([]string{"close", "nope"}); err == nil {
		t.Fatal("关闭不存在的频道应报错")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run TestLocal -v`
Expected: 编译错误（`RunLocal`、`loadProjectsIn` 未定义）。

- [ ] **Step 3: 重构出带目录参数的 helper**

`config.go`：
```go
func saveGlobal(cfg *GlobalConfig) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	return saveGlobalTo(dir, cfg)
}

func saveGlobalTo(dir string, cfg *GlobalConfig) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "config.toml"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

func registerProject(channel, dir string) error {
	registryDir, err := configDir()
	if err != nil {
		return err
	}
	return registerProjectIn(registryDir, channel, dir)
}

func registerProjectIn(registryDir, channel, dir string) error {
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(registryDir, "projects.toml")
	ps, _ := loadProjectsIn(registryDir)
	found := false
	for i, p := range ps {
		if p.Channel == channel {
			ps[i].Dir = dir
			found = true
			break
		}
	}
	if !found {
		ps = append(ps, ProjectBinding{Channel: channel, Dir: dir})
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(ProjectRegistry{Projects: ps})
}

func loadProjects() ([]ProjectBinding, error) {
	registryDir, err := configDir()
	if err != nil {
		return nil, err
	}
	return loadProjectsIn(registryDir)
}

func loadProjectsIn(registryDir string) ([]ProjectBinding, error) {
	path := filepath.Join(registryDir, "projects.toml")
	var reg ProjectRegistry
	if _, err := toml.DecodeFile(path, &reg); err != nil {
		if os.IsNotExist(err) {
			return []ProjectBinding{}, nil
		}
		return nil, err
	}
	return reg.Projects, nil
}
```
`setup.go`：`saveSetup(info)` → 取 `configDir()` 后调新函数 `saveSetupTo(dir string, info SetupInfo) error`（内容即原 saveSetup 主体）。

`initcmd.go`：把 `RunInit` 从 `root, err := os.Getwd()` 之后的部分抽成
```go
// initProject 在 root 下写 relais/{config.toml,AGENT.md,inbox,sent,drafts}，返回 gitignore 提示。不做网络校验、不登记注册表。
func initProject(root, server, channel, username string) (string, error) {
	for _, sub := range []string{"inbox", "sent", "drafts", "conclusions"} {
		if err := os.MkdirAll(filepath.Join(root, "relais", sub), 0o755); err != nil {
			return "", err
		}
	}
	conf := fmt.Sprintf("server = %q\nchannel = %q\n", server, channel)
	if err := os.WriteFile(filepath.Join(root, "relais", "config.toml"), []byte(conf), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(root, "relais", "AGENT.md"), []byte(guide.Text(username, channel)), 0o644); err != nil {
		return "", err
	}
	return ensureGitignore(root), nil
}
```
`RunInit` 变为：HTTP `c.Members(channel)` 校验 → `os.Getwd()` → `initProject(root, cfg.Server, channel, cfg.Username)` → `registerProject(channel, root)` → 打印（文案不变，`conclusions/` 加进那行）。

`service.go` 新增通用安装：
```go
// installPlist 写并加载一个 launchd 用户代理（macOS）；其他平台返回错误。
func installPlist(label string, args []string, env map[string]string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("本地模式常驻目前只支持 macOS launchd（当前 %s）", runtime.GOOS)
	}
	home, _ := os.UserHomeDir()
	plist := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key><array>`)
	for _, a := range args {
		b.WriteString("<string>" + xmlEscape(a) + "</string>")
	}
	b.WriteString(`</array>
  <key>EnvironmentVariables</key><dict>`)
	for k, v := range env {
		b.WriteString("<key>" + xmlEscape(k) + "</key><string>" + xmlEscape(v) + "</string>")
	}
	b.WriteString(`</dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>` + xmlEscape(filepath.Join(home, "Library", "Logs", label+".log")) + `</string>
  <key>StandardErrorPath</key><string>` + xmlEscape(filepath.Join(home, "Library", "Logs", label+".log")) + `</string>
</dict></plist>`)
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(plist, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	_ = exec.Command("launchctl", "unload", plist).Run()
	if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
		return "", fmt.Errorf("加载 launchd %s 失败: %w", label, err)
	}
	return plist, nil
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
```
（import 加 `"strings"`。现有 `installService` 不动。）

- [ ] **Step 4: 实现 local.go**

```go
package cli

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"os/exec"

	"github.com/hou-physics/relais/internal/store"
)

const localHuman = "hou"

func localDir() (string, error) {
	if d := os.Getenv("RELAIS_LOCAL_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "relais-local"), nil
}

const rulesTemplate = `# 本项目铁律（两侧讨论脑每轮都读）

写具体、可判定的规矩，一行一条。例如：
- 不部署、不推送，除非负责人明确说。
- 不碰 data/app.sqlite 与本机 8000 端口。
- 术语以 CONTEXT.md 为准。
`

func RunLocal(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais local <init|status|close> ...")
	}
	switch args[0] {
	case "init":
		return runLocalInit(args[1:])
	case "status":
		return runLocalStatus()
	case "close":
		if len(args) != 2 {
			return fmt.Errorf("用法: relais local close <频道>")
		}
		return runLocalClose(args[1])
	default:
		return fmt.Errorf("未知 local 子命令 %q", args[0])
	}
}

func localServerConfigPath(ld string) string { return filepath.Join(ld, "server.toml") }

func openLocalStore() (*store.Store, *ServerConfig, string, error) {
	ld, err := localDir()
	if err != nil {
		return nil, nil, "", err
	}
	st, cfg, err := openServerStore(localServerConfigPath(ld))
	return st, cfg, ld, err
}

func runLocalInit(args []string) error {
	fs := flag.NewFlagSet("local init", flag.ContinueOnError)
	claudePath := fs.String("claude", "", "claude 可执行文件路径（默认 PATH 侦测）")
	codexPath := fs.String("codex", "", "codex 可执行文件路径（默认 PATH 侦测；本机常不在 PATH）")
	project := fs.String("project", "", "项目目录（默认当前目录）")
	listen := fs.String("listen", "127.0.0.1:8080", "本地服务器监听地址")
	noService := fs.Bool("no-service", false, "不安装 launchd 常驻（测试/手动运行用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	modules := fs.Args()
	if len(modules) == 0 {
		return fmt.Errorf("用法: relais local init [--claude <path>] [--codex <path>] [--project <dir>] [--no-service] <模块名>...")
	}
	for _, m := range modules {
		if strings.ContainsAny(m, " /\\") {
			return fmt.Errorf("模块名 %q 不能含空格或斜杠", m)
		}
	}
	root := *project
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return err
		}
	}
	root, _ = filepath.Abs(root)
	if !strings.HasPrefix(*listen, "127.0.0.1:") && !strings.HasPrefix(*listen, "localhost:") {
		return fmt.Errorf("本地模式只允许监听回环地址，得到 %q", *listen)
	}
	agents := map[string]string{"claude": *claudePath, "codex": *codexPath}
	for name, p := range agents {
		if p == "" {
			p, _ = exec.LookPath(name)
		}
		if p == "" {
			return fmt.Errorf("没找到 %s：请用 --%s <路径> 指定（Codex 常在 ~/.codex/plugins/.plugin-appserver/codex）", name, name)
		}
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			return fmt.Errorf("%s 路径 %q 不存在或不是文件", name, p)
		}
		agents[name], _ = filepath.Abs(p)
	}
	ld, err := localDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(ld, 0o700); err != nil {
		return err
	}
	// 1) server.toml
	scPath := localServerConfigPath(ld)
	baseURL := "http://" + *listen
	if _, err := os.Stat(scPath); os.IsNotExist(err) {
		sc := fmt.Sprintf("listen = %q\ndata_dir = %q\nbase_url = %q\n", *listen, filepath.Join(ld, "data"), baseURL)
		if err := os.WriteFile(scPath, []byte(sc), 0o600); err != nil {
			return err
		}
	}
	st, cfg, err := openServerStore(scPath)
	if err != nil {
		return err
	}
	defer st.Close()
	baseURL = cfg.BaseURL
	// 2) 身份
	ensureUser := func(name, display string, admin bool) (*store.User, error) {
		u, err := st.UserByName(name)
		if err == nil {
			return u, nil
		}
		pw := newPassword()
		u, err = st.CreateUser(name, display, pw)
		if err != nil {
			return nil, err
		}
		if admin {
			if err := st.SetAdmin(u.ID, true); err != nil {
				return nil, err
			}
			note := fmt.Sprintf("网页 %s 登录账号: %s\n初始密码: %s\n（本文件仅首次创建时写入；改密码后可删）\n", baseURL, name, pw)
			if err := os.WriteFile(filepath.Join(ld, "human.txt"), []byte(note), 0o600); err != nil {
				return nil, err
			}
			fmt.Print(note)
		}
		return u, nil
	}
	claudeU, err := ensureUser("claude", "Claude 侧", false)
	if err != nil {
		return err
	}
	codexU, err := ensureUser("codex", "Codex 侧", false)
	if err != nil {
		return err
	}
	humanU, err := ensureUser(localHuman, "Hou", true)
	if err != nil {
		return err
	}
	humanU, _ = st.UserByName(localHuman) // 取回 is_admin
	// 3) 两侧配置目录 + hook
	sides := map[string]*store.User{"claude": claudeU, "codex": codexU}
	for side, u := range sides {
		d := filepath.Join(ld, "sides", side)
		if err := saveGlobalTo(d, &GlobalConfig{Server: baseURL, Token: u.AgentToken, Username: side}); err != nil {
			return err
		}
		info := SetupInfo{OS: runtime.GOOS, Agent: side, AgentPath: agents[side], Mode: "auto"}
		hp, err := writeLocalHook(d, info)
		if err != nil {
			return err
		}
		info.HookPath = hp
		if err := saveSetupTo(d, info); err != nil {
			return err
		}
	}
	// 4) 频道 + 项目绑定
	defaultMode, _ := st.GetSetting("local.default_mode")
	if defaultMode == "" {
		defaultMode = "supervised"
		_ = st.SetSetting("local.default_mode", defaultMode)
	}
	for _, m := range modules {
		ch, err := st.ChannelByName(m)
		if err != nil {
			if ch, err = st.CreateChannel(m); err != nil {
				return err
			}
		}
		for _, u := range []*store.User{claudeU, codexU, humanU} {
			if ok, _ := st.IsMember(ch.ID, u.ID); !ok {
				if err := st.AddMember(ch.ID, u.ID); err != nil {
					return err
				}
			}
		}
		if a, _ := st.GetAuto(ch.ID); !a.Enabled {
			if err := st.SetAutoEnabled(ch.ID, true, 16); err != nil { // 16 条 = 8 回合（D39）
				return err
			}
			if err := st.SetMode(ch.ID, defaultMode); err != nil {
				return err
			}
		}
		for side := range sides {
			if err := registerProjectIn(filepath.Join(ld, "sides", side), m, root); err != nil {
				return err
			}
		}
	}
	// 项目目录只绑一次（默认频道 = 第一个模块；其余模块靠 RELAIS_CHANNEL）；已绑过则不覆盖
	if _, err := os.Stat(filepath.Join(root, "relais", "config.toml")); os.IsNotExist(err) {
		if _, err := initProject(root, baseURL, modules[0], "claude"); err != nil {
			return err
		}
	} else {
		for _, sub := range []string{"conclusions"} {
			os.MkdirAll(filepath.Join(root, "relais", sub), 0o755)
		}
	}
	rules := filepath.Join(root, "relais", "RULES.md")
	if _, err := os.Stat(rules); os.IsNotExist(err) {
		if err := os.WriteFile(rules, []byte(rulesTemplate), 0o644); err != nil {
			return err
		}
	}
	// 5) 常驻：serve + 两个 bridge
	if !*noService {
		relais, _ := os.Executable()
		if _, err := installPlist("com.relais.local.serve", []string{relais, "serve", "--config", scPath}, nil); err != nil {
			return err
		}
		for side := range sides {
			d := filepath.Join(ld, "sides", side)
			if _, err := installPlist("com.relais.local.bridge."+side,
				[]string{relais, "bridge", "--interval", "5", "--hook", filepath.Join(d, "hooks", "auto-reply.sh")},
				map[string]string{"RELAIS_CONFIG_DIR": d, "HOME": os.Getenv("HOME"), "PATH": os.Getenv("PATH")}); err != nil {
				return err
			}
		}
		waitListen(*listen, 5*time.Second)
	}
	fmt.Printf(`本地模式已就绪（%s）
  网页监督台: %s   （账号 %s，密码见 %s）
  模块频道: %s
  项目目录: %s   （规矩写在 relais/RULES.md）
  两侧配置: %s/sides/{claude,codex}
下一步：在工作脑里把议题写成第一封信 →
  RELAIS_CONFIG_DIR=%s/sides/<你这侧> RELAIS_CHANNEL=<模块> relais send <文件>
  （或在网页里以本人身份发第一封）
`, ld, baseURL, localHuman, filepath.Join(ld, "human.txt"), strings.Join(modules, ", "), root, ld, ld)
	if *noService {
		fmt.Printf("未安装常驻（--no-service）。手动运行：\n  relais serve --config %s\n  RELAIS_CONFIG_DIR=%s/sides/claude relais bridge --interval 5 --hook %s/sides/claude/hooks/auto-reply.sh\n  RELAIS_CONFIG_DIR=%s/sides/codex  relais bridge --interval 5 --hook %s/sides/codex/hooks/auto-reply.sh\n", scPath, ld, ld, ld, ld)
	}
	return nil
}

func waitListen(addr string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Printf("提示：%s 还没响应，launchd 可能仍在启动；稍后用 relais local status 确认。\n", addr)
}

func runLocalStatus() error {
	st, cfg, ld, err := openLocalStore()
	if err != nil {
		return fmt.Errorf("本地模式未初始化？%w", err)
	}
	defer st.Close()
	up := "未响应"
	if c, err := net.DialTimeout("tcp", cfg.Listen, 300*time.Millisecond); err == nil {
		c.Close()
		up = "在跑"
	}
	fmt.Printf("本地服务器 %s：%s（%s）\n", cfg.Listen, up, ld)
	chs, err := st.AllChannels()
	if err != nil {
		return err
	}
	for _, c := range chs {
		ch, _ := st.ChannelByName(c.Name)
		a, _ := st.GetAuto(ch.ID)
		if !a.Enabled && !a.Closed {
			continue // 非本地模式频道不显示
		}
		state := "运行"
		switch {
		case a.Closed:
			state = "已关闭"
		case a.KickedOff:
			state = "已开工"
		case a.Resolved:
			state = "已握手待确认"
		case a.NeedsHumanQ != "":
			state = "等你：" + a.NeedsHumanQ
		case a.Paused:
			state = "已暂停"
		}
		sids := ""
		for _, side := range []string{"claude", "codex"} {
			id, _ := sessionGet(filepath.Join(ld, "sides", side), c.Name)
			if id != "" {
				sids += side + "✓ "
			} else {
				sids += side + "– "
			}
		}
		fmt.Printf("  %-16s %-10s 第 %d/%d 回合  %s  会话 %s\n", c.Name, a.Mode, store.Round(a.RoundCount), store.Round(a.Cap), state, sids)
	}
	return nil
}

func runLocalClose(channel string) error {
	st, _, ld, err := openLocalStore()
	if err != nil {
		return err
	}
	defer st.Close()
	ch, err := st.ChannelByName(channel)
	if err != nil {
		return fmt.Errorf("频道 %q 不存在", channel)
	}
	if err := st.CloseChannel(ch.ID); err != nil {
		return err
	}
	for _, side := range []string{"claude", "codex"} {
		if err := sessionClear(filepath.Join(ld, "sides", side), channel); err != nil {
			return err
		}
	}
	fmt.Printf("频道 %q 已归档：循环停止、两侧讨论会话作废；消息与文件保留。\n", channel)
	return nil
}

```

- [ ] **Step 5: main.go 分发**

用法串加 `local`；加 `case "local": return cli.RunLocal(args[1:])`。

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/cli/ -v -race`
Expected: 全 PASS，含旧的 `TestRegistryUpsert`/`TestInitRegistersProject`（重构后行为不变）。

- [ ] **Step 7: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/cli main.go
git commit -m "feat(cli): relais local init/status/close 一条命令配好本机（serve+两侧身份+频道+hook+launchd）（M7 Task 6）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: web —— 结论卡片、确认开工/继续讨论、模式开关、回合显示、needs-human 回答进频道

**Files:**
- Modify: `internal/server/web/index.html`（auto-bar 加控件）
- Modify: `internal/server/web/app.js`（i18n 三语、`loadAutoState`、`renderMsg`、事件）
- Modify: `internal/server/web/style.css`（`.msg.conclusion`、`.msg.kickoff`、`.badge-owner`）
- Test: `internal/server/web_test.go`（静态资源含新 id 与三语键）；`./scripts/check.sh` 的 `node --check`

**Interfaces:**
- Consumes：Task 3 的 `AutoState` 新字段（`mode/resolved/resolution_summary/owner/kicked_off/closed/round/round_cap/needs_human_q`）、端点 `auto/kickoff`、`auto/reopen`、`auto/mode`、`Message.kind/owner`。
- 网页语义：上限输入框以**回合**为单位（提交 `cap = 2 * 输入`，显示 `round_cap`）。

- [ ] **Step 1: 写失败的测试**

`internal/server/web_test.go` 追加（该文件已有读取内嵌静态资源的写法，照其 helper；假设有 `staticBody(t, path) string`，没有就用 `webFS.ReadFile("web/"+name)`）：
```go
func TestWebHasLocalModeControls(t *testing.T) {
	html, _ := webFS.ReadFile("web/index.html")
	for _, id := range []string{`id="auto-mode"`, `id="auto-kickoff"`, `id="auto-reopen"`, `id="auto-answer"`, `id="auto-answer-send"`} {
		if !strings.Contains(string(html), id) {
			t.Fatalf("index.html 缺 %s", id)
		}
	}
	js, _ := webFS.ReadFile("web/app.js")
	for _, key := range []string{"autoResolved", "kickoff", "reopen", "modeSupervised", "modeAutopilot", "conclusionTag", "kickoffTag", "answerPh", "autoKickedOff", "autoClosed"} {
		if strings.Count(string(js), key+":") < 3 {
			t.Fatalf("app.js 三语文案缺 %s（需 zh/en/de 各一）", key)
		}
	}
	for _, s := range []string{"/auto/kickoff", "/auto/reopen", "/auto/mode", `"conclusion"`, `"kickoff"`, "round_cap"} {
		if !strings.Contains(string(js), s) {
			t.Fatalf("app.js 缺 %s", s)
		}
	}
	if strings.Contains(string(js), ".innerHTML = m.") || strings.Contains(string(js), ".innerHTML = st.") {
		t.Fatal("动态数据不得拼 innerHTML")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/ -run TestWebHasLocalModeControls -v`
Expected: FAIL `index.html 缺 id="auto-mode"`。

- [ ] **Step 3: index.html**

把 `#auto-bar` 块替换为：
```html
      <div id="auto-bar" hidden>
        <span id="auto-state" class="muted"></span>
        <span id="auto-off-ctl" hidden>
          <label data-i18n="autoCap">上限（来回数）</label>
          <input id="auto-cap" type="number" min="1" max="50" value="8">
          <button id="auto-on" type="button" data-i18n="autoEnable">开启自主对话</button>
        </span>
        <select id="auto-mode" hidden>
          <option value="supervised" data-i18n="modeSupervised">监督：握手后我确认才开工</option>
          <option value="autopilot" data-i18n="modeAutopilot">甩手：握手即开工</option>
        </select>
        <button id="auto-kickoff" type="button" hidden data-i18n="kickoff">确认开工</button>
        <button id="auto-reopen" type="button" class="ghost" hidden data-i18n="reopen">继续讨论</button>
        <button id="auto-off" type="button" class="ghost" hidden data-i18n="autoDisable">关闭自主对话</button>
        <button id="auto-pause" type="button" class="ghost" hidden data-i18n="pause">暂停</button>
        <button id="auto-resume" type="button" hidden data-i18n="resume">继续</button>
        <button id="auto-guide" type="button" class="ghost" hidden data-i18n="guideMyAgent">给我的 agent 说一句</button>
        <div id="auto-answer-row" hidden>
          <input id="auto-answer" data-i18n-placeholder="answerPh" placeholder="回答（以你本人身份发进频道，两侧都看到）">
          <button id="auto-answer-send" type="button" data-i18n="answerSend">回答并继续</button>
        </div>
      </div>
```

- [ ] **Step 4: app.js 三语文案**

在 zh 段 `guidePrompt` 行后加：
```js
    autoResolved: "✅ 已握手待确认：{s}（承接方 {o}）", autoKickedOff: "已开工（承接方 {o}）· 在工作脑里执行 relais conclusion", autoClosed: "频道已关闭",
    kickoff: "确认开工", reopen: "继续讨论", modeSupervised: "监督：握手后我确认才开工", modeAutopilot: "甩手：握手即开工",
    conclusionTag: "结论", kickoffTag: "开工", answerPh: "回答（以你本人身份发进频道，两侧都看到）", answerSend: "回答并继续",
```
en 段：
```js
    autoResolved: "✅ Handshake reached, awaiting you: {s} (owner {o})", autoKickedOff: "Kicked off (owner {o}) · run relais conclusion in your work session", autoClosed: "Channel closed",
    kickoff: "Confirm kickoff", reopen: "Keep discussing", modeSupervised: "Supervised: I confirm before kickoff", modeAutopilot: "Autopilot: kickoff on handshake",
    conclusionTag: "Conclusion", kickoffTag: "Kickoff", answerPh: "Answer (posted as you, both sides see it)", answerSend: "Answer & resume",
```
de 段：
```js
    autoResolved: "✅ Einigung erreicht, wartet auf dich: {s} (Owner {o})", autoKickedOff: "Gestartet (Owner {o}) · relais conclusion in deiner Arbeitssitzung", autoClosed: "Kanal geschlossen",
    kickoff: "Start bestätigen", reopen: "Weiter diskutieren", modeSupervised: "Beaufsichtigt: ich bestätige vor dem Start", modeAutopilot: "Autopilot: Start bei Einigung",
    conclusionTag: "Fazit", kickoffTag: "Start", answerPh: "Antwort (als du selbst, beide Seiten sehen sie)", answerSend: "Antworten & fortsetzen",
```
zh/en/de 里现有 `autoCap` 文案分别改为 "上限（来回数）" / "Round cap (exchanges)" / "Rundenlimit (Wechsel)"；`autoRunning` 改为 "自主对话中（第 {n}/{cap} 回合）" / "Auto-chat running (round {n}/{cap})" / "Auto-Chat läuft (Runde {n}/{cap})"。

- [ ] **Step 5: app.js `loadAutoState` 与事件**

把 `loadAutoState` 整个替换为：
```js
async function loadAutoState() {
  const bar = $("auto-bar");
  let st;
  try { st = await api("/api/channels/" + encodeURIComponent(channel) + "/auto"); }
  catch { bar.hidden = true; return; }
  bar.hidden = false;
  const on = !!st.enabled;
  if (on) $("auto-cap").value = st.round_cap;
  $("auto-off-ctl").hidden = on;
  $("auto-off").hidden = !on;
  $("auto-guide").hidden = !on;
  $("auto-mode").hidden = !on;
  if (on) $("auto-mode").value = st.mode || "supervised";
  $("auto-kickoff").hidden = !on || !st.resolved;
  $("auto-reopen").hidden = !on || !st.resolved;
  $("auto-pause").hidden = !on || st.paused || st.resolved;
  $("auto-resume").hidden = !on || !st.paused || st.resolved || !!st.needs_human_q;
  $("auto-answer-row").hidden = !on || !st.needs_human_q;
  const state = $("auto-state");
  if (!on) { state.textContent = t("autoOff"); state.className = "muted"; return; }
  let text = t("autoRunning").replace("{n}", st.round).replace("{cap}", st.round_cap);
  let cls = "muted";
  if (st.closed) { text = t("autoClosed"); }
  else if (st.resolved) { text = t("autoResolved").replace("{s}", st.resolution_summary || "").replace("{o}", st.owner || ""); cls = "ok"; }
  else if (st.needs_human_q) { text = "⚠️ " + t("autoNeedsYou") + " " + st.needs_human_q; cls = "err"; }
  else if (st.kicked_off) { text = t("autoKickedOff").replace("{o}", st.owner || ""); cls = "ok"; }
  else if (st.paused) { text = t("autoPaused"); cls = "err"; }
  state.textContent = text;
  state.className = cls;
}
```
`auto-on` / `auto-off` 两个监听里 `const cap = parseInt($("auto-cap").value, 10) || 6;` 改为 `const cap = 2 * (parseInt($("auto-cap").value, 10) || 8);`。在 `auto-guide` 监听之后追加：
```js
$("auto-mode").addEventListener("change", async () => {
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto/mode", { method: "POST", body: JSON.stringify({ mode: $("auto-mode").value }) });
  loadAutoState();
});
$("auto-kickoff").addEventListener("click", async () => {
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto/kickoff", { method: "POST" });
  loadAutoState(); refresh();
});
$("auto-reopen").addEventListener("click", async () => {
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto/reopen", { method: "POST" });
  loadAutoState();
});
$("auto-answer-send").addEventListener("click", async () => {
  const text = $("auto-answer").value.trim();
  if (!text) return;
  const to = members.filter((m) => m.username !== me.username).map((m) => m.username);
  await api("/api/channels/" + encodeURIComponent(channel) + "/messages", {
    method: "POST", body: JSON.stringify({ to, summary: text.slice(0, 80), body_md: text }),
  });
  await api("/api/channels/" + encodeURIComponent(channel) + "/auto/resume", { method: "POST" });
  $("auto-answer").value = "";
  loadAutoState(); refresh();
});
```
（`members`、`me` 是文件里已有的全局变量，`renderToRow` 已在用。）

- [ ] **Step 6: app.js `renderMsg` 结论/开工卡片**

在 `div.dataset.id = m.id;` 之后加：
```js
  if (m.kind === "conclusion" || m.kind === "kickoff") {
    div.classList.add(m.kind);
    const tag = document.createElement("span");
    tag.className = "kind-tag";
    tag.textContent = (m.kind === "conclusion" ? t("conclusionTag") : t("kickoffTag")) + (m.owner ? " · " + m.owner : "");
    div.append(tag);
  }
```
`style.css` 追加：
```css
.msg.conclusion { border-left: 3px solid var(--ok); background: color-mix(in srgb, var(--ok) 6%, var(--card)); }
.msg.kickoff { border-left: 3px solid var(--accent); }
.msg .kind-tag { display: inline-block; font-size: 11px; font-weight: 600; color: var(--ok); margin-bottom: 4px; }
.msg.kickoff .kind-tag { color: var(--accent); }
#auto-answer-row { display: flex; gap: 6px; width: 100%; margin-top: 6px; }
#auto-answer-row input { flex: 1; }
#auto-mode { font-size: 12px; }
```
（`--ok`、`--accent`、`--card` 是 style.css 顶部已有的变量；若没有 `--ok` 就用现有 `.ok` 里的颜色值。）

- [ ] **Step 7: 跑测试与地板**

Run: `go test ./internal/server/ -run TestWeb -v && ./scripts/check.sh`
Expected: PASS，`node --check` 通过。

手工冒烟（可选但推荐）：`go run . serve --config <本地 server.toml>` 后浏览器登录 hou，进一个频道，确认状态条显示"第 0/8 回合"、模式下拉可切换、needs-human 时出现回答框。

- [ ] **Step 8: 提交**

```bash
git add internal/server/web internal/server/web_test.go
git commit -m "feat(web): 结论卡片 + 确认开工/继续讨论 + 模式开关 + 回合显示 + needs-human 回答进频道（M7 Task 7）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: e2e 锚点、版本号、文档、D42

**Files:**
- Modify: `e2e/e2e_test.go`（新增 `TestAnchorM7Handshake`、`TestAnchorM7KeyIsolation`、`TestAnchorM7Bridge`）
- Modify: `main.go`、`internal/server/static.go`（version → `0.5.0-m7`）
- Modify: `README.md`（本地模式一节）、`docs/decisions.md`（D42 执行期偏离）
- Modify: `internal/cli/doctor.go`（可选：识别 `RELAIS_LOCAL_DIR`；不做也不阻塞）

**Interfaces:** 只消费前面任务的公开行为。

- [ ] **Step 1: 写锚点（先写、先红）**

`e2e/e2e_test.go` 追加：
```go
func TestAnchorM7Handshake(t *testing.T) {
	w := newWorld(t)
	duo, _ := w.st.ChannelByName("duo")
	w.st.SetAutoEnabled(duo.ID, true, 16)

	// hou 的讨论脑提议 RESOLVED（frontmatter 走 CLI）
	houProj := w.actAs(t, "hou", "duo")
	md := filepath.Join(houProj, "r1.md")
	os.WriteFile(md, []byte("---\nowner: codex\nowner_reason: 词对线一直是 codex 审\n---\n\n结论 X"), 0o644)
	if err := cli.RunSend([]string{"--kind", "resolved", "--summary", "谈拢了：X", "--idempotency-key", "h1", md}); err != nil {
		t.Fatalf("锚点M7-1 提议失败: %v", err)
	}
	// 同键重发 → 只落一条（幂等）
	cli.RunSend([]string{"--kind", "resolved", "--summary", "谈拢了：X", "--idempotency-key", "h1", md})
	msgs, _ := w.st.ListEnvelopes(duo.ID, w.users["wu"].ID, false, false)
	if len(msgs) != 1 || msgs[0].Seq != 1 {
		t.Fatalf("锚点M7-1 幂等失败: %+v", msgs)
	}
	m1 := msgs[0]
	a, _ := w.st.GetAuto(duo.ID)
	if a.Resolved || !a.InFlight {
		t.Fatalf("锚点M7-1 单边不应握手且应在途: %+v", a)
	}
	// wu 侧：bridge 落盘（标已读）→ 在途解除 → 附和
	wuProj := w.actAs(t, "wu", "duo")
	c, _ := cli.NewClientForTest()
	if n, err := cli.PollOnceForTest(c, "duo", wuProj); err != nil || n != 1 {
		t.Fatalf("锚点M7-1 wu 拉取: %d %v", n, err)
	}
	ack := filepath.Join(wuProj, "ack.md")
	os.WriteFile(ack, []byte("---\nowner: codex\nowner_reason: 同意\nack_of: "+m1.ID+"\n---\n\n结论 X（最终措辞）"), 0o644)
	if err := cli.RunSend([]string{"--kind", "resolved", "--summary", "同意 X", ack}); err != nil {
		t.Fatalf("锚点M7-1 附和失败: %v", err)
	}
	a, _ = w.st.GetAuto(duo.ID)
	if !a.Resolved || !a.Paused || a.Mode != "supervised" {
		t.Fatalf("锚点M7-1 握手后应 resolved+paused: %+v", a)
	}
	if ok, _, _ := w.st.RequestTurn(duo.ID); ok {
		t.Fatal("锚点M7-1 握手后 turn 必须拒")
	}
	// 监督模式：agent token 不能 kickoff；人钥匙可以
	req, _ := http.NewRequest("POST", w.ts.URL+"/api/channels/duo/auto/kickoff", nil)
	req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("锚点M7-1 agent kickoff 必须 403, got %d", resp.StatusCode)
	}
	if _, err := w.st.Kickoff(duo.ID, w.users["hou"].ID); err != nil { // 等价于人钥匙端点（HTTP 版见 server 测试）
		t.Fatal(err)
	}
	// 承接方侧 bridge 拉到 kickoff → conclusions/，不进 inbox
	if n, err := cli.PollOnceForTest(c, "duo", wuProj); err != nil || n != 1 {
		t.Fatalf("锚点M7-1 kickoff 拉取: %d %v", n, err)
	}
	concl, _ := os.ReadDir(filepath.Join(wuProj, "relais", "conclusions"))
	if len(concl) != 1 {
		t.Fatalf("锚点M7-1 应落 1 份结论: %v", concl)
	}
	data, _ := os.ReadFile(filepath.Join(wuProj, "relais", "conclusions", concl[0].Name()))
	if !strings.Contains(string(data), "最终措辞") || !strings.Contains(string(data), "owner: codex") {
		t.Fatalf("锚点M7-1 结论内容错: %s", data)
	}
	if err := cli.RunConclusion(nil); err != nil {
		t.Fatal(err)
	}
	// 现实中 hou 侧 bridge 早已拉走 wu 的附和（标已读、conclusion 不跑 hook）；e2e 里手动补这一步，否则在途检查会拦新议题
	if l, _ := w.st.ListEnvelopes(duo.ID, w.users["hou"].ID, true, true); len(l) > 0 {
		for _, um := range l {
			w.st.MarkRead(um.ID, w.users["hou"].ID)
		}
	}
	// 开工后频道回到空闲：可开新议题，且新议题清 kicked_off
	a, _ = w.st.GetAuto(duo.ID)
	if a.Resolved || a.Paused || !a.KickedOff {
		t.Fatalf("锚点M7-1 开工后状态: %+v", a)
	}
	os.WriteFile(ack, []byte("新议题"), 0o644)
	if err := cli.RunSend([]string{"--summary", "新议题", ack}); err != nil {
		t.Fatalf("锚点M7-1 开工后应能开新议题: %v", err)
	}
	a, _ = w.st.GetAuto(duo.ID)
	if a.KickedOff {
		t.Fatal("锚点M7-1 新议题应清 kicked_off")
	}
}

func TestAnchorM7OwnerConflictAndCap(t *testing.T) {
	w := newWorld(t)
	duo, _ := w.st.ChannelByName("duo")
	w.st.SetAutoEnabled(duo.ID, true, 16)
	hou, wu := w.users["hou"], w.users["wu"]
	m1, _ := w.st.SaveMessageOpts(duo.ID, hou.ID, []int64{wu.ID}, "s", "X", "", store.SaveOpts{Kind: "resolved", Owner: "claude", OwnerReason: "我熟"})
	m2, _ := w.st.SaveMessageOpts(duo.ID, wu.ID, []int64{hou.ID}, "s", "X", "", store.SaveOpts{Kind: "resolved", Owner: "codex", OwnerReason: "我先做的", AckOf: m1.ID})
	if r, _ := w.st.EvaluateHandshake(duo.ID, m2.ID); r != store.HandshakeOwnerConflict {
		t.Fatalf("锚点M7-2 owner 分歧应升级: %v", r)
	}
	a, _ := w.st.GetAuto(duo.ID)
	if a.Resolved || !strings.Contains(a.NeedsHumanQ, "承接方分歧") {
		t.Fatalf("锚点M7-2: %+v", a)
	}
	// 人回答后 resume → 可继续；上限 2 条 → 第 3 次 turn 转人并摆立场
	w.st.Reopen(duo.ID)
	w.st.SetAutoEnabled(duo.ID, true, 2)
	w.st.RequestTurn(duo.ID)
	w.st.RequestTurn(duo.ID)
	if ok, _, _ := w.st.RequestTurn(duo.ID); ok {
		t.Fatal("锚点M7-2 到上限应拒")
	}
	a, _ = w.st.GetAuto(duo.ID)
	if !strings.Contains(a.NeedsHumanQ, "回合上限") {
		t.Fatalf("锚点M7-2 到上限应转人: %q", a.NeedsHumanQ)
	}
	// 关闭后一律拒
	w.st.Reopen(duo.ID)
	w.st.CloseChannel(duo.ID)
	if ok, reason, _ := w.st.RequestTurn(duo.ID); ok || !strings.Contains(reason, "关闭") {
		t.Fatal("锚点M7-2 关闭后 turn 必须拒")
	}
}

func TestAnchorM7AutopilotAndKeyIsolation(t *testing.T) {
	w := newWorld(t)
	duo, _ := w.st.ChannelByName("duo")
	w.st.SetAutoEnabled(duo.ID, true, 16)
	w.st.SetMode(duo.ID, "autopilot")
	hou, wu := w.users["hou"], w.users["wu"]
	// 走 HTTP：wu 的 agent 附和 → 服务器自动 kickoff
	m1, _ := w.st.SaveMessageOpts(duo.ID, hou.ID, []int64{wu.ID}, "s", "X", "", store.SaveOpts{Kind: "resolved", Owner: "codex"})
	body, _ := json.Marshal(map[string]any{"to": []string{"hou"}, "summary": "同意", "body_md": "X", "kind": "resolved", "owner": "codex", "ack_of": m1.ID})
	req, _ := http.NewRequest("POST", w.ts.URL+"/api/channels/duo/messages", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+wu.AgentToken)
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("锚点M7-3 附和应 200, got %d", resp.StatusCode)
	}
	a, _ := w.st.GetAuto(duo.ID)
	if !a.KickedOff || a.Resolved {
		t.Fatalf("锚点M7-3 甩手模式握手即开工: %+v", a)
	}
	// 钥匙隔离：agent token 打 reopen/mode → 403
	for _, p := range []string{"/auto/reopen", "/auto/mode"} {
		r, _ := http.NewRequest("POST", w.ts.URL+"/api/channels/duo"+p, strings.NewReader(`{"mode":"supervised"}`))
		r.Header.Set("Authorization", "Bearer "+hou.AgentToken)
		r.Header.Set("Content-Type", "application/json")
		rs, _ := http.DefaultClient.Do(r)
		if rs.StatusCode != 403 {
			t.Fatalf("锚点M7-3 agent %s 必须 403, got %d", p, rs.StatusCode)
		}
	}
	// 客户端不得自设 kickoff/conclusion
	bad, _ := json.Marshal(map[string]any{"to": []string{"hou"}, "summary": "s", "body_md": "b", "kind": "kickoff"})
	r2, _ := http.NewRequest("POST", w.ts.URL+"/api/channels/duo/messages", bytes.NewReader(bad))
	r2.Header.Set("Authorization", "Bearer "+wu.AgentToken)
	r2.Header.Set("Content-Type", "application/json")
	rs2, _ := http.DefaultClient.Do(r2)
	if rs2.StatusCode != 400 {
		t.Fatalf("锚点M7-3 kind=kickoff 必须 400, got %d", rs2.StatusCode)
	}
}
```
（`e2e_test.go` 已 import `bytes`、`json`、`http`、`strings`、`store`。）

- [ ] **Step 2: 跑锚点确认通过**

Run: `go test ./e2e/ -v -race`
Expected: 全 PASS（含 M1–M5 旧锚点）。若失败，修的是前面任务的代码，不改锚点断言。

- [ ] **Step 3: 版本号**

`main.go` `const version = "0.5.0-m7"`；`internal/server/static.go` `const Version = "0.5.0-m7"`。跑 `go test ./internal/server/ -run Static` 确认 ETag 测试仍绿。

- [ ] **Step 4: README 与 D42**

`README.md` 加一节「本地单人模式（M7）」：三行命令（`relais local init --codex ~/.codex/plugins/.plugin-appserver/codex grammar`、`RELAIS_CONFIG_DIR=…/sides/claude relais send 第一封.md`、`relais conclusion`）+ 一段"人只在开题 / needs-human / 确认开工三处出面"。

`docs/decisions.md` 顶部追加：
```markdown
## 2026-09-26 · D42 M7 执行期实现选择（对照 spec 的三处）

- **问题**：M7 落地时三处实现与 spec 文字不完全一致，记档防后人"修正"。
- **考虑过**：严格照 spec §7.2 把服务器 cap 改成回合单位——被否：会改动 M5 `RequestTurn` 语义与锚点；照 §6.3 kickoff 只发承接方——被否：`owner=user` 时要发两侧，统一发全体更简单且两侧都能 `relais conclusion`。
- **选择**：① 服务器 `cap`/`round_count` 仍按条计，`relais local init` 设 `cap=16`（=8 回合），网页与 CLI 显示 `ceil(n/2)`、网页输入框按回合换算 ×2；② kickoff 消息收件人 = 频道全部成员，落到每一侧的 `relais/conclusions/`；③ RESOLVED 信号 = 首行 `RESOLVED: <一句>` + frontmatter（owner/owner_reason/ack_of），hook 转成 `relais send --kind resolved --summary`；④ 在途拒绝只对 `auto.enabled` 的频道生效（保 M1 锚点"连发两封"行为）；⑤ spec §7.2"再放 N 轮"不做数字框：人回答后点继续即 `resume`（round 归零 = 再放一整个上限）；⑥ 一个项目目录承载多个模块：`relais/config.toml` 只记默认频道，bridge 给 hook 传 `RELAIS_CHANNEL`，工作脑开题也用它指定模块。
- **状态**：live（v0.5.0-m7）。
- **反转触发**：若联网频道也要回合语义 → 届时统一改服务器单位并迁移 cap。
```

- [ ] **Step 5: 全量地板 + 真实冒烟**

```bash
./scripts/check.sh
```
真实冒烟（本机，按 spec §12 最后一段）：
```bash
go build -o /tmp/relais . && /tmp/relais local init --claude "$(which claude)" --codex ~/.codex/plugins/.plugin-appserver/codex --project /tmp/relais-smoke smoke
```
然后在 `/tmp/relais-smoke` 写第一封 `open.md`（frontmatter `summary: 缓存用 Redis 还是 Memcached，谁来做`，正文两行），`RELAIS_CONFIG_DIR="$HOME/Library/Application Support/relais-local/sides/claude" /tmp/relais send open.md`；观察 `~/Library/Logs/com.relais.local.bridge.codex.log` 与网页 `http://127.0.0.1:8080`；期待：codex 侧回信 → claude 侧续会话回 → 数轮内出现 RESOLVED 握手 → 状态条绿色"已握手待确认" → 点"确认开工" → `/tmp/relais-smoke/relais/conclusions/smoke-*.md` 出现 → `relais conclusion` 打印。记录结果到本任务提交信息；冒烟不过就是 bug，回到对应任务修。

- [ ] **Step 6: 提交**

```bash
git add e2e main.go internal/server/static.go README.md docs/decisions.md
git commit -m "test(e2e): M7 锚点（握手/承接方分歧/上限转人/甩手开工/钥匙隔离/结论落盘）；version 0.5.0-m7；D42

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

## 执行顺序与依赖

Task 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8，严格串行（每个任务都依赖前一个的接口）。Task 7 只依赖 Task 3，可与 4–6 并行，但为了 check.sh 稳定建议仍串行。

## 交付后

按 [[feedback-milestone-push]] 规则：M7 全绿 + 冒烟通过 = 里程碑完成，合并 main 并 push `github.com/hou-physics/relais`，打标签 `v0.5.0-m7`。线上 relais-ai.com **不部署**（D35：本地模式不动联网层，但代码同仓；部署与否由 Hou 另定）。
