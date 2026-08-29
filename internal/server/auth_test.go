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
