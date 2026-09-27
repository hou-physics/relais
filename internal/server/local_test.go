package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

type fakeLocal struct {
	modules   []api.LocalModule
	created   []api.LocalModuleRequest
	patched   []api.LocalModulePatch
	closed    []string
	reopened  []string
	deleted   map[string]bool
	attached  []api.LocalAttachRequest
	redeliver []string
	set       api.LocalSettings
}

func (f *fakeLocal) ScanRepos() ([]api.LocalRepo, error) {
	return []api.LocalRepo{{Dir: "/Users/x/proj", Name: "proj"}}, nil
}
func (f *fakeLocal) ListModules() ([]api.LocalModule, error) { return f.modules, nil }
func (f *fakeLocal) CreateModule(req api.LocalModuleRequest) (api.LocalModule, error) {
	if strings.Contains(req.Name, "/") {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("模块名含斜杠"))
	}
	f.created = append(f.created, req)
	m := api.LocalModule{Name: req.Name, Dir: req.Dir, Mode: "supervised", RoundCap: 8, State: "未接入"}
	f.modules = append(f.modules, m)
	return m, nil
}
func (f *fakeLocal) PatchModule(name string, p api.LocalModulePatch) (api.LocalModule, error) {
	if p.Name == "taken" {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("名字已存在"))
	}
	f.patched = append(f.patched, p)
	return api.LocalModule{Name: name}, nil
}
func (f *fakeLocal) CloseModule(name string) error {
	if name == "nope" {
		return errors.Join(ErrLocalInvalid, errors.New("不存在"))
	}
	f.closed = append(f.closed, name)
	return nil
}
func (f *fakeLocal) ReopenModule(name string) error {
	f.reopened = append(f.reopened, name)
	return nil
}
func (f *fakeLocal) DeleteModule(name string, files bool) error {
	if f.deleted == nil {
		f.deleted = map[string]bool{}
	}
	f.deleted[name] = files
	return nil
}
func (f *fakeLocal) Conversations(side, dir string) ([]api.LocalConversation, error) {
	return []api.LocalConversation{{ID: side + "-1", Name: "对话", Cwd: dir, UpdatedAt: time.Now()}}, nil
}
func (f *fakeLocal) AttachModule(name string, req api.LocalAttachRequest) (api.LocalModule, error) {
	if req.Side != "codex" {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("只有 codex 侧需要登记"))
	}
	f.attached = append(f.attached, req)
	return api.LocalModule{Name: name, Codex: api.LocalSide{Attached: true, ThreadName: "对话"}}, nil
}
func (f *fakeLocal) Redeliver(name string) error          { f.redeliver = append(f.redeliver, name); return nil }
func (f *fakeLocal) Settings() (api.LocalSettings, error) { return f.set, nil }
func (f *fakeLocal) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode == "yolo" {
		return errors.Join(ErrLocalInvalid, errors.New("模式无效"))
	}
	f.set = s
	return nil
}
func (f *fakeLocal) State() api.LocalState { return api.LocalState{Version: "test", CodexOK: true} }

func newLocalTestServer(t *testing.T) (*httptest.Server, *Server, *fakeLocal, map[string]*store.User) {
	t.Helper()
	ts, st, users := newTestServer(t)
	ts.Close()
	f := &fakeLocal{set: api.LocalSettings{CodexPath: "/x", DefaultMode: "supervised", DefaultCap: 8}}
	srv := New(st, "http://relais.test", t.TempDir())
	srv.SetLocal(f, "hou")
	ts2 := httptest.NewServer(srv.Handler())
	t.Cleanup(ts2.Close)
	return ts2, srv, f, users
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, ts.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLocalRoutesAbsentWithoutManager(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, p := range []string{"/api/local/modules", "/api/local/state", "/local/app.js"} {
		resp, _ := http.Get(ts.URL + p)
		if resp.StatusCode == 200 {
			t.Fatalf("线上不该有 %s: %d", p, resp.StatusCode)
		}
	}
	resp, _ := http.Get(ts.URL + "/")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `id="login-view"`) {
		t.Fatal("线上首页应仍是联网页面（含登录）")
	}
}

func TestLocalRoutesLoopbackOnly(t *testing.T) {
	ts, srv, _, _ := newLocalTestServer(t)
	if r := do(t, ts, "GET", "/api/local/modules", nil); r.StatusCode != 200 {
		t.Fatalf("回环免钥匙应 200: %d", r.StatusCode)
	}
	req := httptest.NewRequest("GET", "/api/local/modules", nil)
	req.RemoteAddr = "10.0.0.5:4321"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("非回环应 401: %d", rec.Code)
	}
	if r := do(t, ts, "POST", "/api/local/heartbeat", nil); r.StatusCode != 404 {
		t.Fatalf("心跳路由应已删除: %d", r.StatusCode)
	}
	if r := do(t, ts, "GET", "/api/local/modules/x/rules", nil); r.StatusCode != 404 {
		t.Fatalf("规矩路由应已删除: %d", r.StatusCode)
	}
}

func TestLocalModuleRoutes(t *testing.T) {
	ts, _, f, _ := newLocalTestServer(t)
	if r := do(t, ts, "POST", "/api/local/modules", api.LocalModuleRequest{Name: "m", Dir: "/Users/x/proj", CodexThread: "t1"}); r.StatusCode != 200 {
		t.Fatalf("create: %d", r.StatusCode)
	}
	if len(f.created) != 1 || f.created[0].CodexThread != "t1" {
		t.Fatalf("create 参数没传全: %+v", f.created)
	}
	if r := do(t, ts, "POST", "/api/local/modules", api.LocalModuleRequest{Name: "a/b", Dir: "/x"}); r.StatusCode != 400 {
		t.Fatalf("invalid 应 400: %d", r.StatusCode)
	}
	if r := do(t, ts, "PATCH", "/api/local/modules/m", api.LocalModulePatch{Name: "m2", RoundCap: 10}); r.StatusCode != 200 || f.patched[0].RoundCap != 10 {
		t.Fatalf("patch: %d %+v", r.StatusCode, f.patched)
	}
	if r := do(t, ts, "PATCH", "/api/local/modules/m", api.LocalModulePatch{Name: "taken"}); r.StatusCode != 400 {
		t.Fatalf("重名应 400: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/close", nil); r.StatusCode != 204 || f.closed[0] != "m" {
		t.Fatalf("close: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/reopen", nil); r.StatusCode != 204 || f.reopened[0] != "m" {
		t.Fatalf("reopen: %d", r.StatusCode)
	}
	if r := do(t, ts, "DELETE", "/api/local/modules/m?files=1", nil); r.StatusCode != 204 || !f.deleted["m"] {
		t.Fatalf("delete files=1: %d %v", r.StatusCode, f.deleted)
	}
	if r := do(t, ts, "DELETE", "/api/local/modules/n", nil); r.StatusCode != 204 || f.deleted["n"] {
		t.Fatalf("delete 默认不删文件: %d %v", r.StatusCode, f.deleted)
	}
	r := do(t, ts, "GET", "/api/local/conversations?side=codex&dir=/Users/x/proj", nil)
	var convs []api.LocalConversation
	json.NewDecoder(r.Body).Decode(&convs)
	if r.StatusCode != 200 || len(convs) != 1 || convs[0].ID != "codex-1" {
		t.Fatalf("conversations: %d %+v", r.StatusCode, convs)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/attach", api.LocalAttachRequest{Side: "codex", Thread: "t9"}); r.StatusCode != 200 || f.attached[0].Thread != "t9" {
		t.Fatalf("attach: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/attach", api.LocalAttachRequest{Side: "claude"}); r.StatusCode != 400 {
		t.Fatalf("claude attach 应 400: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/redeliver", nil); r.StatusCode != 204 || f.redeliver[0] != "m" {
		t.Fatalf("redeliver: %d", r.StatusCode)
	}
	if r := do(t, ts, "PUT", "/api/local/settings", api.LocalSettings{CodexPath: "/c", DefaultMode: "autopilot", DefaultCap: 6, NotifyEveryLetter: true}); r.StatusCode != 204 || f.set.DefaultCap != 6 || !f.set.NotifyEveryLetter {
		t.Fatalf("settings: %d %+v", r.StatusCode, f.set)
	}
	if r := do(t, ts, "PUT", "/api/local/settings", api.LocalSettings{DefaultMode: "yolo"}); r.StatusCode != 400 {
		t.Fatalf("坏设置应 400: %d", r.StatusCode)
	}
	r = do(t, ts, "GET", "/api/local/state", nil)
	var stt api.LocalState
	json.NewDecoder(r.Body).Decode(&stt)
	if r.StatusCode != 200 || stt.Version != "test" || !stt.CodexOK {
		t.Fatalf("state: %d %+v", r.StatusCode, stt)
	}
}

func TestLocalPagesServedAtRoot(t *testing.T) {
	ts, _, _, _ := newLocalTestServer(t)
	resp, _ := http.Get(ts.URL + "/")
	b, _ := io.ReadAll(resp.Body)
	s := string(b)
	if resp.StatusCode != 200 || !strings.Contains(s, "/local/app.js") || strings.Contains(s, `id="login-view"`) {
		t.Fatalf("本地首页应是控制台、无登录: %d", resp.StatusCode)
	}
	for _, p := range []string{"/local/app.js", "/local/style.css", "/vendor/marked.min.js"} {
		resp, _ := http.Get(ts.URL + p)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
	}
}
