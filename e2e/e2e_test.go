// Package e2e 是 Relais 的锚点回归套件（spec §12）。
// 锚点对本项目的意义 = 单元测试对代码的意义：任何改动破坏本文件中的断言 → 当天回退。
package e2e

import (
	"bytes"
	"encoding/json"
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

func TestAnchorM2Flows(t *testing.T) {
	w := newWorld(t)

	// 锚点 5：frontmatter 摘要贯通（CLI 无 --summary）
	houProj := w.actAs(t, "hou", "duo")
	md := filepath.Join(houProj, "fm.md")
	os.WriteFile(md, []byte("---\nsummary: FM摘要\n---\n\n正文X"), 0o644)
	if err := cli.RunSend([]string{md}); err != nil {
		t.Fatalf("锚点5 frontmatter 发送失败: %v", err)
	}

	// 锚点 6：草稿仅作者可见 + 转正闭环
	os.WriteFile(md, []byte("---\nsummary: 草稿FM\n---\n\n草稿正文"), 0o644)
	if err := cli.RunDraft([]string{md}); err != nil {
		t.Fatalf("锚点6 draft 失败: %v", err)
	}
	duoCh, _ := w.st.ChannelByName("duo")
	drafts, _ := w.st.ListDrafts(duoCh.ID, w.users["hou"].ID)
	if len(drafts) != 1 {
		t.Fatalf("作者应有 1 条草稿: %+v", drafts)
	}
	if l, _ := w.st.ListDrafts(duoCh.ID, w.users["wu"].ID); len(l) != 0 {
		t.Fatalf("锚点6 非作者必须不可见: %+v", l)
	}
	// 作者经 HTTP 发送草稿
	req, _ := http.NewRequest("POST", w.ts.URL+"/api/drafts/"+drafts[0].ID+"/send", nil)
	req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("锚点6 转正应 200: %v %d", err, resp.StatusCode)
	}

	// 锚点 7：bridge pollOnce 落盘（wu 侧两条未读：FM摘要 + 草稿转正）
	wuProj := w.actAs(t, "wu", "duo")
	c, err := cli.NewClientForTest()
	if err != nil {
		t.Fatal(err)
	}
	landed, err := cli.PollOnceForTest(c, "duo", wuProj)
	if err != nil || landed != 2 {
		t.Fatalf("锚点7 bridge 应落 2 条: %d %v", landed, err)
	}
	entries, _ := os.ReadDir(filepath.Join(wuProj, "relais", "inbox"))
	if len(entries) != 2 {
		t.Fatalf("锚点7 inbox 应 2 个文件: %v", entries)
	}

	// 锚点 8：token 重置后旧 token 401
	newTok, _ := w.st.RegenerateToken(w.users["sun"].ID)
	reqOld, _ := http.NewRequest("GET", w.ts.URL+"/api/me", nil)
	reqOld.Header.Set("Authorization", "Bearer "+w.users["sun"].AgentToken)
	respOld, _ := http.DefaultClient.Do(reqOld)
	if respOld.StatusCode != 401 {
		t.Fatalf("锚点8 旧 token 应 401, got %d", respOld.StatusCode)
	}
	reqNew, _ := http.NewRequest("GET", w.ts.URL+"/api/me", nil)
	reqNew.Header.Set("Authorization", "Bearer "+newTok)
	respNew, _ := http.DefaultClient.Do(reqNew)
	if respNew.StatusCode != 200 {
		t.Fatalf("锚点8 新 token 应 200, got %d", respNew.StatusCode)
	}
}

func TestAnchorAdminInvariant(t *testing.T) {
	w := newWorld(t)
	// hou 设管理员
	if err := w.st.SetAdmin(w.users["hou"].ID, true); err != nil {
		t.Fatal(err)
	}
	adminGet := func(auth func(*http.Request)) int {
		req, _ := http.NewRequest("GET", w.ts.URL+"/api/admin/channels", nil)
		auth(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	// 锚点 A：管理员的 agent token → 403（安全核心）
	if code := adminGet(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken) }); code != 403 {
		t.Fatalf("锚点A 管理员 agent token 必须 403, got %d", code)
	}
	// 锚点 B：管理员人钥匙（登录拿 cookie）→ 200
	body, _ := json.Marshal(map[string]string{"username": "hou", "password": "pw-hou"})
	lr, _ := http.Post(w.ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	var cookie *http.Cookie
	for _, c := range lr.Cookies() {
		if c.Name == "relais_session" {
			cookie = c
		}
	}
	if code := adminGet(func(r *http.Request) { r.AddCookie(cookie) }); code != 200 {
		t.Fatalf("锚点B 管理员人钥匙应 200, got %d", code)
	}
	// 锚点 C：非管理员(wu)人钥匙 → 403
	body2, _ := json.Marshal(map[string]string{"username": "wu", "password": "pw-wu"})
	lr2, _ := http.Post(w.ts.URL+"/api/login", "application/json", bytes.NewReader(body2))
	var cookie2 *http.Cookie
	for _, c := range lr2.Cookies() {
		if c.Name == "relais_session" {
			cookie2 = c
		}
	}
	if code := adminGet(func(r *http.Request) { r.AddCookie(cookie2) }); code != 403 {
		t.Fatalf("锚点C 非管理员应 403, got %d", code)
	}
}
