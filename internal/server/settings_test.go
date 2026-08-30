package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hou-physics/relais/internal/api"
)

func humanDo(t *testing.T, ts *httptest.Server, c *http.Cookie, method, path string, in any) *http.Response {
	t.Helper()
	var body = bytes.NewReader(nil)
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, ts.URL+path, body)
	req.AddCookie(c)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestSettingsFlow(t *testing.T) {
	ts, st, users := newTestServer(t)
	c := loginCookie(t, ts, "hou", "pw-hou")
	// 改资料
	r := humanDo(t, ts, c, "POST", "/api/settings/profile", api.ProfileRequest{DisplayName: "侯", Avatar: "🦉"})
	if r.StatusCode != 200 {
		t.Fatalf("改资料应 200, got %d", r.StatusCode)
	}
	var me api.Me
	json.NewDecoder(r.Body).Decode(&me)
	if me.DisplayName != "侯" || me.Avatar != "🦉" {
		t.Fatalf("me 应更新: %+v", me)
	}
	// 改密码：旧密码错 401
	r = humanDo(t, ts, c, "POST", "/api/settings/password", api.PasswordRequest{Old: "falsch", New: "neuepass123"})
	if r.StatusCode != 401 {
		t.Fatalf("旧密码错应 401, got %d", r.StatusCode)
	}
	r = humanDo(t, ts, c, "POST", "/api/settings/password", api.PasswordRequest{Old: "pw-hou", New: "neuepass123"})
	if r.StatusCode != 204 {
		t.Fatalf("改密码应 204, got %d", r.StatusCode)
	}
	if _, err := st.Authenticate("hou", "neuepass123"); err != nil {
		t.Fatal("新密码应生效")
	}
	// 换 token：旧 token 立即 401
	oldTok := users["hou"].AgentToken
	r = humanDo(t, ts, c, "POST", "/api/settings/token", nil)
	var tr api.TokenResponse
	json.NewDecoder(r.Body).Decode(&tr)
	if tr.AgentToken == "" || tr.AgentToken == oldTok {
		t.Fatalf("应返回新 token: %+v", tr)
	}
	if resp := agentGet(t, ts, oldTok, "/api/me"); resp.StatusCode != 401 {
		t.Fatalf("旧 token 应 401, got %d", resp.StatusCode)
	}
	if resp := agentGet(t, ts, tr.AgentToken, "/api/me"); resp.StatusCode != 200 {
		t.Fatalf("新 token 应 200, got %d", resp.StatusCode)
	}
	// 登出：session 失效
	r = humanDo(t, ts, c, "POST", "/api/logout", nil)
	if r.StatusCode != 204 {
		t.Fatalf("登出应 204, got %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	req.AddCookie(c)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 401 {
		t.Fatalf("登出后应 401, got %d", resp.StatusCode)
	}
}

func TestSettingsHumanKeyOnly(t *testing.T) {
	ts, _, users := newTestServer(t)
	for _, path := range []string{"/api/settings/password", "/api/settings/profile", "/api/settings/token", "/api/logout"} {
		req, _ := http.NewRequest("POST", ts.URL+path, bytes.NewReader([]byte("{}")))
		req.Header.Set("Authorization", "Bearer "+users["hou"].AgentToken)
		resp, _ := http.DefaultClient.Do(req)
		if resp.StatusCode != 403 {
			t.Fatalf("%s agent 钥匙应 403, got %d", path, resp.StatusCode)
		}
	}
}
