// Package e2e 是 Relais 的锚点回归套件（spec §12）。
// 锚点对本项目的意义 = 单元测试对代码的意义：任何改动破坏本文件中的断言 → 当天回退。
package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/cli"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

type world struct {
	st    *store.Store
	ts    *httptest.Server
	users map[string]*store.User
}

func newWorld(t *testing.T) *world {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "e2e.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &world{st: st, users: map[string]*store.User{}}
	for _, n := range []string{"hou", "wu", "sun"} {
		u, _ := st.CreateUser(n, n, "pw-"+n)
		w.users[n] = u
	}
	duo, _ := st.CreateChannel("duo")
	st.AddMember(duo.ID, w.users["hou"].ID)
	st.AddMember(duo.ID, w.users["wu"].ID)
	trio, _ := st.CreateChannel("trio")
	for _, u := range w.users {
		st.AddMember(trio.ID, u.ID)
	}
	w.ts = httptest.NewServer(server.New(st, "http://relais.e2e", t.TempDir()).Handler())
	t.Cleanup(w.ts.Close)
	return w
}

// actAs 把 CLI 切换为某用户身份并绑定到临时项目目录。
func (w *world) actAs(t *testing.T, username, channel string) string {
	t.Helper()
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if err := cli.SaveGlobalForTest(w.ts.URL, w.users[username].AgentToken, username); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	t.Chdir(proj)
	if err := cli.RunInit([]string{channel}); err != nil {
		t.Fatal(err)
	}
	return proj
}

func TestAnchorFullLoop(t *testing.T) {
	w := newWorld(t)

	// 锚点 1：双人频道，hou 免 --to 发送，wu 拉取落盘
	houProj := w.actAs(t, "hou", "duo")
	md := filepath.Join(houProj, "conclusion.md")
	os.WriteFile(md, []byte("# 结论\n给 wu 的 agent 的正文"), 0o644)
	if err := cli.RunSend([]string{"--summary", "架构结论", md}); err != nil {
		t.Fatalf("锚点1 发送失败: %v", err)
	}
	wuProj := w.actAs(t, "wu", "duo")
	if err := cli.RunPull(nil); err != nil {
		t.Fatalf("锚点1 拉取失败: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(wuProj, "relais", "inbox"))
	if len(entries) != 1 {
		t.Fatalf("锚点1 应落盘 1 条: %v", entries)
	}

	// 锚点 2：三人频道缺 --to 必须报错
	w.actAs(t, "hou", "trio")
	os.WriteFile("note.md", []byte("x"), 0o644)
	if err := cli.RunSend([]string{"--summary", "s", "note.md"}); err == nil ||
		!strings.Contains(err.Error(), "--to") {
		t.Fatalf("锚点2 应要求 --to: %v", err)
	}

	// 锚点 3：A→B 定向后，C 的 agent 403、C 本人 200
	if err := cli.RunSend([]string{"--summary", "定向给wu", "--to", "wu", "note.md"}); err != nil {
		t.Fatal(err)
	}
	trioCh, _ := w.st.ChannelByName("trio")
	msgs, _ := w.st.ListEnvelopes(trioCh.ID, w.users["wu"].ID, true, true)
	if len(msgs) != 1 {
		t.Fatalf("wu 应有 1 条未读: %+v", msgs)
	}
	id := msgs[0].ID
	req, _ := http.NewRequest("GET", w.ts.URL+"/api/messages/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+w.users["sun"].AgentToken)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("锚点3 串台必须 403, got %d", resp.StatusCode)
	}
	if _, err := w.st.GetMessage(id, w.users["sun"].ID, false); err != nil {
		t.Fatalf("锚点3 人应全透明: %v", err)
	}
}
