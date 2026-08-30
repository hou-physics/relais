package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hou-physics/relais/internal/api"
)

// makeAdmin 把 newTestServer 里的某用户设为管理员并返回其 cookie。
func TestAdminInvariantAndOps(t *testing.T) {
	ts, st, users := newTestServer(t)
	// hou 设为管理员
	if err := st.SetAdmin(users["hou"].ID, true); err != nil {
		t.Fatal(err)
	}
	adminCookie := loginCookie(t, ts, "hou", "pw-hou")

	// 不变量：管理员自己的 agent token 打 admin 端点 → 403
	r := agentDo(t, ts, users["hou"].AgentToken, "GET", "/api/admin/channels", nil)
	if r.StatusCode != 403 {
		t.Fatalf("管理员 agent token 应 403, got %d", r.StatusCode)
	}
	// 不变量：非管理员(wu)人钥匙 → 403
	wuCookie := loginCookie(t, ts, "wu", "pw-wu")
	req, _ := http.NewRequest("GET", ts.URL+"/api/admin/channels", nil)
	req.AddCookie(wuCookie)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("非管理员应 403, got %d", resp.StatusCode)
	}

	// 管理员人钥匙：建频道
	adminReq := func(method, path string, in any) *http.Response {
		var body = bytes.NewReader(nil)
		if in != nil {
			b, _ := json.Marshal(in)
			body = bytes.NewReader(b)
		}
		rq, _ := http.NewRequest(method, ts.URL+path, body)
		rq.AddCookie(adminCookie)
		rp, err := http.DefaultClient.Do(rq)
		if err != nil {
			t.Fatal(err)
		}
		return rp
	}
	if rp := adminReq("POST", "/api/admin/channels", api.AdminChannelRequest{Name: "proj"}); rp.StatusCode != 200 {
		t.Fatalf("建频道应 200, got %d", rp.StatusCode)
	}
	// 重名 400
	if rp := adminReq("POST", "/api/admin/channels", api.AdminChannelRequest{Name: "proj"}); rp.StatusCode != 400 {
		t.Fatalf("重名频道应 400, got %d", rp.StatusCode)
	}
	// 列频道含 proj（管理员看全部）
	rp := adminReq("GET", "/api/admin/channels", nil)
	var stats []api.ChannelStat
	json.NewDecoder(rp.Body).Decode(&stats)
	found := false
	for _, s := range stats {
		if s.Name == "proj" {
			found = true
		}
	}
	if !found {
		t.Fatalf("频道列表应含 proj: %+v", stats)
	}
	// 加成员 wu（管理员本人不是 proj 成员也能管）
	if rp := adminReq("POST", "/api/admin/channels/proj/members", api.AdminMemberRequest{Username: "wu"}); rp.StatusCode != 204 {
		t.Fatalf("加成员应 204, got %d", rp.StatusCode)
	}
	// 加不存在的用户 404
	if rp := adminReq("POST", "/api/admin/channels/proj/members", api.AdminMemberRequest{Username: "ghost"}); rp.StatusCode != 404 {
		t.Fatalf("加不存在用户应 404, got %d", rp.StatusCode)
	}
	// 成员列表含 wu
	rp = adminReq("GET", "/api/admin/channels/proj/members", nil)
	var ms []api.Member
	json.NewDecoder(rp.Body).Decode(&ms)
	if len(ms) != 1 || ms[0].Username != "wu" {
		t.Fatalf("proj 成员应为 wu: %+v", ms)
	}
	// 生成邀请
	rp = adminReq("POST", "/api/admin/channels/proj/invites", nil)
	var out map[string]string
	json.NewDecoder(rp.Body).Decode(&out)
	if out["url"] == "" {
		t.Fatalf("应返回邀请链接: %+v", out)
	}
	// 移除成员
	if rp := adminReq("DELETE", "/api/admin/channels/proj/members/wu", nil); rp.StatusCode != 204 {
		t.Fatalf("移除成员应 204, got %d", rp.StatusCode)
	}
	rp = adminReq("GET", "/api/admin/channels/proj/members", nil)
	ms = nil
	json.NewDecoder(rp.Body).Decode(&ms)
	if len(ms) != 0 {
		t.Fatalf("移除后应无成员: %+v", ms)
	}
}

func TestMeCarriesIsAdmin(t *testing.T) {
	ts, st, users := newTestServer(t)
	st.SetAdmin(users["hou"].ID, true)
	c := loginCookie(t, ts, "hou", "pw-hou")
	req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	req.AddCookie(c)
	resp, _ := http.DefaultClient.Do(req)
	var me api.Me
	json.NewDecoder(resp.Body).Decode(&me)
	if !me.IsAdmin {
		t.Fatalf("管理员 /api/me 应 is_admin=true: %+v", me)
	}
}
