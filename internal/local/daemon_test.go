package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/store"
)

type fixture struct {
	st    *store.Store
	proj  string
	md    string
	chID  int64
	d     *Daemon
	codex string // 假 codex 脚本记录 argv 的日志文件
	notes []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	users := map[string]int64{}
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.CreateUser(n, n, "pw")
		users[n] = u.ID
	}
	ch, _ := st.CreateChannel("m")
	for _, id := range users {
		st.AddMember(ch.ID, id)
	}
	st.SetAutoEnabled(ch.ID, true, 16)
	proj := t.TempDir()
	EnsureMailDir(proj, "m")
	st.UpsertLocalModule(ch.ID, proj)
	log := filepath.Join(t.TempDir(), "codex.log")
	script := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+log+"\"\nif [ \"$FAKE_CODEX_FAIL\" = 1 ]; then echo boom >&2; exit 3; fi\n"), 0o755)
	f := &fixture{st: st, proj: proj, md: MailDir(proj, "m"), chID: ch.ID, codex: log}
	f.d = &Daemon{Store: st, Users: users,
		Modules:   func() ([]ModuleRef, error) { return []ModuleRef{{ChannelID: ch.ID, Name: "m", Dir: proj}}, nil },
		CodexPath: func() string { return script },
		Notify:    func(title, body string) { f.notes = append(f.notes, title+"|"+body) },
	}
	return f
}

func (f *fixture) post(t *testing.T, side, body string, o PostOpts) {
	t.Helper()
	draft := filepath.Join(f.md, "drafts", side+".md")
	os.WriteFile(draft, []byte(body), 0o644)
	if _, err := Post(f.md, side, draft, o); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonIngestsArchivesDelivers(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", Name: "对话", At: time.Now()})
	f.post(t, "claude", "# 第一封\n\n你好\n", PostOpts{})
	if err := f.d.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.md, "outbox")); len(entries) != 0 {
		t.Fatal("入库后 outbox 应清空")
	}
	data, err := os.ReadFile(filepath.Join(f.md, "001-claude.md"))
	if err != nil {
		t.Fatal("应归档为 001-claude.md")
	}
	l, body, _ := ParseLetter(data)
	if l.Seq != 1 || l.From != "claude" || l.Kind != "letter" || l.Module != "m" || l.ID == "" || l.Summary != "第一封" || body != "# 第一封\n\n你好\n" {
		t.Fatalf("归档信封: %+v %q", l, body)
	}
	argv, _ := os.ReadFile(f.codex)
	s := string(argv)
	if !strings.Contains(s, "queue\n--thread\nt-1\n--message\n") || !strings.Contains(s, "Relais 模块「m」") || !strings.Contains(s, "001-claude.md") || !strings.Contains(s, "PROTOCOL.md") {
		t.Fatalf("codex queue 参数: %q", s)
	}
	m, _ := f.st.LocalModuleByName("m")
	if m.ArchivedSeq != 1 || m.CodexThreadID != "t-1" || m.CodexDeliveryError != "" {
		t.Fatalf("登记表: %+v", m)
	}
	if id, status, _, _, _ := f.st.LastDelivery(f.chID, "codex"); id != l.ID || status != "ok" {
		t.Fatalf("投递记录: %s %s", id, status)
	}
	if a, _ := f.st.GetAuto(f.chID); a.RoundCount != 1 {
		t.Fatalf("agent 信应计回合: %d", a.RoundCount)
	}
	// 幂等：再跑一轮不重复归档、不重复投递
	f.d.RunOnce()
	if argv2, _ := os.ReadFile(f.codex); string(argv2) != s {
		t.Fatal("不该重复投递")
	}
}

func TestDaemonCodexNotAttachedAndFailure(t *testing.T) {
	f := newFixture(t)
	f.post(t, "claude", "x\n", PostOpts{})
	f.d.RunOnce()
	m, _ := f.st.LocalModuleByName("m")
	if !strings.Contains(m.CodexDeliveryError, "未接入") {
		t.Fatalf("未接入应记错误: %+v", m)
	}
	if len(f.notes) == 0 {
		t.Fatal("投递失败应通知")
	}
	WriteAttach(f.md, Attach{ThreadID: "t-1", Name: "对话", At: time.Now()})
	t.Setenv("FAKE_CODEX_FAIL", "1")
	if err := f.d.Redeliver(f.chID); err == nil {
		t.Fatal("codex 退出非零应报错")
	}
	m, _ = f.st.LocalModuleByName("m")
	if !strings.Contains(m.CodexDeliveryError, "boom") {
		t.Fatalf("应记 stderr: %q", m.CodexDeliveryError)
	}
	t.Setenv("FAKE_CODEX_FAIL", "0")
	if err := f.d.Redeliver(f.chID); err != nil {
		t.Fatal(err)
	}
	m, _ = f.st.LocalModuleByName("m")
	if m.CodexDeliveryError != "" {
		t.Fatal("重投成功应清错误")
	}
}

func TestDaemonHandshakeConclusionKickoffAndWait(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.st.SetMode(f.chID, "autopilot")
	f.post(t, "codex", "提议\n", PostOpts{Resolved: true, Owner: "codex"})
	f.d.RunOnce()
	f.post(t, "claude", "附和\n", PostOpts{Ack: true})
	f.d.RunOnce()
	for _, name := range []string{"001-codex.md", "002-claude.md", "conclusion-002.md", "kickoff-002.md"} {
		if _, err := os.Stat(filepath.Join(f.md, name)); err != nil {
			t.Fatalf("缺 %s", name)
		}
	}
	data, _ := os.ReadFile(filepath.Join(f.md, "002-claude.md"))
	l, _, _ := ParseLetter(data)
	if l.Kind != "conclusion" || l.AckOf != 1 || l.From != "claude" || l.Owner != "codex" {
		t.Fatalf("结论信: %+v", l)
	}
	data, _ = os.ReadFile(filepath.Join(f.md, "kickoff-002.md"))
	l, _, _ = ParseLetter(data)
	if l.Kind != "kickoff" || l.From != "relais" || l.Owner != "codex" || l.Seq != 2 {
		t.Fatalf("kickoff 文件: %+v", l)
	}
	argv, _ := os.ReadFile(f.codex)
	if !strings.Contains(string(argv), "你是承接方") || !strings.Contains(string(argv), "conclusion-002.md") {
		t.Fatalf("codex 应收到开工通知: %q", argv)
	}
	// 终审修复补：甩手模式下 kickoff 与结论同一秒建，Codex 也必须先收到附和、再收到开工
	if i, j := strings.Index(string(argv), "这是对方的附和"), strings.Index(string(argv), "你是承接方"); i < 0 || j < i {
		t.Fatalf("应先投附和（002）再投开工: %q", argv)
	}
	// claude 侧 wait 立即拿到附和之后的新东西：kickoff（001 是对方的、002 是自己的）
	ls, err := Wait(nil, f.md, "claude", 10*time.Millisecond, 0, "")
	if err != nil || len(ls) != 2 || ls[0].Seq != 1 || ls[1].Kind != "kickoff" {
		t.Fatalf("wait: %+v %v", ls, err)
	}
	f.d.RunOnce() // 幂等：kickoff 不重复归档
	if entries, _ := filepath.Glob(filepath.Join(f.md, "kickoff-*")); len(entries) != 1 {
		t.Fatal("kickoff 只归档一次")
	}
}

func TestDaemonNeedsHumanRejectedAndHumanLetter(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.post(t, "codex", "要不要上 Redis？\n", PostOpts{NeedsHuman: true})
	f.d.RunOnce()
	if a, _ := f.st.GetAuto(f.chID); a.NeedsHumanQ != "要不要上 Redis？" || !a.Paused {
		t.Fatalf("needs-human 应置问题并暂停: %+v", a)
	}
	os.WriteFile(filepath.Join(f.md, "outbox", "claude-bad.md"), []byte("没头"), 0o644)
	f.d.RunOnce()
	if _, err := os.Stat(filepath.Join(f.md, "outbox", "claude-bad.md.rejected")); err != nil {
		t.Fatal("坏文件应改名 .rejected")
	}
	// 雇主在控制台写信（直接入库）→ 归档 003-hou.md 并投给 codex，通知里说明由谁先回
	users := f.d.Users
	f.st.SaveMessage(f.chID, users["hou"], []int64{users["claude"], users["codex"]}, "上", "@codex 先回\n\n上 Redis", "")
	f.d.RunOnce()
	if _, err := os.Stat(filepath.Join(f.md, "002-hou.md")); err != nil {
		t.Fatal("雇主信应归档 002-hou.md")
	}
	argv, _ := os.ReadFile(f.codex)
	if !strings.Contains(string(argv), "002-hou.md") || !strings.Contains(string(argv), "由你先回") {
		t.Fatalf("雇主信投递: %q", argv)
	}
}

func TestReadSideStatus(t *testing.T) {
	_, md := setupMail(t)
	s := ReadSideStatus(md, func(int) bool { return true })
	if s.ClaudeWaiting || s.CodexAttached {
		t.Fatal("初始都未接入")
	}
	os.WriteFile(filepath.Join(md, ".wait-claude"), []byte(`{"pid":42,"since":"2026-09-27T10:00:00Z","session_id":"s"}`), 0o644)
	WriteCursor(md, "claude", Cursor{Seq: 3})
	WriteAttach(md, Attach{ThreadID: "t", Name: "n", At: time.Now()})
	s = ReadSideStatus(md, func(pid int) bool { return pid == 42 })
	if !s.ClaudeWaiting || s.ClaudeSessionID != "s" || s.ClaudeCursor != 3 || !s.CodexAttached || s.CodexThreadName != "n" {
		t.Fatalf("status: %+v", s)
	}
	s = ReadSideStatus(md, func(int) bool { return false })
	if s.ClaudeWaiting {
		t.Fatal("pid 死了不算在等")
	}
}

// TestDaemonNotifyEveryLetter：控制者裁决 1——NotifyEveryLetter 打开时，每归档一封非
// kickoff、非 relais 发件的信都应通知一次，文案含"第 N 封"。
func TestDaemonNotifyEveryLetter(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.d.NotifyEveryLetter = func() bool { return true }
	f.post(t, "claude", "第一封\n", PostOpts{})
	if err := f.d.RunOnce(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range f.notes {
		if strings.Contains(n, "第 1 封") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应通知每封信，未见含“第 1 封”的通知: %v", f.notes)
	}
}

// --- 修复轮 1（code review 发现）---

// F1：syncAttach 不该在没有新 attach 的每一轮都把 codex_delivery_error 清掉。
// DB 存 codex_attached_at 是秒精度，.attach-codex 的 At 若不截到秒比较，
// a.At.After(prev) 会永远为真。
func TestDaemonSyncAttachDoesNotWipeDeliveryError(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", Name: "对话", At: time.Now()})
	t.Setenv("FAKE_CODEX_FAIL", "1")
	f.post(t, "claude", "x\n", PostOpts{})
	f.d.RunOnce()
	m, _ := f.st.LocalModuleByName("m")
	if !strings.Contains(m.CodexDeliveryError, "boom") {
		t.Fatalf("首次投递失败应记 boom: %+v", m)
	}
	// 再跑一轮：没有新的 outbox/归档，唯一起作用的是 syncAttach；attach 文件没变，
	// 不该重新 SetCodexAttach，更不该顺带清掉上面记的错误。
	f.d.RunOnce()
	m, _ = f.st.LocalModuleByName("m")
	if !strings.Contains(m.CodexDeliveryError, "boom") {
		t.Fatalf("没有新 attach 时不该清掉投递错误: %+v", m)
	}
}

// F2：outbox 文件的删除挪到所有副作用之后；重放（同一 IdemKey 再次入库，因为上一轮
// 后续步骤失败导致文件没删掉）时不应重复计回合、不应产生第二条消息。
func TestDaemonIngestReplaySkipsTurnCountAndDedups(t *testing.T) {
	f := newFixture(t)
	draft := filepath.Join(f.md, "drafts", "claude.md")
	os.WriteFile(draft, []byte("x\n"), 0o644)
	outPath, err := Post(f.md, "claude", draft, PostOpts{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	f.d.RunOnce()
	if a, _ := f.st.GetAuto(f.chID); a.RoundCount != 1 {
		t.Fatalf("首次入库应计 1 回合: %d", a.RoundCount)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatal("首次入库成功应清空 outbox 文件")
	}
	notesBefore := len(f.notes)
	// 手工重建同名 outbox 文件，模拟"上一轮某个后续步骤失败、文件没被删"的场景
	if err := os.WriteFile(outPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	f.d.RunOnce()
	if a, _ := f.st.GetAuto(f.chID); a.RoundCount != 1 {
		t.Fatalf("重放不该再计回合: %d", a.RoundCount)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatal("重放处理完也该清空 outbox 文件")
	}
	// 修复轮 2：重放不该再产生任何新通知（needs-human/OwnerConflict/回合上限都一样，
	// 跟 CountLocalTurn 共用 !replay 这套门）。
	if len(f.notes) != notesBefore {
		t.Fatalf("重放不该产生新通知: 之前 %d 条，之后 %d 条: %v", notesBefore, len(f.notes), f.notes)
	}
	msgs, err := f.st.ListAfterSeq(f.chID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("重放不该重复入库，应仍只有 1 条: %d", len(msgs))
	}
}

// F3：未知发件人/未知 kind/ack_of 找不到，跟 ScanOutbox 的解析错误一样处理：
// 改名 .rejected、写 .rejected.txt、通知一次，不再永久卡住重试。这里用 outbox 里
// from 既不是 claude 也不是 codex 的文件触发（ScanOutbox 层面就会拒收）。
func TestDaemonIngestRejectsUnknownSender(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.md, "outbox", "kimi-x.md")
	if err := os.WriteFile(p, RenderLetter(Letter{From: "kimi", Kind: "letter", Summary: "s"}, "正文\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.d.RunOnce()
	if _, err := os.Stat(p + ".rejected"); err != nil {
		t.Fatal("未知发件人应改名 .rejected")
	}
	data, err := os.ReadFile(p + ".rejected.txt")
	if err != nil || !strings.Contains(string(data), "发件人") {
		t.Fatalf("原因应提到“发件人”: %q %v", data, err)
	}
	found := false
	for _, n := range f.notes {
		if strings.Contains(n, "发件人") {
			found = true
		}
	}
	if !found {
		t.Fatalf("应通知一次: %v", f.notes)
	}
}

// F4：只投给信封 to 里点了名的一侧；雇主只写给 claude 的悄悄话不该被塞进 Codex 对话。
func TestDaemonSkipsCodexWhenNotAddressed(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.post(t, "claude", "第一封\n", PostOpts{})
	f.d.RunOnce()
	before, err := os.ReadFile(f.codex)
	if err != nil {
		t.Fatal(err)
	}
	users := f.d.Users
	f.st.SaveMessage(f.chID, users["hou"], []int64{users["claude"]}, "仅抄送 claude", "私下说一句\n", "")
	f.d.RunOnce()
	if _, err := os.Stat(filepath.Join(f.md, "002-hou.md")); err != nil {
		t.Fatal("应归档 002-hou.md")
	}
	after, err := os.ReadFile(f.codex)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("未指名 codex 的信不该投给 codex: %q -> %q", before, after)
	}
}

// 修复轮 2 Important：Redeliver 挑投递目标要跟 deliver() 用同一条 to 规则，不能再按
// From != "codex" 挑——那样会把雇主只写给 claude 的悄悄话也当成该重投给 codex 的目标。
func TestDaemonRedeliverRespectsAddressing(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.post(t, "claude", "第一封\n", PostOpts{})
	t.Setenv("FAKE_CODEX_FAIL", "1")
	f.d.RunOnce() // 001-claude.md 自动投给 codex 一次，失败，留在待投集合里
	t.Setenv("FAKE_CODEX_FAIL", "0")
	users := f.d.Users
	f.st.SaveMessage(f.chID, users["hou"], []int64{users["claude"]}, "仅抄送 claude", "私下说一句\n", "")
	f.d.RunOnce() // 归档 002-hou.md，但 to=[claude]，deliver() 不该投给 codex
	if _, err := os.Stat(filepath.Join(f.md, "002-hou.md")); err != nil {
		t.Fatal("应归档 002-hou.md")
	}
	before, err := os.ReadFile(f.codex)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.d.Redeliver(f.chID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(f.codex)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(after), "002-hou.md") != 0 {
		t.Fatalf("不该把只写给 claude 的悄悄话重投给 codex: %q", after)
	}
	if strings.Count(string(after), "001-claude.md") != strings.Count(string(before), "001-claude.md")+1 {
		t.Fatalf("Redeliver 应该重投更早的、指名 codex 的那封 001-claude.md: before=%q after=%q", before, after)
	}
}

// minor (b)：deliverCodex 读不到/解析不了归档文件时应算投递失败，不该拿空正文当真投出去。
// 注：黑盒测试很难在不改内部结构的前提下稳定制造"文件在 ListLetters 扫描之后、
// deliverCodex 内部第二次读取之前"这个极窄的竞态窗口——文件若已经不存在或解析不了，
// ListLetters/Redeliver 会在选目标那一步就把它过滤掉，走到 "没有需要投给 Codex 的信"
// 这条更早的错误分支，不会进入 deliverCodex。这里改成直接测 Redeliver 在归档文件缺失
// 时给出的错误足够明确（不会假装投递成功），deliverCodex 内部 fail() 分支本身已经过
// 人工审查确认（daemon.go 里 os.ReadFile/ParseLetter 出错都走 fail(...)，不再吞错拿空
// 正文接着投）。
func TestDaemonRedeliverErrorsClearlyWhenNothingToDeliver(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.post(t, "claude", "x\n", PostOpts{})
	f.d.RunOnce()
	if err := os.Remove(filepath.Join(f.md, "001-claude.md")); err != nil {
		t.Fatal(err)
	}
	err := f.d.Redeliver(f.chID)
	if err == nil {
		t.Fatal("归档文件缺失应报错，不能假装投递成功")
	}
	if !strings.Contains(err.Error(), "没有需要投给 Codex 的信") {
		t.Fatalf("错误应说明白没有可投的信: %v", err)
	}
}

// --- 终审修复 ---

// #2(a)：Codex 接入前写给它的信，接入后下一轮补投。
func TestDaemonDeliversPendingAfterAttach(t *testing.T) {
	f := newFixture(t)
	users := f.d.Users
	f.st.SaveMessage(f.chID, users["hou"], []int64{users["claude"], users["codex"]}, "先回", "@codex 先回\n\n用什么缓存", "")
	if err := f.d.RunOnce(); err == nil {
		t.Fatal("未接入时投递应报错")
	}
	if _, err := os.Stat(f.codex); err == nil {
		t.Fatal("未接入时不该调用 codex")
	}
	notes := len(f.notes)
	f.d.RunOnce() // 仍未接入：不重试、不再通知
	if len(f.notes) != notes {
		t.Fatalf("未接入时不该每轮重复通知: %v", f.notes)
	}
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	if err := f.d.RunOnce(); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(f.codex)
	if !strings.Contains(string(argv), "001-hou.md") || !strings.Contains(string(argv), "由你先回") {
		t.Fatalf("接入后应补投接入前的信: %q", argv)
	}
	if m, _ := f.st.LocalModuleByName("m"); m.CodexDeliveryError != "" {
		t.Fatalf("补投成功应清错误: %q", m.CodexDeliveryError)
	}
	f.d.RunOnce()
	if again, _ := os.ReadFile(f.codex); string(again) != string(argv) {
		t.Fatal("投成功后不该再投")
	}
}

// #2(b)：归档后、投递前崩溃（这里用「已归档但没有投递记录」模拟），下一轮照样投。
func TestDaemonDeliversArchivedButUndelivered(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.post(t, "claude", "第一封\n", PostOpts{})
	f.d.RunOnce()
	id, _, _, _, _ := f.st.LastDelivery(f.chID, "codex")
	os.Remove(f.codex)
	if err := f.st.RecordDelivery(id, "codex", "pending", ""); err != nil { // 非 ok：当作没投成
		t.Fatal(err)
	}
	f.d.RunOnce()
	if argv, _ := os.ReadFile(f.codex); !strings.Contains(string(argv), "001-claude.md") {
		t.Fatalf("没投成的归档信应补投: %q", argv)
	}
}

// #2：投递失败后 60 秒内不自动重试，过了 60 秒重试。
func TestDaemonRetriesFailedDeliveryAfterInterval(t *testing.T) {
	f := newFixture(t)
	clock := time.Now()
	f.d.Now = func() time.Time { return clock }
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	t.Setenv("FAKE_CODEX_FAIL", "1")
	f.post(t, "claude", "第一封\n", PostOpts{})
	if err := f.d.RunOnce(); err == nil {
		t.Fatal("首次投递应失败")
	}
	t.Setenv("FAKE_CODEX_FAIL", "0")
	count := func() int { b, _ := os.ReadFile(f.codex); return strings.Count(string(b), "001-claude.md") }
	clock = clock.Add(30 * time.Second)
	f.d.RunOnce()
	if count() != 1 {
		t.Fatalf("60 秒内不该重试: %d", count())
	}
	clock = clock.Add(40 * time.Second)
	if err := f.d.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatalf("过了 60 秒应重试一次: %d", count())
	}
	if _, status, _, _, _ := f.st.LastDelivery(f.chID, "codex"); status != "ok" {
		t.Fatalf("重试成功应记 ok: %s", status)
	}
}

// #4：信箱目录不见了（项目被挪走/删掉）：守卫不重建，记为 missing；目录回来后恢复。
func TestDaemonSkipsMissingMailbox(t *testing.T) {
	f := newFixture(t)
	if err := os.RemoveAll(f.md); err != nil {
		t.Fatal(err)
	}
	f.d.RunOnce()
	if _, err := os.Stat(f.md); !os.IsNotExist(err) {
		t.Fatal("守卫不该重建不见了的信箱目录")
	}
	if got := f.d.MissingModules(); len(got) != 1 || got[0] != f.chID {
		t.Fatalf("应记为 missing: %v", got)
	}
	EnsureMailDir(f.proj, "m")
	f.d.RunOnce()
	if got := f.d.MissingModules(); len(got) != 0 {
		t.Fatalf("目录回来后应清掉 missing: %v", got)
	}
}
