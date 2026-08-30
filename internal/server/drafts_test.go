package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hou-physics/relais/internal/api"
)

func agentDo(t *testing.T, ts *httptest.Server, token, method, path string, in any) *http.Response {
	t.Helper()
	var body *bytes.Reader = bytes.NewReader(nil)
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestDraftFlowAndIsolation(t *testing.T) {
	ts, _, users := newTestServer(t)
	// hou 建草稿
	resp := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/channels/deutschapp/drafts",
		api.SendRequest{To: []string{"wu"}, Summary: "草稿", Body: "# 正文"})
	if resp.StatusCode != 200 {
		t.Fatalf("建草稿应 200, got %d", resp.StatusCode)
	}
	var d api.Draft
	json.NewDecoder(resp.Body).Decode(&d)
	if d.ID == "" || d.To[0] != "wu" {
		t.Fatalf("草稿不完整: %+v", d)
	}
	// 隔离：wu（收件人）与 sun 都看不见，agent 与 cookie 两把钥匙都试
	r := agentDo(t, ts, users["wu"].AgentToken, "GET", "/api/channels/deutschapp/drafts", nil)
	var list []api.Draft
	json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("非作者列表应为空: %+v", list)
	}
	c := loginCookie(t, ts, "sun", "pw-sun")
	req, _ := http.NewRequest("POST", ts.URL+"/api/drafts/"+d.ID+"/send", nil)
	req.AddCookie(c)
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != 404 {
		t.Fatalf("非作者 send 必须 404, got %d", resp2.StatusCode)
	}
	r = agentDo(t, ts, users["sun"].AgentToken, "DELETE", "/api/drafts/"+d.ID, nil)
	if r.StatusCode != 404 {
		t.Fatalf("非作者 delete 必须 404, got %d", r.StatusCode)
	}
	// 作者发送：转正式消息，wu 收到未读；草稿消失
	r = agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/drafts/"+d.ID+"/send", nil)
	if r.StatusCode != 200 {
		t.Fatalf("作者 send 应 200, got %d", r.StatusCode)
	}
	var m api.Message
	json.NewDecoder(r.Body).Decode(&m)
	if m.Summary != "草稿" || m.From != "hou" {
		t.Fatalf("转正消息不对: %+v", m)
	}
	r = agentDo(t, ts, users["wu"].AgentToken, "GET", "/api/channels/deutschapp/messages?unread=1", nil)
	var msgs []api.Message
	json.NewDecoder(r.Body).Decode(&msgs)
	if len(msgs) != 1 || msgs[0].ID != m.ID {
		t.Fatalf("wu 应收到该消息: %+v", msgs)
	}
	r = agentDo(t, ts, users["hou"].AgentToken, "GET", "/api/channels/deutschapp/drafts", nil)
	list = nil
	json.NewDecoder(r.Body).Decode(&list)
	if len(list) != 0 {
		t.Fatalf("发送后草稿应删除: %+v", list)
	}
	// 二次 send 同一草稿 → 404
	r = agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/drafts/"+d.ID+"/send", nil)
	if r.StatusCode != 404 {
		t.Fatalf("已发送草稿再 send 应 404, got %d", r.StatusCode)
	}
}

func TestDraftValidation(t *testing.T) {
	ts, _, users := newTestServer(t)
	resp := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/channels/deutschapp/drafts",
		api.SendRequest{To: []string{"fremd"}, Summary: "x", Body: "y"})
	if resp.StatusCode != 400 {
		t.Fatalf("陌生收件人应 400, got %d", resp.StatusCode)
	}
	resp = agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/channels/deutschapp/drafts",
		api.SendRequest{To: []string{"wu"}, Body: "y"})
	if resp.StatusCode != 400 {
		t.Fatalf("空摘要应 400, got %d", resp.StatusCode)
	}
}
