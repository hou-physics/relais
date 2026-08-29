package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/api"
)

func agentSend(t *testing.T, ts *httptest.Server, token, channel string, req api.SendRequest) (*http.Response, api.Message) {
	t.Helper()
	body, _ := json.Marshal(req)
	r, _ := http.NewRequest("POST", ts.URL+"/api/channels/"+channel+"/messages", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	var m api.Message
	if resp.StatusCode == 200 {
		json.NewDecoder(resp.Body).Decode(&m)
	}
	return resp, m
}

func TestAnchorHTTPIsolation(t *testing.T) {
	ts, _, users := newTestServer(t)
	// hou 的 agent 发一条只给 wu
	resp, m := agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"wu"}, Summary: "定向", Body: "# 机密正文"})
	if resp.StatusCode != 200 || m.ID == "" {
		t.Fatalf("发送应成功, got %d", resp.StatusCode)
	}
	// wu 的 agent 读正文：200
	r := agentGet(t, ts, users["wu"].AgentToken, "/api/messages/"+m.ID)
	if r.StatusCode != 200 {
		t.Fatalf("收件人 agent 应 200, got %d", r.StatusCode)
	}
	// sun 的 agent 读正文：403 —— 核心锚点
	r = agentGet(t, ts, users["sun"].AgentToken, "/api/messages/"+m.ID)
	if r.StatusCode != 403 {
		t.Fatalf("串台必须 403, got %d", r.StatusCode)
	}
	// sun 的 agent 列表：空
	r = agentGet(t, ts, users["sun"].AgentToken, "/api/channels/deutschapp/messages")
	var list []api.Message
	json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("sun 的 agent 列表应为空, got %d", len(list))
	}
	// sun 本人（cookie）：信封列表 1 条、正文 200 —— 人全透明
	c := loginCookie(t, ts, "sun", "pw-sun")
	req, _ := http.NewRequest("GET", ts.URL+"/api/channels/deutschapp/messages", nil)
	req.AddCookie(c)
	resp2, _ := http.DefaultClient.Do(req)
	list = nil
	json.NewDecoder(resp2.Body).Decode(&list)
	if len(list) != 1 || list[0].Body != "" || list[0].Summary != "定向" {
		t.Fatalf("人的列表应 1 条信封: %+v", list)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/messages/"+m.ID, nil)
	req.AddCookie(c)
	resp2, _ = http.DefaultClient.Do(req)
	if resp2.StatusCode != 200 {
		t.Fatalf("人读正文应 200, got %d", resp2.StatusCode)
	}
}

func TestSendValidation(t *testing.T) {
	ts, st, users := newTestServer(t)
	// 收件人不在频道 → 400 且错误里列出有效成员
	resp, _ := agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"fremd"}, Summary: "x", Body: "y"})
	if resp.StatusCode != 400 {
		t.Fatalf("陌生收件人应 400, got %d", resp.StatusCode)
	}
	var e api.ErrorResponse
	json.NewDecoder(resp.Body).Decode(&e)
	if !strings.Contains(e.Error, "wu") || !strings.Contains(e.Error, "sun") {
		t.Fatalf("错误应列出有效成员: %q", e.Error)
	}
	// 空收件人 → 400
	resp, _ = agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{Summary: "x", Body: "y"})
	if resp.StatusCode != 400 {
		t.Fatalf("空收件人应 400, got %d", resp.StatusCode)
	}
	// 摘要为空 → 400（摘要是给人看的，必填）
	resp, _ = agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"wu"}, Body: "y"})
	if resp.StatusCode != 400 {
		t.Fatalf("空摘要应 400, got %d", resp.StatusCode)
	}
	// 非成员发消息 → 403
	outsider, _ := st.CreateUser("gast", "Gast", "pw")
	resp, _ = agentSend(t, ts, outsider.AgentToken, "deutschapp",
		api.SendRequest{To: []string{"wu"}, Summary: "x", Body: "y"})
	if resp.StatusCode != 403 {
		t.Fatalf("非成员应 403, got %d", resp.StatusCode)
	}
}

func TestUnreadAndMarkRead(t *testing.T) {
	ts, _, users := newTestServer(t)
	_, m := agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"wu"}, Summary: "s", Body: "b"})
	r := agentGet(t, ts, users["wu"].AgentToken, "/api/channels/deutschapp/messages?unread=1")
	var list []api.Message
	json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 1 || !list[0].Unread {
		t.Fatalf("wu 应 1 条未读: %+v", list)
	}
	req, _ := http.NewRequest("POST", ts.URL+"/api/messages/"+m.ID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+users["wu"].AgentToken)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("标已读应 204, got %d", resp.StatusCode)
	}
	r = agentGet(t, ts, users["wu"].AgentToken, "/api/channels/deutschapp/messages?unread=1")
	list = nil
	json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("已读后未读应为空: %+v", list)
	}
}

func TestChannelsHumanOnly(t *testing.T) {
	ts, _, users := newTestServer(t)
	r := agentGet(t, ts, users["hou"].AgentToken, "/api/channels")
	if r.StatusCode != 403 {
		t.Fatalf("agent 钥匙列频道应 403, got %d", r.StatusCode)
	}
	c := loginCookie(t, ts, "hou", "pw-hou")
	req, _ := http.NewRequest("GET", ts.URL+"/api/channels", nil)
	req.AddCookie(c)
	resp, _ := http.DefaultClient.Do(req)
	var infos []api.ChannelInfo
	json.NewDecoder(resp.Body).Decode(&infos)
	if len(infos) != 1 || infos[0].Name != "deutschapp" {
		t.Fatalf("频道列表不对: %+v", infos)
	}
}
