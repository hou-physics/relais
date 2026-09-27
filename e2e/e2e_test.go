// Package e2e 是 Relais 的锚点回归套件（spec §12）。
// 锚点对本项目的意义 = 单元测试对代码的意义：任何改动破坏本文件中的断言 → 当天回退。
package e2e

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/cli"
	"github.com/hou-physics/relais/internal/local"
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

// TestAnchorAdminInvariantAllEndpoints 把「agent token 无任何管理权」这条安全核心
// 铺到全部 6 个 admin 端点：不管方法/路径，agent token 必须在进入任何 handler 逻辑
// 之前就被拦 403（哪怕频道名根本不存在）。
func TestAnchorAdminInvariantAllEndpoints(t *testing.T) {
	w := newWorld(t)
	// hou 设管理员——即便如此，agent token 仍必须被拒，证明拦截与管理员身份无关。
	if err := w.st.SetAdmin(w.users["hou"].ID, true); err != nil {
		t.Fatal(err)
	}
	endpoints := []struct {
		method string
		path   string
	}{
		{"GET", "/api/admin/channels"},
		{"POST", "/api/admin/channels"},
		{"GET", "/api/admin/channels/general/members"},
		{"POST", "/api/admin/channels/general/members"},
		{"DELETE", "/api/admin/channels/general/members/wu"},
		{"POST", "/api/admin/channels/general/invites"},
	}
	for _, ep := range endpoints {
		req, _ := http.NewRequest(ep.method, w.ts.URL+ep.path, nil)
		req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", ep.method, ep.path, err)
		}
		if resp.StatusCode != 403 {
			t.Fatalf("锚点D %s %s 管理员 agent token 必须 403, got %d", ep.method, ep.path, resp.StatusCode)
		}
	}
}

func TestAnchorAutoGovernance(t *testing.T) {
	w := newWorld(t)
	duo, _ := w.st.ChannelByName("duo")
	// 人开启 cap=2
	if err := w.st.SetAutoEnabled(duo.ID, true, 2); err != nil {
		t.Fatal(err)
	}
	// agent turn 经 HTTP：前2放行、第3拒
	turn := func() bool {
		req, _ := http.NewRequest("POST", w.ts.URL+"/api/channels/duo/auto/turn", nil)
		req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var tr struct {
			Allowed bool `json:"allowed"`
		}
		json.NewDecoder(resp.Body).Decode(&tr)
		return tr.Allowed
	}
	if !turn() || !turn() {
		t.Fatal("锚点：前2轮应放行")
	}
	if turn() {
		t.Fatal("锚点：第3轮必须被拒(检查点)")
	}
	// needs-human 立即暂停后 turn 拒
	w.st.ResumeAuto(duo.ID)
	if err := w.st.SetNeedsHuman(duo.ID, "预算？"); err != nil {
		t.Fatal(err)
	}
	if turn() {
		t.Fatal("锚点：needs-human 暂停时必须拒 turn")
	}
	// 安全底线：默认新频道 auto 关闭 → turn 拒
	w.st.CreateChannel("fresh")
	fresh, _ := w.st.ChannelByName("fresh")
	w.st.AddMember(fresh.ID, w.users["hou"].ID)
	req, _ := http.NewRequest("POST", w.ts.URL+"/api/channels/fresh/auto/turn", nil)
	req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
	resp, _ := http.DefaultClient.Do(req)
	var tr struct {
		Allowed bool `json:"allowed"`
	}
	json.NewDecoder(resp.Body).Decode(&tr)
	if tr.Allowed {
		t.Fatal("锚点：默认未开启的频道必须拒 turn（安全底线）")
	}
}

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

// M8 锚点：本地管理接口的隔离 + 模块创建（M9：信箱 + 协议 + 指针块；收发闭环由 internal/local 守卫测试覆盖）
func TestAnchorM8LocalConsole(t *testing.T) {
	w := newWorld(t)
	// 线上世界（未注入本地管理器）：/api/local/* 一律 404
	req, _ := http.NewRequest("GET", w.ts.URL+"/api/local/modules", nil)
	req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 404 {
		t.Fatalf("锚点M8-1 线上不得暴露本地接口, got %d", resp.StatusCode)
	}
	// 本地世界：真 localManager + 真 serve handler
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	t.Setenv("HOME", t.TempDir())
	if err := cli.RunLocal([]string{"bootstrap", "--listen", "127.0.0.1:18096", "--no-service"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(ld, "data", "relais.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st, "http://127.0.0.1:18096", t.TempDir())
	srv.SetLocal(cli.NewLocalManagerForTest(ld), "hou")
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// M9：本地控制台只查回环，不再区分人/agent 钥匙——回环 + agent token 也放行（守卫循环要用）
	claudeU, _ := st.UserByName("claude")
	req, _ = http.NewRequest("GET", ts.URL+"/api/local/modules", nil)
	req.Header.Set("Authorization", "Bearer "+claudeU.AgentToken)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 200 {
		t.Fatalf("锚点M8-2（M9 更新：回环免人/agent 之分）agent 打本地接口应 200, got %d", resp.StatusCode)
	}
	// 人在控制台新建模块（M9：回环免钥匙，无需登录）
	proj := t.TempDir()
	body, _ := json.Marshal(map[string]string{"name": "grammar", "dir": proj})
	req, _ = http.NewRequest("POST", ts.URL+"/api/local/modules", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("锚点M8-3 新建模块应 200, got %d", resp.StatusCode)
	}
	// M9：模块 = 信箱目录 + 协议 + 指针块；不再有 sides/、human.txt、RULES.md、AGENT.md
	for _, f := range []string{"relais/PROTOCOL.md", "relais/mail/grammar/outbox", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(proj, f)); err != nil {
			t.Fatalf("锚点M8-3 项目应有 %s", f)
		}
	}
	for _, p := range []string{filepath.Join(ld, "sides"), filepath.Join(ld, "human.txt"), filepath.Join(proj, "relais", "AGENT.md")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("锚点M8-3 不应再生成 %s", p)
		}
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/local/modules", nil)
	resp, _ = http.DefaultClient.Do(req)
	var mods []api.LocalModule
	json.NewDecoder(resp.Body).Decode(&mods)
	if len(mods) != 1 || mods[0].Name != "grammar" || mods[0].State != "未接入" {
		t.Fatalf("锚点M8-5 模块列表应含 grammar: %+v", mods)
	}
	// 非回环 RemoteAddr → 401（M9：localOnly 只查回环，直接调 handler）
	rr := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/local/modules", nil)
	r2.RemoteAddr = "192.168.1.9:5555"
	srv.Handler().ServeHTTP(rr, r2)
	if rr.Code != 401 {
		t.Fatalf("锚点M8-6 非回环必须 401, got %d", rr.Code)
	}
}

// M9 锚点：serve 在本地模式起守卫——agent 用 local.Post 把信放进 outbox，守卫几秒内归档成 001-claude.md，
// 本地控制台（回环免钥匙）报告最新一封与轮到谁（替代 M8-4 的"信送到另一侧"锚点）。
func TestAnchorM9DaemonEndToEnd(t *testing.T) {
	const listen = "127.0.0.1:18093"
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	t.Setenv("HOME", t.TempDir())
	if err := cli.RunLocal([]string{"bootstrap", "--listen", listen, "--no-service"}); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if _, err := cli.NewLocalManagerForTest(ld).CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatal(err)
	}
	// RunServe 无法优雅停止——测试进程结束即释放端口；端口与其它测试错开
	go func() { _ = cli.RunServe([]string{"--config", filepath.Join(ld, "server.toml")}) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if c, err := net.DialTimeout("tcp", listen, 200*time.Millisecond); err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("锚点M9-1 守卫端到端：serve 没起来")
		}
		time.Sleep(100 * time.Millisecond)
	}
	md := local.MailDir(proj, "m")
	draft := filepath.Join(md, "drafts", "a.md")
	if err := os.WriteFile(draft, []byte("第一封\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Post(md, "claude", draft, local.PostOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(md, "001-claude.md")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("锚点M9-1 守卫端到端：守卫应在几秒内把 outbox 的信归档成 001-claude.md")
		}
		time.Sleep(200 * time.Millisecond)
	}
	resp, err := http.Get("http://" + listen + "/api/local/modules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("锚点M9-1 守卫端到端：回环免钥匙应 200, got %d", resp.StatusCode)
	}
	var mods []api.LocalModule
	if err := json.NewDecoder(resp.Body).Decode(&mods); err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0].LastSeq != 1 || mods[0].LastFrom != "claude" || mods[0].WaitingFor != "codex" {
		t.Fatalf("锚点M9-1 守卫端到端：模块状态应为 第1封/来自claude/轮到codex: %+v", mods)
	}
}

func login(t *testing.T, ts *httptest.Server, user, pw string) string {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pw})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("登录失败: %v %v", err, resp)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "relais_session" {
			return c.Value
		}
	}
	t.Fatal("无 session cookie")
	return ""
}

func mustUser(t *testing.T, st *store.Store, name string) *store.User {
	u, err := st.UserByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
