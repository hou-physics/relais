package server

import (
	"bytes"
	"encoding/json"
	"net/http"
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
