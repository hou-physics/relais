package cli

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

// setupCLITest: 起测试服务器（hou/wu 两人频道 duo；hou/wu/sun 三人频道 trio），
// 以 username 身份登录 CLI 并 init 到指定频道的临时项目目录。
func setupCLITest(t *testing.T, username, channel string) (*store.Store, map[string]*store.User, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	users := map[string]*store.User{}
	for _, n := range []string{"hou", "wu", "sun"} {
		u, _ := st.CreateUser(n, n, "pw-"+n)
		users[n] = u
	}
	duo, _ := st.CreateChannel("duo")
	st.AddMember(duo.ID, users["hou"].ID)
	st.AddMember(duo.ID, users["wu"].ID)
	trio, _ := st.CreateChannel("trio")
	for _, u := range users {
		st.AddMember(trio.ID, u.ID)
	}
	ts := httptest.NewServer(server.New(st, "http://relais.test", t.TempDir()).Handler())
	t.Cleanup(ts.Close)
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if err := saveGlobal(&GlobalConfig{Server: ts.URL, Token: users[username].AgentToken, Username: username}); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	t.Chdir(proj)
	if err := RunInit([]string{channel}); err != nil {
		t.Fatal(err)
	}
	return st, users, proj
}

func TestSendDefaultRecipientInDuo(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	md := filepath.Join(proj, "note.md")
	os.WriteFile(md, []byte("# 结论\n正文"), 0o644)
	if err := RunSend([]string{"--summary", "双人默认", md}); err != nil {
		t.Fatal(err)
	}
	ch, _ := st.ChannelByName("duo")
	msgs, _ := st.ListEnvelopes(ch.ID, users["wu"].ID, true, true)
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "wu" {
		t.Fatalf("双人频道应默认发给 wu: %+v", msgs)
	}
	// sent/ 副本落盘
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "sent"))
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".md") {
		t.Fatalf("sent/ 应有 1 个副本: %v", entries)
	}
}

func TestSendTrioRequiresTo(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "trio")
	md := filepath.Join(proj, "note.md")
	os.WriteFile(md, []byte("正文"), 0o644)
	err := RunSend([]string{"--summary", "三人", md})
	if err == nil || !strings.Contains(err.Error(), "--to") {
		t.Fatalf("三人频道缺 --to 应报错并提示: %v", err)
	}
	if err := RunSend([]string{"--summary", "三人", "--to", "wu", "--to", "sun", md}); err != nil {
		t.Fatal(err)
	}
	if err := RunSend([]string{"--summary", "全体", "--all", md}); err != nil {
		t.Fatal(err)
	}
}

func TestSendStdinDraftOnFailure(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "trio")
	// 无效收件人 → 服务器拒绝 → stdin 内容保存为草稿
	old := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.WriteString("宝贵的正文，不能丢")
	w.Close()
	t.Cleanup(func() { os.Stdin = old })
	err := RunSend([]string{"--summary", "s", "--to", "fremd", "-"})
	if err == nil {
		t.Fatal("应失败")
	}
	drafts, _ := os.ReadDir(filepath.Join(proj, "relais", "drafts"))
	if len(drafts) != 1 {
		t.Fatalf("失败后 stdin 正文应存草稿: %v", drafts)
	}
}

func TestSendStdinDraftOnMembersFailure(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "trio")
	// 损坏 token → Members 失败（401）→ stdin 内容保存为草稿
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if err := saveGlobal(&GlobalConfig{Server: "http://127.0.0.1:1", Token: "invalid", Username: "hou"}); err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	w.WriteString("宝贵的正文，不能丢")
	w.Close()
	t.Cleanup(func() { os.Stdin = old })
	err := RunSend([]string{"--summary", "s", "-"})
	if err == nil {
		t.Fatal("应失败")
	}
	drafts, _ := os.ReadDir(filepath.Join(proj, "relais", "drafts"))
	if len(drafts) != 1 {
		t.Fatalf("Members 失败后 stdin 正文应存草稿: %v", drafts)
	}
}

func TestSendFrontmatterSummary(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	md := filepath.Join(proj, "note.md")
	os.WriteFile(md, []byte("---\nsummary: 文件头里的摘要\n---\n\n# 正文\n内容"), 0o644)
	if err := RunSend([]string{md}); err != nil { // 不带 --summary
		t.Fatal(err)
	}
	ch, _ := st.ChannelByName("duo")
	msgs, _ := st.ListEnvelopes(ch.ID, users["wu"].ID, true, true)
	if len(msgs) != 1 || msgs[0].Summary != "文件头里的摘要" {
		t.Fatalf("摘要应取自 frontmatter: %+v", msgs)
	}
	got, _ := st.GetMessage(msgs[0].ID, users["wu"].ID, true)
	if got.Body != "# 正文\n内容" {
		t.Fatalf("正文不应含 frontmatter: %q", got.Body)
	}
	// --summary 显式给出时优先，正文保留原样（不剥头）
	os.WriteFile(md, []byte("---\nsummary: 会被覆盖\n---\n\nB"), 0o644)
	if err := RunSend([]string{"--summary", "显式摘要", md}); err != nil {
		t.Fatal(err)
	}
	msgs, _ = st.ListEnvelopes(ch.ID, users["wu"].ID, true, true)
	last := msgs[len(msgs)-1]
	if last.Summary != "显式摘要" {
		t.Fatalf("显式 --summary 应优先: %+v", last)
	}
	// 两者皆无 → 报错提示 --summary
	os.WriteFile(md, []byte("纯文本无头"), 0o644)
	if err := RunSend([]string{md}); err == nil || !strings.Contains(err.Error(), "--summary") {
		t.Fatalf("无摘要来源应报错: %v", err)
	}
}

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

// 本地单人模式（M7）：频道 = claude/codex/hou 三成员；agent 侧不带 --to 默认发给对侧 agent，
// 否则 hook 的 relais send 与工作脑开题都会被"三人频道需 --to"拦下（冒烟发现）。
func TestSendLocalSideDefaultsToPeer(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	cl, _ := st.CreateUser("claude", "Claude 侧", "pw-c")
	cx, _ := st.CreateUser("codex", "Codex 侧", "pw-x")
	ch, _ := st.CreateChannel("smoke")
	for _, u := range []*store.User{cl, cx, users["hou"]} {
		st.AddMember(ch.ID, u.ID)
	}
	g, _ := loadGlobal()
	if err := saveGlobal(&GlobalConfig{Server: g.Server, Token: cl.AgentToken, Username: "claude"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAIS_CHANNEL", "smoke")
	md := filepath.Join(proj, "open.md")
	os.WriteFile(md, []byte("开题"), 0o644)
	if err := RunSend([]string{"--summary", "开题", md}); err != nil {
		t.Fatalf("本地侧应默认发对侧: %v", err)
	}
	msgs, _ := st.ListEnvelopes(ch.ID, cx.ID, true, true)
	if len(msgs) != 1 || len(msgs[0].To) != 1 || msgs[0].To[0] != "codex" {
		t.Fatalf("应只发给 codex: %+v", msgs)
	}
}
