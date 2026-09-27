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
