package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

// newTestServer: hou/wu/sun 三人 + 频道 deutschapp（全员）。后续 server 测试都用它。
func newTestServer(t *testing.T) (*httptest.Server, *store.Store, map[string]*store.User) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	users := map[string]*store.User{}
	for _, n := range []string{"hou", "wu", "sun"} {
		u, err := st.CreateUser(n, n, "pw-"+n)
		if err != nil {
			t.Fatal(err)
		}
		users[n] = u
	}
	ch, _ := st.CreateChannel("deutschapp")
	for _, u := range users {
		st.AddMember(ch.ID, u.ID)
	}
	ts := httptest.NewServer(New(st, "http://relais.test", t.TempDir()).Handler())
	t.Cleanup(ts.Close)
	return ts, st, users
}

func loginCookie(t *testing.T, ts *httptest.Server, username, password string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(api.LoginRequest{Username: username, Password: password})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("登录应 200, got %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "relais_session" {
			return c
		}
	}
	t.Fatal("响应缺少 relais_session cookie")
	return nil
}

func agentGet(t *testing.T, ts *httptest.Server, token, path string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", ts.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLoginAndMe(t *testing.T) {
	ts, _, users := newTestServer(t)
	// 错密码 401
	body, _ := json.Marshal(api.LoginRequest{Username: "hou", Password: "falsch"})
	resp, _ := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 401 {
		t.Fatalf("错密码应 401, got %d", resp.StatusCode)
	}
	// cookie → human
	c := loginCookie(t, ts, "hou", "pw-hou")
	req, _ := http.NewRequest("GET", ts.URL+"/api/me", nil)
	req.AddCookie(c)
	resp, _ = http.DefaultClient.Do(req)
	var me api.Me
	json.NewDecoder(resp.Body).Decode(&me)
	if me.Username != "hou" || me.Key != "human" {
		t.Fatalf("me 不对: %+v", me)
	}
	// bearer → agent
	resp = agentGet(t, ts, users["wu"].AgentToken, "/api/me")
	json.NewDecoder(resp.Body).Decode(&me)
	if me.Username != "wu" || me.Key != "agent" {
		t.Fatalf("agent me 不对: %+v", me)
	}
	// 无凭证 401
	resp, _ = http.Get(ts.URL + "/api/me")
	if resp.StatusCode != 401 {
		t.Fatalf("无凭证应 401, got %d", resp.StatusCode)
	}
}

func TestLoopbackWithoutKeyIsHumanOnlyInLocalMode(t *testing.T) {
	// 线上（无 local）：回环不带钥匙 → 401（永久锚点）
	ts, _, _ := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/api/me")
	if resp.StatusCode != 401 {
		t.Fatalf("线上回环免钥匙不该存在: %d", resp.StatusCode)
	}
	// 本地：回环不带钥匙 → 200 且是 hou
	ts2, srv, _, users := newLocalTestServer(t)
	resp, _ = http.Get(ts2.URL + "/api/me")
	if resp.StatusCode != 200 {
		t.Fatalf("本地回环免钥匙应 200: %d", resp.StatusCode)
	}
	var me api.Me
	json.NewDecoder(resp.Body).Decode(&me)
	if me.Username != "hou" {
		t.Fatalf("应是 hou: %+v", me)
	}
	// 本地但非回环 → 401
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.RemoteAddr = "10.0.0.5:1"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("非回环无钥匙应 401: %d", rec.Code)
	}
	// 本地 + agent token 仍按 token
	r := agentGet(t, ts2, users["hou"].AgentToken, "/api/me")
	if r.StatusCode != 200 {
		t.Fatalf("agent token 仍有效: %d", r.StatusCode)
	}
	// 本地：RemoteAddr 回环，但 Host 是攻击者域名（DNS rebinding）→ 403
	req2 := httptest.NewRequest("GET", "/api/me", nil)
	req2.Host = "evil.com"
	req2.RemoteAddr = "127.0.0.1:1"
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != 403 {
		t.Fatalf("Host 非回环应 403: %d", rec2.Code)
	}
	// 本地：回环 + 跨站 Origin 的写请求（CSRF）→ 403
	body, _ := json.Marshal(api.LocalModuleRequest{Name: "x", Dir: "/y"})
	req3 := httptest.NewRequest("POST", "/api/local/modules", bytes.NewReader(body))
	req3.Host = "127.0.0.1:8080"
	req3.RemoteAddr = "127.0.0.1:1"
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("Origin", "https://evil.com")
	rec3 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec3, req3)
	if rec3.Code != 403 {
		t.Fatalf("跨站 Origin 应 403: %d", rec3.Code)
	}
	// 本地：回环 + 同源 Origin 的写请求 → 不应是 403（具体 200/400 由业务逻辑决定）
	req4 := httptest.NewRequest("POST", "/api/local/modules", bytes.NewReader(body))
	req4.Host = "127.0.0.1:8080"
	req4.RemoteAddr = "127.0.0.1:1"
	req4.Header.Set("Content-Type", "application/json")
	req4.Header.Set("Origin", "http://127.0.0.1:8080")
	rec4 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec4, req4)
	if rec4.Code == 403 {
		t.Fatalf("回环同源 Origin 不该 403: %d", rec4.Code)
	}
}
