package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubAdminServer 模拟 login（发 relais_session cookie）+ admin channel create/list。
func stubAdminServer(t *testing.T) *httptest.Server {
	t.Helper()
	var created []string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "relais_session", Value: "sess-xyz", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"username":"hou","display_name":"Hou","is_admin":true,"key":"human"}`))
	})
	mux.HandleFunc("POST /api/admin/channels", func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie("relais_session"); c == nil || c.Value != "sess-xyz" {
			w.WriteHeader(403)
			return
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		created = append(created, body["name"])
		w.Write([]byte(`{"name":"` + body["name"] + `","members":0}`))
	})
	mux.HandleFunc("GET /api/admin/channels", func(w http.ResponseWriter, r *http.Request) {
		if c, _ := r.Cookie("relais_session"); c == nil || c.Value != "sess-xyz" {
			w.WriteHeader(403)
			return
		}
		w.Write([]byte(`[{"name":"general","members":2}]`))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestAdminLoginAndChannelCommands(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	ts := stubAdminServer(t)
	// 直接写 admin 配置（跳过交互式密码）：用测试后门 saveAdminConfigForTest
	if err := saveAdminConfigForTest(ts.URL, "sess-xyz"); err != nil {
		t.Fatal(err)
	}
	// channel create
	if err := RunAdmin([]string{"channel", "create", "proj"}); err != nil {
		t.Fatalf("channel create 失败: %v", err)
	}
	// channel list（能跑通、无错）
	if err := RunAdmin([]string{"channel", "list"}); err != nil {
		t.Fatalf("channel list 失败: %v", err)
	}
	// 未登录时报清晰错误
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if err := RunAdmin([]string{"channel", "list"}); err == nil || !strings.Contains(err.Error(), "relais admin login") {
		t.Fatalf("未登录应提示 relais admin login: %v", err)
	}
}
