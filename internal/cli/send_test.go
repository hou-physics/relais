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
