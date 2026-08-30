package server

import (
	"encoding/json"
	"testing"

	"github.com/hou-physics/relais/internal/api"
)

func TestAutoGovernanceHTTP(t *testing.T) {
	ts, _, users := newTestServer(t) // hou/wu/sun 在 deutschapp
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	// 人开启 cap=2
	r := humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto", api.AutoConfigRequest{Enabled: true, Cap: 2})
	if r.StatusCode != 204 {
		t.Fatalf("开启应 204, got %d", r.StatusCode)
	}
	// agent turn ×2 放行、第3次拒
	turn := func() api.TurnResponse {
		resp := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/channels/deutschapp/auto/turn", nil)
		var tr api.TurnResponse
		json.NewDecoder(resp.Body).Decode(&tr)
		return tr
	}
	if !turn().Allowed || !turn().Allowed {
		t.Fatal("前2轮应放行")
	}
	if third := turn(); third.Allowed || third.Reason == "" {
		t.Fatalf("第3轮应被拒: %+v", third)
	}
	// resume 重置
	humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto/resume", nil)
	if !turn().Allowed {
		t.Fatal("resume 后应放行")
	}
	// needs-human 立即暂停 + 状态可读
	agentDo(t, ts, users["wu"].AgentToken, "POST", "/api/channels/deutschapp/auto/needs-human", api.NeedsHumanRequest{Question: "预算多少？"})
	rs := humanDo(t, ts, cookie, "GET", "/api/channels/deutschapp/auto", nil)
	var st api.AutoState
	json.NewDecoder(rs.Body).Decode(&st)
	if !st.Paused || st.NeedsHumanQ != "预算多少？" {
		t.Fatalf("needs-human 状态不对: %+v", st)
	}
	if turn().Allowed {
		t.Fatal("needs-human 暂停时不放行")
	}
	// 钥匙隔离：agent 不能开关；人不能请求 turn
	if r := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/channels/deutschapp/auto", api.AutoConfigRequest{Enabled: false}); r.StatusCode != 403 {
		t.Fatalf("agent 开关 auto 应 403, got %d", r.StatusCode)
	}
	if r := humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/auto/turn", nil); r.StatusCode != 403 {
		t.Fatalf("人请求 turn 应 403, got %d", r.StatusCode)
	}
}

func TestGuidanceHTTP(t *testing.T) {
	ts, _, users := newTestServer(t)
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	humanDo(t, ts, cookie, "POST", "/api/channels/deutschapp/guidance", api.GuidanceRequest{Note: "优先上线速度"})
	// hou 的 agent 取到并清空
	resp := agentDo(t, ts, users["hou"].AgentToken, "GET", "/api/channels/deutschapp/guidance", nil)
	var g api.GuidanceResponse
	json.NewDecoder(resp.Body).Decode(&g)
	if g.Note != "优先上线速度" {
		t.Fatalf("应取到引导: %+v", g)
	}
	resp = agentDo(t, ts, users["hou"].AgentToken, "GET", "/api/channels/deutschapp/guidance", nil)
	json.NewDecoder(resp.Body).Decode(&g)
	if g.Note != "" {
		t.Fatalf("取后应清空: %+v", g)
	}
	// wu 的 agent 取不到 hou 的引导（各人各自）
	resp = agentDo(t, ts, users["wu"].AgentToken, "GET", "/api/channels/deutschapp/guidance", nil)
	json.NewDecoder(resp.Body).Decode(&g)
	if g.Note != "" {
		t.Fatalf("wu 不应看到 hou 的引导: %+v", g)
	}
}
