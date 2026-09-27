package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

// fakeLocal：记录调用的假管理器
type fakeLocal struct {
	modules []api.LocalModule
	rules   map[string]string
	created []api.LocalModuleRequest
	closed  []string
	set     api.LocalSettings
}

func (f *fakeLocal) ScanRepos() ([]api.LocalRepo, error) {
	return []api.LocalRepo{{Dir: "/Users/x/proj", Name: "proj"}}, nil
}
func (f *fakeLocal) ListModules() ([]api.LocalModule, error) { return f.modules, nil }
func (f *fakeLocal) CreateModule(name, dir string) (api.LocalModule, error) {
	if name == "bad name" {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("模块名含空格"))
	}
	f.created = append(f.created, api.LocalModuleRequest{Name: name, Dir: dir})
	m := api.LocalModule{Name: name, Dir: dir, Mode: "supervised", RoundCap: 8, State: "running"}
	f.modules = append(f.modules, m)
	return m, nil
}
func (f *fakeLocal) CloseModule(name string) error {
	if name == "nope" {
		return errors.Join(ErrLocalInvalid, errors.New("不存在"))
	}
	f.closed = append(f.closed, name)
	return nil
}
func (f *fakeLocal) Rules(name string) (string, error) {
	if _, ok := f.rules[name]; !ok {
		return "", errors.Join(ErrLocalInvalid, errors.New("不存在"))
	}
	return f.rules[name], nil
}
func (f *fakeLocal) PutRules(name, text string) error     { f.rules[name] = text; return nil }
func (f *fakeLocal) Settings() (api.LocalSettings, error) { return f.set, nil }
func (f *fakeLocal) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode == "yolo" {
		return errors.Join(ErrLocalInvalid, errors.New("模式无效"))
	}
	f.set = s
	return nil
}

func newLocalTestServer(t *testing.T) (*httptest.Server, *Server, *fakeLocal, map[string]*store.User) {
	t.Helper()
	ts, st, users := newTestServer(t)
	ts.Close()
	f := &fakeLocal{rules: map[string]string{}, set: api.LocalSettings{ClaudePath: "/c", CodexPath: "/x", DefaultMode: "supervised"}}
	srv := New(st, "http://relais.test", t.TempDir())
	srv.SetLocal(f)
	ts2 := httptest.NewServer(srv.Handler())
	t.Cleanup(ts2.Close)
	return ts2, srv, f, users
}

func TestLocalRoutesAbsentWithoutManager(t *testing.T) {
	ts, _, users := newTestServer(t)
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	if r := humanDo(t, ts, cookie, "GET", "/api/local/modules", nil); r.StatusCode != 404 {
		t.Fatalf("未注入管理器时应 404（线上不暴露）, got %d", r.StatusCode)
	}
	// 心跳路由未注册时落到 catch-all "GET /"：POST 撞见方法不匹配的通配路径，
	// net/http.ServeMux（Go 1.22+）按规范回 405 而非 404（已用最小复现验证），
	// 但同样意味着接口不可用，满足"线上不暴露"的安全目标。
	if r := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/local/heartbeat", nil); r.StatusCode != 405 {
		t.Fatalf("心跳未注册时应 405（方法不匹配 catch-all）, got %d", r.StatusCode)
	}
}

func TestLocalRoutesHumanAndLoopbackOnly(t *testing.T) {
	ts, srv, _, users := newLocalTestServer(t)
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	// agent token → 403
	for _, ep := range []struct{ m, p string }{{"GET", "/api/local/repos"}, {"GET", "/api/local/modules"}, {"POST", "/api/local/modules"}, {"POST", "/api/local/modules/x/close"}, {"GET", "/api/local/modules/x/rules"}, {"PUT", "/api/local/modules/x/rules"}, {"GET", "/api/local/settings"}, {"PUT", "/api/local/settings"}} {
		if r := agentDo(t, ts, users["hou"].AgentToken, ep.m, ep.p, map[string]string{}); r.StatusCode != 403 {
			t.Fatalf("agent %s %s 应 403, got %d", ep.m, ep.p, r.StatusCode)
		}
	}
	// 人钥匙但非回环 → 403（直接调 handler 伪造 RemoteAddr）
	req := httptest.NewRequest("GET", "/api/local/modules", nil)
	req.AddCookie(cookie)
	req.RemoteAddr = "10.0.0.5:4321"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("非回环应 403, got %d", rec.Code)
	}
	// 人钥匙 + 回环（httptest 客户端就是 127.0.0.1）→ 200
	if r := humanDo(t, ts, cookie, "GET", "/api/local/modules", nil); r.StatusCode != 200 {
		t.Fatalf("回环+人钥匙应 200, got %d", r.StatusCode)
	}
	// 心跳：人钥匙 403，agent 204
	if r := humanDo(t, ts, cookie, "POST", "/api/local/heartbeat", nil); r.StatusCode != 403 {
		t.Fatalf("人发心跳应 403, got %d", r.StatusCode)
	}
	if r := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/local/heartbeat", nil); r.StatusCode != 204 {
		t.Fatalf("agent 心跳应 204, got %d", r.StatusCode)
	}
}

func TestLocalModulesCRUDAndHeartbeat(t *testing.T) {
	ts, _, f, users := newLocalTestServer(t)
	cookie := loginCookie(t, ts, "hou", "pw-hou")
	r := humanDo(t, ts, cookie, "GET", "/api/local/repos", nil)
	var repos []api.LocalRepo
	json.NewDecoder(r.Body).Decode(&repos)
	if len(repos) != 1 || repos[0].Name != "proj" {
		t.Fatalf("repos 错: %+v", repos)
	}
	r = humanDo(t, ts, cookie, "POST", "/api/local/modules", api.LocalModuleRequest{Name: "grammar", Dir: "/Users/x/proj"})
	if r.StatusCode != 200 || len(f.created) != 1 {
		t.Fatalf("创建应 200, got %d %v", r.StatusCode, f.created)
	}
	r = humanDo(t, ts, cookie, "POST", "/api/local/modules", api.LocalModuleRequest{Name: "bad name", Dir: "/x"})
	var e api.ErrorResponse
	json.NewDecoder(r.Body).Decode(&e)
	if r.StatusCode != 400 || !strings.Contains(e.Error, "空格") {
		t.Fatalf("非法输入应 400 带文案, got %d %q", r.StatusCode, e.Error)
	}
	// 心跳：hou 的 agent 报活后（测试世界里没有 claude/codex 用户，用 hou 代表一侧）
	agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/local/heartbeat", nil)
	r = humanDo(t, ts, cookie, "GET", "/api/local/modules", nil)
	var mods []api.LocalModule
	json.NewDecoder(r.Body).Decode(&mods)
	if len(mods) != 1 || !mods[0].BridgeAlive["hou"] || mods[0].LastHeartbeat["hou"].IsZero() || mods[0].BridgeAlive["wu"] {
		t.Fatalf("心跳应体现在模块列表: %+v", mods)
	}
	// rules
	if r := humanDo(t, ts, cookie, "PUT", "/api/local/modules/grammar/rules", api.LocalRules{Text: "- 规矩"}); r.StatusCode != 204 {
		t.Fatalf("PUT rules 应 204, got %d", r.StatusCode)
	}
	r = humanDo(t, ts, cookie, "GET", "/api/local/modules/grammar/rules", nil)
	var rules api.LocalRules
	json.NewDecoder(r.Body).Decode(&rules)
	if rules.Text != "- 规矩" {
		t.Fatalf("rules 回读错: %+v", rules)
	}
	if r := humanDo(t, ts, cookie, "GET", "/api/local/modules/nope/rules", nil); r.StatusCode != 400 {
		t.Fatalf("不存在的模块 rules 应 400, got %d", r.StatusCode)
	}
	// settings
	if r := humanDo(t, ts, cookie, "PUT", "/api/local/settings", api.LocalSettings{ClaudePath: "/c2", CodexPath: "/x", DefaultMode: "autopilot"}); r.StatusCode != 204 || f.set.ClaudePath != "/c2" {
		t.Fatalf("PUT settings 应 204 并生效, got %d %+v", r.StatusCode, f.set)
	}
	if r := humanDo(t, ts, cookie, "PUT", "/api/local/settings", api.LocalSettings{DefaultMode: "yolo"}); r.StatusCode != 400 {
		t.Fatalf("无效 settings 应 400, got %d", r.StatusCode)
	}
	// close
	if r := humanDo(t, ts, cookie, "POST", "/api/local/modules/grammar/close", nil); r.StatusCode != 204 || len(f.closed) != 1 {
		t.Fatalf("close 应 204, got %d", r.StatusCode)
	}
	if r := humanDo(t, ts, cookie, "POST", "/api/local/modules/nope/close", nil); r.StatusCode != 400 {
		t.Fatalf("close 不存在应 400, got %d", r.StatusCode)
	}
}
