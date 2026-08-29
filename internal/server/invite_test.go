package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

func TestJoinFlow(t *testing.T) {
	ts, st, users := newTestServer(t)
	ch, _ := st.ChannelByName("deutschapp")
	code, err := st.CreateInvite(ch.ID, users["hou"].ID, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// join info 无需登录
	resp, _ := http.Get(ts.URL + "/api/join/" + code)
	var info api.JoinInfo
	json.NewDecoder(resp.Body).Decode(&info)
	if info.Channel != "deutschapp" {
		t.Fatalf("join info 不对: %+v", info)
	}
	// join 建号
	body, _ := json.Marshal(api.JoinRequest{Code: code, Username: "neu", DisplayName: "Neu", Password: "pw-neu-12"})
	resp, _ = http.Post(ts.URL+"/api/join", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 200 {
		t.Fatalf("join 应 200, got %d", resp.StatusCode)
	}
	var jr api.JoinResponse
	json.NewDecoder(resp.Body).Decode(&jr)
	if jr.AgentToken == "" || !strings.Contains(jr.LoginCmd, jr.AgentToken) ||
		!strings.Contains(jr.Guide, "neu") || jr.Channel != "deutschapp" {
		t.Fatalf("join 响应不完整: %+v", jr)
	}
	// 新用户已入频道且能登录
	u, err := st.Authenticate("neu", "pw-neu-12")
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := st.IsMember(ch.ID, u.ID)
	if !ok {
		t.Fatal("join 后应已是频道成员")
	}
	// 邀请码一次性
	body2, _ := json.Marshal(api.JoinRequest{Code: code, Username: "neu2", DisplayName: "Neu2", Password: "pw-neu-12"})
	resp, _ = http.Post(ts.URL+"/api/join", "application/json", bytes.NewReader(body2))
	if resp.StatusCode != 400 {
		t.Fatalf("重复使用邀请码应 400, got %d", resp.StatusCode)
	}
}

func TestCreateInviteHumanOnly(t *testing.T) {
	ts, _, users := newTestServer(t)
	req, _ := http.NewRequest("POST", ts.URL+"/api/invites?channel=deutschapp", nil)
	req.Header.Set("Authorization", "Bearer "+users["hou"].AgentToken)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Fatalf("agent 建邀请应 403, got %d", resp.StatusCode)
	}
	c := loginCookie(t, ts, "hou", "pw-hou")
	req, _ = http.NewRequest("POST", ts.URL+"/api/invites?channel=deutschapp", nil)
	req.AddCookie(c)
	resp, _ = http.DefaultClient.Do(req)
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	if !strings.Contains(out["url"], "/join/") {
		t.Fatalf("应返回邀请链接: %+v", out)
	}
}

func TestJoinDuplicateUsernameDoesNotBurnInvite(t *testing.T) {
	ts, st, users := newTestServer(t)
	ch, _ := st.ChannelByName("deutschapp")
	code, err := st.CreateInvite(ch.ID, users["hou"].ID, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Attempt to join with an already-taken username (hou)
	body, _ := json.Marshal(api.JoinRequest{Code: code, Username: "hou", DisplayName: "Hou", Password: "pw-hou-12"})
	resp, _ := http.Post(ts.URL+"/api/join", "application/json", bytes.NewReader(body))
	if resp.StatusCode != 400 {
		t.Fatalf("重名请求应返回 400, got %d", resp.StatusCode)
	}
	// Invite should still be redeemable with a different username
	body2, _ := json.Marshal(api.JoinRequest{Code: code, Username: "newuser", DisplayName: "NewUser", Password: "pw-newuser"})
	resp, _ = http.Post(ts.URL+"/api/join", "application/json", bytes.NewReader(body2))
	if resp.StatusCode != 200 {
		t.Fatalf("邀请码应仍可用, got %d", resp.StatusCode)
	}
	var jr api.JoinResponse
	json.NewDecoder(resp.Body).Decode(&jr)
	if jr.Username != "newuser" {
		t.Fatalf("用户名不对: %+v", jr)
	}
}
