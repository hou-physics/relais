# Relais M8 Implementation Plan（本地控制台 + 双击安装）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Hou 日常不敲任何命令：双击一个文件完成安装与常驻，此后新建模块、开题、看状态、复制开工指令全在 `http://127.0.0.1:8080` 的网页里；工作脑靠 `AGENT.md` 自己执行"拿去讨论 / 开工"。

**Architecture:** 把 `internal/cli/local.go` 里 `runLocalInit` 的逻辑拆成可复用的环境层（bootstrap）与模块层（create/list/close/rules/settings），封装成一个实现 `server.LocalManager` 接口的类型，由 `RunServe` 在 `server.toml` 有 `local_dir` 时注入服务器；服务器挂 `/api/local/*`（人钥匙 + 回环双重限制，线上不注册）并在内存记 bridge 心跳；bridge 每轮重读登记表并报心跳；接话规则认信首行 `@claude/@codex`；网页加「模块」页、「开题」框、「复制开工指令」；根目录放双击安装脚本调用新子命令 `relais local bootstrap`。

**Tech Stack:** Go ≥1.22，无新增依赖；bash 安装脚本 + osascript 对话框；原生 JS 网页；launchd。

**Spec:** `docs/superpowers/specs/2026-09-27-relais-m8-console-design.md`（必读）；术语 `CONTEXT.md`；决策 D43。M7 为基线（分支 88e43f9+）。

## Global Constraints

- 无新增 Go 依赖；M1–M7 测试与锚点一行不改、全绿；每任务结束 `./scripts/check.sh` 绿（含 `node --check` 与新加的安装脚本 `bash -n`）。
- **联网层一行不动（D35）**：线上服务器（`local_dir` 为空）不注册任何 `/api/local/*` 路由（锚点：`GET /api/local/modules` → 404）；`installService`、`writeHook`、`RunSetup` 不改。
- **本地接口安全不变量（spec §8）**：`/api/local/*` 除 heartbeat 外只认人的钥匙（agent token → 403）且只认回环 `RemoteAddr`（否则 403）；heartbeat 只认 agent 钥匙；`CreateModule` 目录必须存在、绝对路径、不含 `..`；`PutRules` 只写 `<dir>/relais/RULES.md`；`PutSettings` 的路径必须是存在的普通文件。
- **执行期实现选择（记入 D44，Task 6）**：spec §3.2 说"抽成 `internal/local` 包"；因 `internal/cli` 已依赖 `internal/server`（RunServe），新包若被 server 依赖会成环。改为：server 定义 `LocalManager` 接口，`internal/cli/localmgr.go` 实现并由 `RunServe` 注入。共用逻辑仍只有一份，位置不同。
- 网页动态数据一律 `textContent`；三语 zh/en/de 同步；人的按钮统一走 `humanAction`。
- commit 前缀 feat:/fix:/test:/docs:；末尾 `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`；中文注释与错误文案。
- `const version` → `0.6.0-m8`（`main.go` 与 `internal/server/static.go`，Task 6）。

## File Structure

```
internal/api/types.go            # LocalRepo/LocalModule/LocalModuleRequest/LocalRules/LocalSettings（Task 1）
internal/cli/localmgr.go         # localManager：bootstrap 环境层 + 模块层（Task 1）；实现 server.LocalManager
internal/cli/local.go            # runLocalInit/status/close 改薄壳；新增 bootstrap 子命令（Task 1、5）
internal/cli/admin.go            # ServerConfig.LocalDir；RunServe 注入 manager（Task 1、2）
internal/server/local.go         # LocalManager 接口、ErrLocalInvalid、/api/local/* handlers、心跳表、回环判定（Task 2）
internal/server/server.go        # New 加 option；路由按 local 是否注入（Task 2）
internal/cli/bridge.go           # 每轮重读登记表 + 心跳（Task 3）
internal/cli/client.go           # Heartbeat 客户端方法（Task 3）
internal/cli/auto.go             # 接话规则认 @side 首行（Task 3）
internal/cli/localprompt.go      # 提示词加"首行 @ 是路由指示，忽略"（Task 3）
internal/server/web/{index.html,app.js,style.css}  # 模块页 / 开题框 / 复制开工指令 / 引导（Task 4）
安装 Relais 本地模式.command      # 双击安装脚本（Task 5）
scripts/check.sh                 # + bash -n 安装脚本（Task 5）
internal/guide/guide.go          # LocalText：工作脑本地模式说明（Task 6）
e2e/e2e_test.go                  # M8 锚点（Task 6）
README.md / docs/decisions.md    # 用法 + D44（Task 6）
main.go                          # version（Task 6）
```

---

### Task 1: 环境层 + 模块层（`localManager`）与 `ServerConfig.LocalDir`

**Files:**
- Create: `internal/cli/localmgr.go`
- Modify: `internal/cli/local.go`（`runLocalInit` 改为调用 manager；`runLocalStatus`/`runLocalClose` 改薄壳）
- Modify: `internal/cli/admin.go`（`ServerConfig` 加 `LocalDir string \`toml:"local_dir"\``）
- Modify: `internal/api/types.go`
- Test: `internal/cli/localmgr_test.go`（现有 `local_test.go` 的三个测试保持通过）

**Interfaces:**
- Produces（`internal/api`）：
  ```go
  type LocalRepo struct { Dir string `json:"dir"`; Name string `json:"name"`; ModifiedAt time.Time `json:"modified_at"` }
  type LocalModule struct {
      Name string `json:"name"`; Dir string `json:"dir"`; Mode string `json:"mode"`
      Round int `json:"round"`; RoundCap int `json:"round_cap"`; State string `json:"state"` // running|paused|needs_human|resolved|kicked_off|closed
      NeedsHumanQ string `json:"needs_human_q,omitempty"`; Conclusions int `json:"conclusions"`
      BridgeAlive map[string]bool `json:"bridge_alive"`           // 由服务器填（Task 2）
      LastHeartbeat map[string]time.Time `json:"last_heartbeat"`  // 由服务器填（Task 2）
  }
  type LocalModuleRequest struct { Name string `json:"name"`; Dir string `json:"dir"` }
  type LocalRules struct { Text string `json:"text"` }
  type LocalSettings struct { ClaudePath string `json:"claude_path"`; CodexPath string `json:"codex_path"`; DefaultMode string `json:"default_mode"` }
  ```
- Produces（`internal/cli`）：
  ```go
  type localManager struct{ ld string; scPath string }              // 每次操作自己 openServerStore，用完即关（与 serve 进程并存，WAL+immediate 已验证）
  func newLocalManager(ld string) *localManager
  type bootstrapResult struct{ BaseURL, HumanUser, HumanPassword string; PasswordShown bool } // 首次创建 hou 时 PasswordShown=true
  func (m *localManager) bootstrap(listen, claudePath, codexPath string) (bootstrapResult, error) // server.toml(含 local_dir)+三账号+两侧 config/setup/hook+settings(local.claude_path/codex_path/default_mode)；不建模块；幂等
  func (m *localManager) ScanRepos() ([]api.LocalRepo, error)      // $HOME 两层内含 .git 的目录，按 mtime 倒序，最多 50；跳过隐藏目录
  func (m *localManager) ListModules() ([]api.LocalModule, error)  // 所有 auto.enabled 或 closed 的频道；Dir 取 sides/claude/projects.toml；Conclusions = 统计 <dir>/relais/conclusions/<name>-*.md
  func (m *localManager) CreateModule(name, dir string) (api.LocalModule, error) // 频道+三成员+auto(16)+mode(默认)+两侧登记+项目 relais/{config(首次),RULES(若无),AGENT,conclusions}；幂等；非法输入返回 wrap 了 server.ErrLocalInvalid 的错误
  func (m *localManager) CloseModule(name string) error            // CloseChannel + 两侧 sessionClear；不存在 → ErrLocalInvalid
  func (m *localManager) Rules(name string) (string, error); func (m *localManager) PutRules(name, text string) error
  func (m *localManager) Settings() (api.LocalSettings, error); func (m *localManager) PutSettings(s api.LocalSettings) error // 路径须为存在的普通文件；改路径后重写两侧 hook 与 setup.toml
  ```
  `server.ErrLocalInvalid` 在 Task 2 定义；本任务先在 `internal/server/local.go` 里只放这一行 `var ErrLocalInvalid = errors.New("本地管理：输入无效")`（Task 2 再扩展该文件）。
- `runLocalInit` 保持全部现有 flag 与输出（`local_test.go` 不改），内部 = `bootstrap` + 逐模块 `CreateModule` + 常驻安装（常驻仍留在 local.go）。

- [ ] **Step 1: 写失败的测试**

`internal/cli/localmgr_test.go`：
```go
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

func newMgrForTest(t *testing.T) (*localManager, string) {
	t.Helper()
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	m := newLocalManager(ld)
	res, err := m.bootstrap("127.0.0.1:18099", "/bin/echo", "/bin/cat")
	if err != nil {
		t.Fatal(err)
	}
	if !res.PasswordShown || res.HumanPassword == "" || res.HumanUser != "hou" || res.BaseURL != "http://127.0.0.1:18099" {
		t.Fatalf("首次 bootstrap 结果错: %+v", res)
	}
	return m, ld
}

func TestBootstrapIdempotentAndWritesLocalDir(t *testing.T) {
	m, ld := newMgrForTest(t)
	res2, err := m.bootstrap("127.0.0.1:18099", "/bin/echo", "/bin/cat")
	if err != nil || res2.PasswordShown {
		t.Fatalf("第二次 bootstrap 应幂等且不再显示密码: %+v %v", res2, err)
	}
	data, _ := os.ReadFile(filepath.Join(ld, "server.toml"))
	if !strings.Contains(string(data), "local_dir = ") {
		t.Fatalf("server.toml 应含 local_dir: %s", data)
	}
	cfg, err := loadServerConfig(filepath.Join(ld, "server.toml"))
	if err != nil || cfg.LocalDir != ld {
		t.Fatalf("LocalDir 应可读回: %+v %v", cfg, err)
	}
	st, _ := store.Open(filepath.Join(ld, "data", "relais.db"))
	defer st.Close()
	for k, want := range map[string]string{"local.claude_path": "/bin/echo", "local.codex_path": "/bin/cat", "local.default_mode": "supervised"} {
		if v, _ := st.GetSetting(k); v != want {
			t.Fatalf("setting %s = %q, want %q", k, v, want)
		}
	}
	for _, side := range []string{"claude", "codex"} {
		if _, err := os.Stat(filepath.Join(ld, "sides", side, "hooks", "auto-reply.sh")); err != nil {
			t.Fatalf("%s hook 应存在", side)
		}
	}
	if !strings.HasPrefix(m.bootstrapMustFail("0.0.0.0:80"), "本地模式只允许监听回环") {
		t.Fatal("非回环应拒绝")
	}
}

// bootstrapMustFail 是测试小助手：返回错误文案
func (m *localManager) bootstrapMustFail(listen string) string {
	_, err := m.bootstrap(listen, "/bin/echo", "/bin/cat")
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestScanRepos(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	mk := func(rel string, git bool) {
		p := filepath.Join(home, rel)
		os.MkdirAll(p, 0o755)
		if git {
			os.MkdirAll(filepath.Join(p, ".git"), 0o755)
		}
	}
	mk("proj-a", true)
	mk("work/proj-b", true)
	mk("work/deep/proj-c", true) // 第三层，不取
	mk("plain", false)
	mk(".hidden/proj-d", true) // 隐藏目录，不取
	os.Chtimes(filepath.Join(home, "proj-a"), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	m, _ := newMgrForTest(t)
	repos, err := m.ScanRepos()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if strings.Join(names, ",") != "proj-b,proj-a" {
		t.Fatalf("应只列两层内的 git 仓库并按修改时间倒序: %v", names)
	}
	if repos[0].Dir != filepath.Join(home, "work", "proj-b") {
		t.Fatalf("Dir 应为绝对路径: %+v", repos[0])
	}
}

func TestCreateListCloseModule(t *testing.T) {
	m, ld := newMgrForTest(t)
	proj := t.TempDir()
	mod, err := m.CreateModule("grammar", proj)
	if err != nil {
		t.Fatal(err)
	}
	if mod.Name != "grammar" || mod.Dir != proj || mod.Mode != "supervised" || mod.RoundCap != 8 || mod.State != "running" {
		t.Fatalf("模块信息错: %+v", mod)
	}
	if _, err := m.CreateModule("grammar", proj); err != nil {
		t.Fatalf("重复创建应幂等: %v", err)
	}
	for _, side := range []string{"claude", "codex"} {
		ps, _ := loadProjectsIn(filepath.Join(ld, "sides", side))
		if len(ps) != 1 || ps[0].Channel != "grammar" || ps[0].Dir != proj {
			t.Fatalf("%s 侧登记错: %v", side, ps)
		}
	}
	for _, f := range []string{"relais/config.toml", "relais/RULES.md", "relais/AGENT.md", "relais/conclusions"} {
		if _, err := os.Stat(filepath.Join(proj, f)); err != nil {
			t.Fatalf("项目应有 %s", f)
		}
	}
	// 第二个模块共用目录：config.toml 不覆盖
	if _, err := m.CreateModule("reader", proj); err != nil {
		t.Fatal(err)
	}
	var pc ProjectConfig
	decodeTOMLFile(t, filepath.Join(proj, "relais", "config.toml"), &pc)
	if pc.Channel != "grammar" {
		t.Fatalf("默认频道应仍是第一个模块: %+v", pc)
	}
	// 非法输入
	for _, bad := range []struct{ name, dir string }{{"bad name", proj}, {"x", "/nonexistent/dir"}, {"x", proj + "/../" + filepath.Base(proj)}, {"x", "relative"}} {
		_, err := m.CreateModule(bad.name, bad.dir)
		if !errors.Is(err, server.ErrLocalInvalid) {
			t.Fatalf("%+v 应为 ErrLocalInvalid: %v", bad, err)
		}
	}
	// list
	mods, err := m.ListModules()
	if err != nil || len(mods) != 2 {
		t.Fatalf("应列出 2 个模块: %v %v", mods, err)
	}
	// 结论计数
	os.WriteFile(filepath.Join(proj, "relais", "conclusions", "grammar-01AAAAAAAAAAAAAAAAAAAAAAAA.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(proj, "relais", "conclusions", "reader-01BBBBBBBBBBBBBBBBBBBBBBBB.md"), []byte("x"), 0o644)
	mods, _ = m.ListModules()
	for _, md := range mods {
		if md.Conclusions != 1 {
			t.Fatalf("%s 结论数应为 1: %+v", md.Name, md)
		}
	}
	// close
	sessionSet(filepath.Join(ld, "sides", "claude"), "grammar", "sid")
	if err := m.CloseModule("grammar"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(filepath.Join(ld, "sides", "claude"), "grammar"); id != "" {
		t.Fatal("关闭应作废会话")
	}
	mods, _ = m.ListModules()
	for _, md := range mods {
		if md.Name == "grammar" && md.State != "closed" {
			t.Fatalf("应显示 closed: %+v", md)
		}
	}
	if err := m.CloseModule("nope"); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("关闭不存在的模块应 ErrLocalInvalid: %v", err)
	}
}

func TestRulesAndSettings(t *testing.T) {
	m, ld := newMgrForTest(t)
	proj := t.TempDir()
	m.CreateModule("grammar", proj)
	text, err := m.Rules("grammar")
	if err != nil || !strings.Contains(text, "铁律") {
		t.Fatalf("应读到模板: %q %v", text, err)
	}
	if err := m.PutRules("grammar", "- 不碰 8000 端口\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, "relais", "RULES.md"))
	if string(data) != "- 不碰 8000 端口\n" {
		t.Fatalf("RULES.md 应被覆盖: %q", data)
	}
	if _, err := m.Rules("nope"); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatal("不存在的模块应 ErrLocalInvalid")
	}
	s, _ := m.Settings()
	if s.ClaudePath != "/bin/echo" || s.CodexPath != "/bin/cat" || s.DefaultMode != "supervised" {
		t.Fatalf("settings 错: %+v", s)
	}
	before, _ := os.ReadFile(filepath.Join(ld, "sides", "claude", "hooks", "auto-reply.sh"))
	if err := m.PutSettings(api.LocalSettings{ClaudePath: "/bin/ls", CodexPath: "/bin/cat", DefaultMode: "autopilot"}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(ld, "sides", "claude", "hooks", "auto-reply.sh"))
	if string(before) == string(after) || !strings.Contains(string(after), "/bin/ls") {
		t.Fatal("改路径后应重写 hook")
	}
	s, _ = m.Settings()
	if s.ClaudePath != "/bin/ls" || s.DefaultMode != "autopilot" {
		t.Fatalf("settings 未更新: %+v", s)
	}
	for _, bad := range []api.LocalSettings{{ClaudePath: "/nonexistent", CodexPath: "/bin/cat", DefaultMode: "supervised"}, {ClaudePath: "/bin/ls", CodexPath: "/bin/cat", DefaultMode: "yolo"}, {ClaudePath: t.TempDir(), CodexPath: "/bin/cat", DefaultMode: "supervised"}} {
		if err := m.PutSettings(bad); !errors.Is(err, server.ErrLocalInvalid) {
			t.Fatalf("%+v 应 ErrLocalInvalid: %v", bad, err)
		}
	}
}
```
`decodeTOMLFile` 小助手（放测试文件里）：
```go
func decodeTOMLFile(t *testing.T, path string, v any) {
	t.Helper()
	if _, err := toml.DecodeFile(path, v); err != nil {
		t.Fatal(err)
	}
}
```
（import `"github.com/BurntSushi/toml"`。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run 'TestBootstrap|TestScanRepos|TestCreateListClose|TestRulesAndSettings' -v`
Expected: 编译错误 `undefined: newLocalManager`。

- [ ] **Step 3: 实现**

(a) `internal/api/types.go` 追加上面 Interfaces 里的五个类型（import `time`）。

(b) `internal/server/local.go` 新建，仅：
```go
package server

import "errors"

// ErrLocalInvalid：本地管理接口的"输入无效"哨兵，handler 据此回 400 而非 500。
var ErrLocalInvalid = errors.New("本地管理：输入无效")
```

(c) `internal/cli/admin.go` `ServerConfig` 加 `LocalDir string \`toml:"local_dir"\``（`loadServerConfig` 不强制非空）。

(d) `internal/cli/localmgr.go`：
```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

// localManager：本地模式的环境层（bootstrap）与模块层（增/列/关/规矩/设置）。
// CLI（relais local …）与服务器（/api/local/*，经 server.LocalManager 接口）共用这一份逻辑（D44）。
type localManager struct {
	ld     string
	scPath string
}

func newLocalManager(ld string) *localManager {
	return &localManager{ld: ld, scPath: localServerConfigPath(ld)}
}

type bootstrapResult struct {
	BaseURL, HumanUser, HumanPassword string
	PasswordShown                     bool
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", server.ErrLocalInvalid, fmt.Sprintf(format, a...))
}

func (m *localManager) open() (*store.Store, *ServerConfig, error) {
	st, cfg, err := openServerStore(m.scPath)
	if err != nil {
		return nil, nil, err
	}
	if !isLoopbackListen(cfg.Listen) {
		st.Close()
		return nil, nil, fmt.Errorf("%s 里的 listen = %q 不是回环地址；本地模式只允许 127.0.0.1/localhost", m.scPath, cfg.Listen)
	}
	return st, cfg, nil
}

func (m *localManager) sideDir(side string) string { return filepath.Join(m.ld, "sides", side) }

// bootstrap：只做"环境"——server.toml（含 local_dir）、三账号、两侧配置与 hook、设置；不建模块；幂等。
func (m *localManager) bootstrap(listen, claudePath, codexPath string) (bootstrapResult, error) {
	var res bootstrapResult
	if !isLoopbackListen(listen) {
		return res, fmt.Errorf("本地模式只允许监听回环地址，得到 %q", listen)
	}
	agents := map[string]string{"claude": claudePath, "codex": codexPath}
	for name, p := range agents {
		abs, err := checkAgentPath(name, p)
		if err != nil {
			return res, err
		}
		agents[name] = abs
	}
	if err := os.MkdirAll(m.ld, 0o700); err != nil {
		return res, err
	}
	if _, err := os.Stat(m.scPath); os.IsNotExist(err) {
		sc := fmt.Sprintf("listen = %q\ndata_dir = %q\nbase_url = %q\nlocal_dir = %q\n", listen, filepath.Join(m.ld, "data"), "http://"+listen, m.ld)
		if err := os.WriteFile(m.scPath, []byte(sc), 0o600); err != nil {
			return res, err
		}
	} else if err := ensureLocalDirLine(m.scPath, m.ld); err != nil { // 旧版 server.toml 没有 local_dir，补上
		return res, err
	}
	st, cfg, err := m.open()
	if err != nil {
		return res, err
	}
	defer st.Close()
	res.BaseURL = cfg.BaseURL
	res.HumanUser = localHuman
	ensureUser := func(name, display string, admin bool) (*store.User, error) {
		if u, err := st.UserByName(name); err == nil {
			if admin && !u.IsAdmin { // 上次中途失败的补救
				if err := st.SetAdmin(u.ID, true); err != nil {
					return nil, err
				}
			}
			return u, nil
		}
		pw := newPassword()
		u, err := st.CreateUser(name, display, pw)
		if err != nil {
			return nil, err
		}
		if admin {
			if err := st.SetAdmin(u.ID, true); err != nil {
				return nil, err
			}
			note := fmt.Sprintf("网页 %s 登录账号: %s\n初始密码: %s\n（本文件仅首次创建时写入；改密码后可删）\n", cfg.BaseURL, name, pw)
			if err := os.WriteFile(filepath.Join(m.ld, "human.txt"), []byte(note), 0o600); err != nil {
				return nil, err
			}
			res.HumanPassword, res.PasswordShown = pw, true
		}
		return u, nil
	}
	claudeU, err := ensureUser("claude", "Claude 侧", false)
	if err != nil {
		return res, err
	}
	codexU, err := ensureUser("codex", "Codex 侧", false)
	if err != nil {
		return res, err
	}
	if _, err := ensureUser(localHuman, "Hou", true); err != nil {
		return res, err
	}
	for side, u := range map[string]*store.User{"claude": claudeU, "codex": codexU} {
		if err := m.writeSide(side, cfg.BaseURL, u.AgentToken, agents[side]); err != nil {
			return res, err
		}
	}
	for k, v := range map[string]string{"local.claude_path": agents["claude"], "local.codex_path": agents["codex"]} {
		if err := st.SetSetting(k, v); err != nil {
			return res, err
		}
	}
	if v, _ := st.GetSetting("local.default_mode"); v == "" {
		if err := st.SetSetting("local.default_mode", "supervised"); err != nil {
			return res, err
		}
	}
	return res, nil
}

func checkAgentPath(name, p string) (string, error) {
	if p == "" {
		return "", invalid("没找到 %s：请指定路径（Codex 常在 ~/.codex/plugins/.plugin-appserver/codex）", name)
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "", invalid("%s 路径 %q 不存在或不是普通文件", name, p)
	}
	abs, _ := filepath.Abs(p)
	return abs, nil
}

// ensureLocalDirLine：server.toml 缺 local_dir 时追加一行（M7 生成的旧文件）。
func ensureLocalDirLine(scPath, ld string) error {
	data, err := os.ReadFile(scPath)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "local_dir") {
		return nil
	}
	return os.WriteFile(scPath, append(data, []byte(fmt.Sprintf("local_dir = %q\n", ld))...), 0o600)
}

// writeSide：某一侧的 config.toml / setup.toml / hook。
func (m *localManager) writeSide(side, baseURL, token, agentPath string) error {
	d := m.sideDir(side)
	if err := saveGlobalTo(d, &GlobalConfig{Server: baseURL, Token: token, Username: side}); err != nil {
		return err
	}
	info := SetupInfo{OS: runtime.GOOS, Agent: side, AgentPath: agentPath, Mode: "auto"}
	hp, err := writeLocalHook(d, info)
	if err != nil {
		return err
	}
	info.HookPath = hp
	return saveSetupTo(d, info)
}

func (m *localManager) ScanRepos() ([]api.LocalRepo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []api.LocalRepo
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(filepath.Join(p, ".git")); err == nil && st.IsDir() {
				info, _ := e.Info()
				out = append(out, api.LocalRepo{Dir: p, Name: e.Name(), ModifiedAt: info.ModTime()})
				continue
			}
			if depth > 1 {
				walk(p, depth-1)
			}
		}
	}
	walk(home, 2)
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

func validModuleName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " /\\\t\n") && len(name) <= 64
}

func (m *localManager) CreateModule(name, dir string) (api.LocalModule, error) {
	var out api.LocalModule
	if !validModuleName(name) {
		return out, invalid("模块名 %q 不能为空、含空格或斜杠", name)
	}
	if !filepath.IsAbs(dir) || strings.Contains(dir, "..") {
		return out, invalid("目录必须是绝对路径且不含 ..")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return out, invalid("目录 %q 不存在", dir)
	}
	st, cfg, err := m.open()
	if err != nil {
		return out, err
	}
	defer st.Close()
	users := map[string]*store.User{}
	for _, n := range []string{"claude", "codex", localHuman} {
		u, err := st.UserByName(n)
		if err != nil {
			return out, fmt.Errorf("账号 %s 不存在，请先运行安装（relais local bootstrap）", n)
		}
		users[n] = u
	}
	ch, err := st.ChannelByName(name)
	if err != nil {
		if ch, err = st.CreateChannel(name); err != nil {
			return out, err
		}
	}
	for _, u := range users {
		if ok, _ := st.IsMember(ch.ID, u.ID); !ok {
			if err := st.AddMember(ch.ID, u.ID); err != nil {
				return out, err
			}
		}
	}
	mode, _ := st.GetSetting("local.default_mode")
	if mode == "" {
		mode = "supervised"
	}
	if a, _ := st.GetAuto(ch.ID); !a.Enabled {
		if err := st.SetAutoEnabled(ch.ID, true, 16); err != nil { // 16 条 = 8 回合（D39/D42 ①）
			return out, err
		}
		if err := st.SetMode(ch.ID, mode); err != nil {
			return out, err
		}
	}
	for _, side := range []string{"claude", "codex"} {
		if err := registerProjectIn(m.sideDir(side), name, dir); err != nil {
			return out, err
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "relais", "config.toml")); os.IsNotExist(err) {
		if _, err := initProject(dir, cfg.BaseURL, name, "claude"); err != nil {
			return out, err
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "relais", "conclusions"), 0o755); err != nil {
		return out, err
	}
	rules := filepath.Join(dir, "relais", "RULES.md")
	if _, err := os.Stat(rules); os.IsNotExist(err) {
		if err := os.WriteFile(rules, []byte(rulesTemplate), 0o644); err != nil {
			return out, err
		}
	}
	if err := writeLocalAgentGuide(dir, m.ld, name); err != nil { // Task 6 才有 guide.LocalText；本任务先写占位实现见下
		return out, err
	}
	return m.moduleInfo(st, name, dir)
}

// writeLocalAgentGuide：把本地模式的工作脑说明追加到 <dir>/relais/AGENT.md（Task 6 换成 guide.LocalText；本任务只保证文件存在并含标记行）。
func writeLocalAgentGuide(dir, ld, name string) error {
	p := filepath.Join(dir, "relais", "AGENT.md")
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "## 本地模式") {
		return nil
	}
	add := fmt.Sprintf("\n## 本地模式（模块 %s）\n本侧配置目录：%s\n", name, filepath.Join(ld, "sides", "<claude|codex>"))
	return os.WriteFile(p, append(data, []byte(add)...), 0o644)
}

func (m *localManager) moduleInfo(st *store.Store, name, dir string) (api.LocalModule, error) {
	ch, err := st.ChannelByName(name)
	if err != nil {
		return api.LocalModule{}, invalid("模块 %q 不存在", name)
	}
	a, err := st.GetAuto(ch.ID)
	if err != nil {
		return api.LocalModule{}, err
	}
	state := "running"
	switch {
	case a.Closed:
		state = "closed"
	case a.KickedOff:
		state = "kicked_off"
	case a.Resolved:
		state = "resolved"
	case a.NeedsHumanQ != "":
		state = "needs_human"
	case a.Paused:
		state = "paused"
	}
	n := 0
	if entries, err := os.ReadDir(filepath.Join(dir, "relais", "conclusions")); err == nil {
		for _, e := range entries {
			if _, ok := conclusionIDFor(e.Name(), name); ok {
				n++
			}
		}
	}
	return api.LocalModule{Name: name, Dir: dir, Mode: a.Mode, Round: store.Round(a.RoundCount), RoundCap: store.Round(a.Cap),
		State: state, NeedsHumanQ: a.NeedsHumanQ, Conclusions: n, BridgeAlive: map[string]bool{}, LastHeartbeat: map[string]time.Time{}}, nil
}

// conclusionIDFor：文件名是否为 <channel>-<26位ULID>.md（与 conclusion.go 的规则一致）。
func conclusionIDFor(filename, channel string) (string, bool) {
	rest := strings.TrimSuffix(strings.TrimPrefix(filename, channel+"-"), ".md")
	if !strings.HasPrefix(filename, channel+"-") || !strings.HasSuffix(filename, ".md") || len(rest) != 26 || strings.Contains(rest, "-") {
		return "", false
	}
	return rest, true
}

func (m *localManager) ListModules() ([]api.LocalModule, error) {
	st, _, err := m.open()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	dirs := map[string]string{}
	if ps, err := loadProjectsIn(m.sideDir("claude")); err == nil {
		for _, p := range ps {
			dirs[p.Channel] = p.Dir
		}
	}
	chs, err := st.AllChannels()
	if err != nil {
		return nil, err
	}
	out := []api.LocalModule{}
	for _, c := range chs {
		ch, err := st.ChannelByName(c.Name)
		if err != nil {
			continue
		}
		a, _ := st.GetAuto(ch.ID)
		if !a.Enabled && !a.Closed {
			continue
		}
		info, err := m.moduleInfo(st, c.Name, dirs[c.Name])
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *localManager) CloseModule(name string) error {
	st, _, err := m.open()
	if err != nil {
		return err
	}
	defer st.Close()
	ch, err := st.ChannelByName(name)
	if err != nil {
		return invalid("模块 %q 不存在", name)
	}
	if err := st.CloseChannel(ch.ID); err != nil {
		return err
	}
	for _, side := range []string{"claude", "codex"} {
		if err := sessionClear(m.sideDir(side), name); err != nil {
			return err
		}
	}
	return nil
}

func (m *localManager) moduleDir(name string) (string, error) {
	ps, err := loadProjectsIn(m.sideDir("claude"))
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if p.Channel == name {
			return p.Dir, nil
		}
	}
	return "", invalid("模块 %q 不存在", name)
}

func (m *localManager) Rules(name string) (string, error) {
	dir, err := m.moduleDir(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "relais", "RULES.md"))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

func (m *localManager) PutRules(name, text string) error {
	dir, err := m.moduleDir(name)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "relais", "RULES.md"), []byte(text), 0o644)
}

func (m *localManager) Settings() (api.LocalSettings, error) {
	st, _, err := m.open()
	if err != nil {
		return api.LocalSettings{}, err
	}
	defer st.Close()
	var s api.LocalSettings
	s.ClaudePath, _ = st.GetSetting("local.claude_path")
	s.CodexPath, _ = st.GetSetting("local.codex_path")
	s.DefaultMode, _ = st.GetSetting("local.default_mode")
	if s.DefaultMode == "" {
		s.DefaultMode = "supervised"
	}
	return s, nil
}

func (m *localManager) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode != "supervised" && s.DefaultMode != "autopilot" {
		return invalid("默认模式只能是 supervised 或 autopilot")
	}
	paths := map[string]string{}
	for name, p := range map[string]string{"claude": s.ClaudePath, "codex": s.CodexPath} {
		abs, err := checkAgentPath(name, p)
		if err != nil {
			return err
		}
		paths[name] = abs
	}
	st, cfg, err := m.open()
	if err != nil {
		return err
	}
	defer st.Close()
	for k, v := range map[string]string{"local.claude_path": paths["claude"], "local.codex_path": paths["codex"], "local.default_mode": s.DefaultMode} {
		if err := st.SetSetting(k, v); err != nil {
			return err
		}
	}
	for _, side := range []string{"claude", "codex"} {
		u, err := st.UserByName(side)
		if err != nil {
			return err
		}
		if err := m.writeSide(side, cfg.BaseURL, u.AgentToken, paths[side]); err != nil {
			return err
		}
	}
	return nil
}
```

(e) `internal/cli/local.go`：`runLocalInit` 保留 flag 解析、`--project` 默认、`RELAIS_LOCAL_DIR`、常驻安装与结尾打印，中间改为：
```go
	mgr := newLocalManager(ld)
	claudeP, codexP := *claudePath, *codexPath
	if claudeP == "" {
		claudeP, _ = exec.LookPath("claude")
	}
	if codexP == "" {
		codexP, _ = exec.LookPath("codex")
	}
	res, err := mgr.bootstrap(*listen, claudeP, codexP)
	if err != nil {
		return err
	}
	if res.PasswordShown {
		fmt.Printf("网页 %s 登录账号: %s\n初始密码: %s（已存到 %s）\n", res.BaseURL, res.HumanUser, res.HumanPassword, shq(filepath.Join(ld, "human.txt")))
	}
	for _, m := range modules {
		if _, err := mgr.CreateModule(m, root); err != nil {
			return err
		}
	}
	baseURL := res.BaseURL
	scPath := localServerConfigPath(ld)
```
删除原来的身份/两侧/频道/项目绑定/RULES 段。`runLocalStatus` 改为 `newLocalManager(ld).ListModules()` 打印（保留服务器探活与会话 ✓ 列）；`runLocalClose` 改为 `mgr.CloseModule`。跑 `go test ./internal/cli/ -run TestLocal -v` 确认 M7 的三个测试仍绿（`TestLocalInitIsIdempotent` 断言的 `si.Agent/Mode/HookPath`、两侧 projects 各 2 条、`pc.Channel=="grammar"`、human.txt 都应仍成立）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/cli/ -run 'TestBootstrap|TestScanRepos|TestCreateListClose|TestRulesAndSettings|TestLocal' -v -race`
Expected: 全 PASS。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/api internal/cli internal/server/local.go
git commit -m "feat(cli): localManager 环境层/模块层（bootstrap、模块增列关、规矩、设置、仓库扫描）；ServerConfig.local_dir（M8 Task 1）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: 服务器 `/api/local/*`（接口注入、人钥匙 + 回环、心跳）

**Files:**
- Modify: `internal/server/local.go`（在 Task 1 的一行之上扩展）
- Modify: `internal/server/server.go`（`Server` 加 `local LocalManager`、`heartbeats`；`SetLocal`；路由）
- Modify: `internal/cli/admin.go`（`RunServe` 注入）
- Test: `internal/server/local_test.go`

**Interfaces:**
- Produces（`internal/server`）：
  ```go
  type LocalManager interface {
      ScanRepos() ([]api.LocalRepo, error)
      ListModules() ([]api.LocalModule, error)
      CreateModule(name, dir string) (api.LocalModule, error)
      CloseModule(name string) error
      Rules(name string) (string, error)
      PutRules(name, text string) error
      Settings() (api.LocalSettings, error)
      PutSettings(api.LocalSettings) error
  }
  func (s *Server) SetLocal(m LocalManager)   // 注入后 Handler() 才注册 /api/local/*
  ```
- 路由（全部 `s.auth` 之内）：
  | 方法 路径 | 守卫 | 响应 |
  |---|---|---|
  | `GET /api/local/repos` | 人 + 回环 | 200 `[]LocalRepo` |
  | `GET /api/local/modules` | 人 + 回环 | 200 `[]LocalModule`（服务器填 `bridge_alive`/`last_heartbeat`，alive = 20s 内有心跳） |
  | `POST /api/local/modules` | 人 + 回环 | 200 `LocalModule`；`ErrLocalInvalid` → 400 带文案；其他 → 500 |
  | `POST /api/local/modules/{name}/close` | 人 + 回环 | 204；不存在 → 400 |
  | `GET /api/local/modules/{name}/rules` | 人 + 回环 | 200 `LocalRules` |
  | `PUT /api/local/modules/{name}/rules` | 人 + 回环 | 204 |
  | `GET /api/local/settings` | 人 + 回环 | 200 `LocalSettings` |
  | `PUT /api/local/settings` | 人 + 回环 | 204；无效 → 400 |
  | `POST /api/local/heartbeat` | agent | 204；记 `p.user.Username → now` |
- `RunServe`：`if cfg.LocalDir != "" { srv.SetLocal(newLocalManager(cfg.LocalDir)) }`。

- [ ] **Step 1: 写失败的测试**

`internal/server/local_test.go`（用 `auth_test.go` 现有 `newTestServer/loginCookie/humanDo/agentDo`；为了造非回环请求，直接调 `Handler().ServeHTTP`）：
```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
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
func (f *fakeLocal) PutRules(name, text string) error { f.rules[name] = text; return nil }
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
	if r := agentDo(t, ts, users["hou"].AgentToken, "POST", "/api/local/heartbeat", nil); r.StatusCode != 404 {
		t.Fatalf("心跳也应 404, got %d", r.StatusCode)
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
	req.AddCookie(&http.Cookie{Name: "relais_session", Value: cookie})
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
```
注意 `humanDo`/`agentDo` 若不接受 `nil` body 给 PUT，请看 `auth_test.go` 的实现按需传空 map。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/ -run TestLocal -v`
Expected: 编译错误 `undefined: (*Server).SetLocal`。

- [ ] **Step 3: 实现**

`internal/server/local.go` 完整内容：
```go
package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

// ErrLocalInvalid：本地管理接口的"输入无效"哨兵，handler 据此回 400 而非 500。
var ErrLocalInvalid = errors.New("本地管理：输入无效")

// LocalManager：本地模式的环境/模块操作，由 cli 包实现并经 SetLocal 注入（D44）。
type LocalManager interface {
	ScanRepos() ([]api.LocalRepo, error)
	ListModules() ([]api.LocalModule, error)
	CreateModule(name, dir string) (api.LocalModule, error)
	CloseModule(name string) error
	Rules(name string) (string, error)
	PutRules(name, text string) error
	Settings() (api.LocalSettings, error)
	PutSettings(api.LocalSettings) error
}

const heartbeatAlive = 20 * time.Second

type heartbeats struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (h *heartbeats) beat(side string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last = map[string]time.Time{}
	}
	h.last[side] = time.Now()
}

func (h *heartbeats) snapshot() (alive map[string]bool, last map[string]time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	alive, last = map[string]bool{}, map[string]time.Time{}
	for k, v := range h.last {
		last[k] = v
		alive[k] = time.Since(v) < heartbeatAlive
	}
	return
}

// SetLocal 注入本地管理器；注入后 Handler() 才注册 /api/local/*（线上不暴露，spec §3.1）。
func (s *Server) SetLocal(m LocalManager) { s.local = m }

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// localHuman：人的钥匙 + 回环双重限制（spec §8）。
func (s *Server) localHuman(h func(http.ResponseWriter, *http.Request, principal)) func(http.ResponseWriter, *http.Request, principal) {
	return func(w http.ResponseWriter, r *http.Request, p principal) {
		if p.agent {
			writeErr(w, http.StatusForbidden, "本地管理仅限网页（人的钥匙）")
			return
		}
		if !isLoopback(r.RemoteAddr) {
			writeErr(w, http.StatusForbidden, "本地管理只接受本机请求")
			return
		}
		h(w, r, p)
	}
}

func (s *Server) localErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrLocalInvalid) {
		writeErr(w, http.StatusBadRequest, "%s", err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, "本地管理失败: %v", err)
}

func (s *Server) registerLocalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/local/repos", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		repos, err := s.local.ScanRepos()
		if err != nil {
			s.localErr(w, err)
			return
		}
		if repos == nil {
			repos = []api.LocalRepo{}
		}
		writeJSON(w, http.StatusOK, repos)
	})))
	mux.HandleFunc("GET /api/local/modules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		mods, err := s.local.ListModules()
		if err != nil {
			s.localErr(w, err)
			return
		}
		alive, last := s.beats.snapshot()
		for i := range mods {
			mods[i].BridgeAlive, mods[i].LastHeartbeat = alive, last
		}
		if mods == nil {
			mods = []api.LocalModule{}
		}
		writeJSON(w, http.StatusOK, mods)
	})))
	mux.HandleFunc("POST /api/local/modules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalModuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		m, err := s.local.CreateModule(req.Name, req.Dir)
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})))
	mux.HandleFunc("POST /api/local/modules/{name}/close", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		if err := s.local.CloseModule(r.PathValue("name")); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/local/modules/{name}/rules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		text, err := s.local.Rules(r.PathValue("name"))
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, api.LocalRules{Text: text})
	})))
	mux.HandleFunc("PUT /api/local/modules/{name}/rules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalRules
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		if err := s.local.PutRules(r.PathValue("name"), req.Text); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/local/settings", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		st, err := s.local.Settings()
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})))
	mux.HandleFunc("PUT /api/local/settings", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		if err := s.local.PutSettings(req); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("POST /api/local/heartbeat", s.auth(func(w http.ResponseWriter, r *http.Request, p principal) {
		if !p.agent {
			writeErr(w, http.StatusForbidden, "心跳仅限 agent 钥匙")
			return
		}
		s.beats.beat(p.user.Username)
		w.WriteHeader(http.StatusNoContent)
	}))
}
```
`server.go`：`Server` 结构加 `local LocalManager` 与 `beats heartbeats`；`Handler()` 末尾（`mux.Handle("GET /", …)` 之前）加 `if s.local != nil { s.registerLocalRoutes(mux) }`。`internal/cli/admin.go` `RunServe`：
```go
	srv := server.New(st, cfg.BaseURL, cfg.DataDir)
	if cfg.LocalDir != "" {
		srv.SetLocal(newLocalManager(cfg.LocalDir))
		fmt.Printf("本地模式管理接口已启用（%s）\n", cfg.LocalDir)
	}
	return http.ListenAndServe(cfg.Listen, srv.Handler())
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/server/ -v -race`
Expected: 全 PASS（含 M1–M7）。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/server internal/cli/admin.go
git commit -m "feat(server): /api/local/* 本地管理接口（LocalManager 注入、人钥匙+回环、心跳；线上不注册）（M8 Task 2）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: bridge 重读登记表 + 心跳；接话规则认 `@side`；提示词说明

**Files:**
- Modify: `internal/cli/bridge.go`（`RunBridge` 循环）
- Modify: `internal/cli/client.go`（`Heartbeat()`）
- Modify: `internal/cli/auto.go`（`humanMsgResponder`）
- Modify: `internal/cli/localprompt.go`（`localPrompt` 加一句）
- Test: `internal/cli/bridge_test.go`、`internal/cli/localturn_test.go`、`internal/cli/localprompt_test.go`

**Interfaces:**
- Produces：
  ```go
  func (c *Client) Heartbeat() error                       // POST /api/local/heartbeat；404（联网服务器）与网络错误均返回 nil 静默
  func loadBridgeTargets() ([]bridgeTarget, error)         // 从 loadProjects() 取有效目录；供 RunBridge 每轮调用
  func firstResponderHint(msgPath string) string           // 读 RELAIS_MSG_PATH 文件正文首行，匹配 ^@(claude|codex)\b 返回该侧，否则 ""
  ```
- `humanMsgResponder` 顺序：先 `firstResponderHint(os.Getenv("RELAIS_MSG_PATH"))`，非空则以它为 `who`（仍须在收件人里）；否则沿用原规则。
- `localPrompt` 在"## 本轮"段末加一句：`正文首行若以 @ 开头（如 @codex 先回），是给系统的路由指示，忽略它。`

- [ ] **Step 1: 写失败的测试**

`internal/cli/bridge_test.go` 追加：
```go
func TestBridgeTargetsReloadEachPoll(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "duo") // 已登记 duo → proj
	targets, err := loadBridgeTargets()
	if err != nil || len(targets) != 1 || targets[0].Channel != "duo" || targets[0].Dir != proj {
		t.Fatalf("初始应 1 个目标: %v %v", targets, err)
	}
	dir2 := t.TempDir()
	if err := registerProject("trio", dir2); err != nil {
		t.Fatal(err)
	}
	targets, _ = loadBridgeTargets()
	if len(targets) != 2 {
		t.Fatalf("登记新项目后重读应 2 个: %v", targets)
	}
	os.RemoveAll(dir2)
	targets, _ = loadBridgeTargets()
	if len(targets) != 1 {
		t.Fatalf("目录失效应被跳过: %v", targets)
	}
}

func TestHeartbeatSilentOn404(t *testing.T) {
	_, _, _ = setupCLITest(t, "hou", "duo") // 测试服务器未注入本地管理器 → 404
	c, _, _ := newClient()
	if err := c.Heartbeat(); err != nil {
		t.Fatalf("联网服务器无心跳路由时应静默: %v", err)
	}
}
```
`internal/cli/localturn_test.go` 追加（照该文件 `TestAutoTurnHumanMessageOnlyOneSideReplies` 的内联搭建方式：`setupCLITest` + 建 claude/codex 用户 + 频道 smoke + `as` 闭包切身份）：
```go
func TestFirstResponderHintOverridesRule(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	cl, _ := st.CreateUser("claude", "Claude 侧", "pw-c")
	cx, _ := st.CreateUser("codex", "Codex 侧", "pw-x")
	hou := users["hou"]
	ch, _ := st.CreateChannel("smoke")
	for _, u := range []*store.User{cl, cx, hou} {
		st.AddMember(ch.ID, u.ID)
	}
	st.SetAutoEnabled(ch.ID, true, 16)
	t.Setenv("RELAIS_CHANNEL", "smoke")
	g, _ := loadGlobal()
	as := func(u *store.User, from, msgID, msgPath string) error {
		t.Helper()
		if err := saveGlobal(&GlobalConfig{Server: g.Server, Token: u.AgentToken, Username: u.Username}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("RELAIS_MSG_FROM", from)
		t.Setenv("RELAIS_MSG_ID", msgID)
		t.Setenv("RELAIS_MSG_PATH", msgPath)
		return RunAutoTurn(nil)
	}
	// claude 刚说过话 → 默认该 codex 接；但人的信首行 "@claude 先回" → claude 接
	st.SaveMessageOpts(ch.ID, cl.ID, []int64{cx.ID}, "s", "x", "", store.SaveOpts{})
	body := "@claude 先回\n\n议题正文"
	h, _ := st.SaveMessageOpts(ch.ID, hou.ID, []int64{cl.ID, cx.ID}, "开题", body, "", store.SaveOpts{})
	msgFile := filepath.Join(proj, "relais", "inbox", "h.md")
	os.WriteFile(msgFile, msg.Render(msg.Envelope{ID: h.ID, Channel: "smoke", From: "hou", To: []string{"claude", "codex"}, Summary: "开题"}, body), 0o644)
	if got := firstResponderHint(msgFile); got != "claude" {
		t.Fatalf("应识别 @claude: %q", got)
	}
	if err := as(cx, "hou", h.ID, msgFile); err == nil || !strings.Contains(err.Error(), "由 claude 侧接话") {
		t.Fatalf("codex 应被拒: %v", err)
	}
	if err := as(cl, "hou", h.ID, msgFile); err != nil {
		t.Fatalf("claude 应放行: %v", err)
	}
	// @codex 但 codex 不在收件人里 → 不干预（收到的一侧照常接）
	body2 := "@codex 先回\n\n只发给 claude"
	h2, _ := st.SaveMessageOpts(ch.ID, hou.ID, []int64{cl.ID}, "s", body2, "", store.SaveOpts{})
	os.WriteFile(msgFile, msg.Render(msg.Envelope{ID: h2.ID, Channel: "smoke", From: "hou", To: []string{"claude"}, Summary: "s"}, body2), 0o644)
	if err := as(cl, "hou", h2.ID, msgFile); err != nil {
		t.Fatalf("接话方不在收件人里时收到的一侧应照常接: %v", err)
	}
	// 无 @ 行 → 原规则；文件不存在 → 空
	os.WriteFile(msgFile, []byte("---\nid: x\n---\n\n普通正文"), 0o644)
	if got := firstResponderHint(msgFile); got != "" {
		t.Fatalf("无 @ 行应空: %q", got)
	}
	if got := firstResponderHint("/nonexistent"); got != "" {
		t.Fatal("文件不存在应空")
	}
}
```
（import `os`、`path/filepath`、`strings`、`internal/msg`。）
`internal/cli/localprompt_test.go` 的 `TestLocalPromptContents` 想要列表加 `"@ 开头"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run 'TestBridgeTargetsReload|TestHeartbeat|TestFirstResponder|TestLocalPromptContents' -v`
Expected: 编译错误（`loadBridgeTargets`、`Heartbeat`、`firstResponderHint` 未定义）。

- [ ] **Step 3: 实现**

`client.go`：
```go
// Heartbeat：bridge 报活（本地控制台用）。联网服务器没有该路由（404）或网络错误都静默——心跳只是显示用。
func (c *Client) Heartbeat() error {
	_ = c.do("POST", "/api/local/heartbeat", nil, nil)
	return nil
}
```
`bridge.go`：抽出
```go
// loadBridgeTargets：每轮重读项目登记表（新建模块无需重启 bridge，spec §4）。
func loadBridgeTargets() ([]bridgeTarget, error) {
	ps, err := loadProjects()
	if err != nil {
		return nil, err
	}
	var targets []bridgeTarget
	for _, p := range ps {
		if st, err := os.Stat(p.Dir); err == nil && st.IsDir() {
			targets = append(targets, bridgeTarget{Channel: p.Channel, Dir: p.Dir})
		}
	}
	return targets, nil
}
```
`RunBridge`：启动时 `targets, err := loadBridgeTargets()`（为空则沿用 `findProject` 回退并打印），循环体改为：
```go
	for {
		if fresh, err := loadBridgeTargets(); err == nil && len(fresh) > 0 {
			if len(fresh) != len(targets) {
				fmt.Printf("项目登记表已更新，现照看 %d 个项目\n", len(fresh))
			}
			targets = fresh
		}
		_, err := pollOnce(c, targets, *hook, notifyDesktop)
		c.Heartbeat()
		…（退避逻辑不变）
	}
```
`auto.go`：
```go
var firstResponderRe = regexp.MustCompile(`^@(claude|codex)\b`)

// firstResponderHint：开题信首行 "@codex 先回" 指定先答的一侧（spec §6）。读不到文件或无此行返回空。
func firstResponderHint(msgPath string) string {
	data, err := os.ReadFile(msgPath)
	if err != nil {
		return ""
	}
	_, body, perr := msg.Parse(data)
	if perr != nil {
		body = string(data)
	}
	first := strings.TrimSpace(strings.SplitN(body, "\n", 2)[0])
	if m := firstResponderRe.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	return ""
}
```
在 `humanMsgResponder` 里，计算 `who` 的 `switch` 之后加：
```go
	if hint := firstResponderHint(os.Getenv("RELAIS_MSG_PATH")); hint != "" {
		who = hint
	}
```
（import `regexp` 与 `internal/msg`。）`localprompt.go` 在 `fmt.Fprintf(&b, "新信在文件 %s。…")` 之后加 `b.WriteString("正文首行若以 @ 开头（如 @codex 先回），是给系统的路由指示，忽略它。\n")`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/cli/ -v -race`
Expected: 全 PASS。

- [ ] **Step 5: 地板 + 提交**

```bash
./scripts/check.sh
git add internal/cli
git commit -m "feat(cli): bridge 每轮重读登记表并报心跳；接话规则认信首行 @claude/@codex；提示词说明（M8 Task 3）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: 网页 —— 「模块」页、「开题」框、「复制开工指令」、引导

**Files:**
- Modify: `internal/server/web/index.html`（顶栏「模块」入口、`#modules-view`、频道页 `#open-topic`）
- Modify: `internal/server/web/app.js`（三语、模块页逻辑、开题、复制开工指令、引导、本地模式探测）
- Modify: `internal/server/web/style.css`
- Test: `internal/server/web_test.go`

**Interfaces:**
- Consumes：Task 2 的 `/api/local/*`；现有 `humanAction`、`api`、`showView`、`loadChannels`、`openChannel`、`renderMsg`、复制函数（`app.js` ~155，`navigator.clipboard.writeText`）。
- 本地模式探测：登录后 `GET /api/local/modules`；200 → `isLocal = true`，显示「模块」入口与开题框；404 → 隐藏（联网页面不变）。
- 新 id：`menu-modules`、`modules-view`、`modules-back`、`module-list`、`module-name`、`module-dir`（`<select>`，末项 `__custom__`）、`module-dir-custom`、`module-create`、`local-settings-claude`、`local-settings-codex`、`local-settings-mode`、`local-settings-save`、`open-topic`（容器）、`topic-body`、`topic-first`（`<select>` claude/codex）、`topic-send`、`onboarding`。
- 新 i18n 键（zh/en/de 各一）：`modules, newModule, moduleName, moduleDir, customDir, create, rules, saveRules, closeModule, closeConfirm, localSettings, claudePath, codexPath, defaultMode, save, hookRewritten, bridgeAlive, bridgeDead, conclusionsCount, openTopic, topicPh, firstResponder, sendTopic, copyKickoff, kickoffCopied, onboarding, stateRunning, statePaused, stateNeedsHuman, stateResolved, stateKickedOff, stateClosed, openChannel, backChat`（`backChat`/`openChannel` 若已存在则复用）。
- 开题发送体：`to` = 频道内除自己外全部成员；`summary` = 正文首行前 80 字；`body_md` = `"@" + side + " 先回\n\n" + 正文`。
- 复制开工指令文本：`读 relais/conclusions/<channel>-<msgid>.md，按结论开工`（msgid = 结论卡片的 `m.id`）。

- [ ] **Step 1: 写失败的测试**

`internal/server/web_test.go` 追加：
```go
func TestWebHasConsoleControls(t *testing.T) {
	html, _ := webFS.ReadFile("web/index.html")
	for _, id := range []string{`id="menu-modules"`, `id="modules-view"`, `id="module-list"`, `id="module-name"`, `id="module-dir"`, `id="module-dir-custom"`, `id="module-create"`, `id="local-settings-claude"`, `id="local-settings-codex"`, `id="local-settings-mode"`, `id="local-settings-save"`, `id="open-topic"`, `id="topic-body"`, `id="topic-first"`, `id="topic-send"`, `id="onboarding"`} {
		if !strings.Contains(string(html), id) {
			t.Fatalf("index.html 缺 %s", id)
		}
	}
	js, _ := webFS.ReadFile("web/app.js")
	for _, key := range []string{"modules", "newModule", "moduleName", "moduleDir", "customDir", "rules", "saveRules", "closeModule", "closeConfirm", "localSettings", "claudePath", "codexPath", "defaultMode", "hookRewritten", "bridgeAlive", "bridgeDead", "conclusionsCount", "openTopic", "topicPh", "firstResponder", "sendTopic", "copyKickoff", "kickoffCopied", "onboarding", "stateRunning", "stateNeedsHuman", "stateKickedOff", "stateClosed"} {
		if strings.Count(string(js), key+":") < 3 {
			t.Fatalf("app.js 三语文案缺 %s", key)
		}
	}
	for _, s := range []string{"/api/local/modules", "/api/local/repos", "/api/local/settings", "/rules", "/close", "先回", "按结论开工", "relais/conclusions/", "__custom__"} {
		if !strings.Contains(string(js), s) {
			t.Fatalf("app.js 缺 %s", s)
		}
	}
	// 开题：body 以 @side 开头
	i := strings.Index(string(js), `$("topic-send")`)
	if i < 0 || !strings.Contains(string(js)[i:i+1500], `"@" + `) {
		t.Fatal("开题发送应把 @side 先回 放在正文首行")
	}
	if strings.Contains(string(js), ".innerHTML = m.") || strings.Contains(string(js), ".innerHTML = mod.") || strings.Contains(string(js), ".innerHTML = r.") {
		t.Fatal("动态数据不得拼 innerHTML")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/ -run TestWebHasConsoleControls -v`
Expected: FAIL `index.html 缺 id="menu-modules"`。

- [ ] **Step 3: index.html**

用户菜单里 `menu-admin` 之前加 `<button id="menu-modules" type="button" hidden data-i18n="modules">模块</button>`。`#chat-view` 内 `#auto-bar` 之前加：
```html
      <div id="onboarding" class="muted" hidden data-i18n="onboarding">还没有模块：先去「模块」页新建一个。</div>
      <div id="open-topic" class="card" hidden>
        <textarea id="topic-body" data-i18n-placeholder="topicPh" placeholder="写下要让两个 AI 讨论的议题……"></textarea>
        <div class="row">
          <label><span data-i18n="firstResponder">先由谁回应</span>
            <select id="topic-first"><option value="claude">Claude</option><option value="codex">Codex</option></select>
          </label>
          <button id="topic-send" type="button" data-i18n="sendTopic">开题</button>
        </div>
      </div>
```
`#admin-view` 之后加：
```html
    <main id="modules-view" hidden>
      <button id="modules-back" type="button" class="ghost" data-i18n="backChat">← 返回</button>
      <section class="card">
        <h3 data-i18n="newModule">新建模块</h3>
        <label><span data-i18n="moduleName">模块名</span> <input id="module-name" placeholder="grammar"></label>
        <label><span data-i18n="moduleDir">项目文件夹</span>
          <select id="module-dir"></select>
          <input id="module-dir-custom" hidden placeholder="/Users/you/project">
        </label>
        <button id="module-create" type="button" data-i18n="create">创建</button>
      </section>
      <section class="card"><h3 data-i18n="modules">模块</h3><div id="module-list"></div></section>
      <section class="card">
        <h3 data-i18n="localSettings">设置</h3>
        <label><span data-i18n="claudePath">claude 路径</span> <input id="local-settings-claude"></label>
        <label><span data-i18n="codexPath">codex 路径</span> <input id="local-settings-codex"></label>
        <label><span data-i18n="defaultMode">新模块默认模式</span>
          <select id="local-settings-mode"><option value="supervised" data-i18n="modeSupervised">监督</option><option value="autopilot" data-i18n="modeAutopilot">甩手</option></select>
        </label>
        <button id="local-settings-save" type="button" data-i18n="save">保存</button>
      </section>
    </main>
```
（`backChat` 若不存在则新增三语键。）

- [ ] **Step 4: app.js**

(a) 三语键（zh 示例；en/de 对应翻译）：
```js
    modules: "模块", newModule: "新建模块", moduleName: "模块名", moduleDir: "项目文件夹", customDir: "手填路径…", create: "创建",
    rules: "编辑规矩", saveRules: "保存规矩", closeModule: "关闭", closeConfirm: "关闭模块 {n}？讨论会停止，数据保留。",
    localSettings: "设置", claudePath: "claude 路径", codexPath: "codex 路径", defaultMode: "新模块默认模式", save: "保存", hookRewritten: "已保存，两侧 hook 已重写",
    bridgeAlive: "在跑", bridgeDead: "没在跑", conclusionsCount: "{n} 份结论",
    openTopic: "开题", topicPh: "写下要让两个 AI 讨论的议题……", firstResponder: "先由谁回应", sendTopic: "开题",
    copyKickoff: "复制开工指令", kickoffCopied: "已复制，贴给承接方的工作脑即可", onboarding: "还没有模块：先去「模块」页新建一个。",
    stateRunning: "运行中", statePaused: "已暂停", stateNeedsHuman: "等你回答", stateResolved: "已握手待确认", stateKickedOff: "已开工", stateClosed: "已关闭",
    backChat: "← 返回", openChannel: "打开频道",
```
(b) 本地模式探测与入口（登录成功后、`loadChannels()` 之后）：
```js
let isLocal = false;
async function detectLocal() {
  try { const mods = await api("/api/local/modules"); isLocal = true; $("onboarding").hidden = mods.length > 0; }
  catch { isLocal = false; $("onboarding").hidden = true; }
  $("menu-modules").hidden = !isLocal;
}
```
在 `showView` 里加 `$("modules-view").hidden = name !== "modules";`。菜单：`$("menu-modules").addEventListener("click", () => { loadModules(); showView("modules"); })`、`$("modules-back").addEventListener("click", () => showView("chat"))`。

(c) 模块页：
```js
function stateLabel(s) { return t({ running: "stateRunning", paused: "statePaused", needs_human: "stateNeedsHuman", resolved: "stateResolved", kicked_off: "stateKickedOff", closed: "stateClosed" }[s] || "stateRunning"); }

async function loadModules() {
  const [mods, repos, settings] = await Promise.all([api("/api/local/modules"), api("/api/local/repos"), api("/api/local/settings")]);
  const sel = $("module-dir"); sel.innerHTML = "";
  for (const r of repos) { const o = document.createElement("option"); o.value = r.dir; o.textContent = r.name + "  " + r.dir; sel.append(o); }
  const custom = document.createElement("option"); custom.value = "__custom__"; custom.textContent = t("customDir"); sel.append(custom);
  $("module-dir-custom").hidden = sel.value !== "__custom__";
  const list = $("module-list"); list.innerHTML = "";
  for (const mod of mods) list.append(renderModule(mod));
  $("local-settings-claude").value = settings.claude_path || "";
  $("local-settings-codex").value = settings.codex_path || "";
  $("local-settings-mode").value = settings.default_mode || "supervised";
}

function renderModule(mod) {
  const row = document.createElement("div"); row.className = "module-row";
  const head = document.createElement("div"); head.className = "head";
  const name = document.createElement("strong"); name.textContent = mod.name;
  const dir = document.createElement("span"); dir.className = "muted"; dir.textContent = mod.dir;
  const state = document.createElement("span"); state.className = mod.state === "needs_human" ? "err" : "muted";
  state.textContent = stateLabel(mod.state) + " · " + mod.round + "/" + mod.round_cap + " · " + mod.mode + " · " + t("conclusionsCount").replace("{n}", mod.conclusions);
  head.append(name, dir, state);
  const beats = document.createElement("div"); beats.className = "muted";
  for (const side of ["claude", "codex"]) {
    const dot = document.createElement("span"); dot.className = "dot " + (mod.bridge_alive && mod.bridge_alive[side] ? "ok" : "dead");
    const lbl = document.createElement("span"); lbl.textContent = side + " bridge " + (mod.bridge_alive && mod.bridge_alive[side] ? t("bridgeAlive") : t("bridgeDead")) + " ";
    beats.append(dot, lbl);
  }
  const actions = document.createElement("div"); actions.className = "actions";
  const open = document.createElement("button"); open.className = "toggle"; open.textContent = t("openChannel");
  open.onclick = () => { showView("chat"); openChannel(mod.name); };
  const rules = document.createElement("button"); rules.className = "toggle"; rules.textContent = t("rules");
  const editor = document.createElement("div"); editor.hidden = true;
  const ta = document.createElement("textarea"); const saveBtn = document.createElement("button"); saveBtn.textContent = t("saveRules");
  editor.append(ta, saveBtn);
  rules.onclick = () => humanAction(async () => {
    if (editor.hidden) { const r = await api("/api/local/modules/" + encodeURIComponent(mod.name) + "/rules"); ta.value = r.text; }
    editor.hidden = !editor.hidden;
  });
  saveBtn.onclick = () => humanAction(async () => {
    await api("/api/local/modules/" + encodeURIComponent(mod.name) + "/rules", { method: "PUT", body: JSON.stringify({ text: ta.value }) });
    editor.hidden = true;
  });
  const close = document.createElement("button"); close.className = "toggle danger"; close.textContent = t("closeModule"); close.hidden = mod.state === "closed";
  close.onclick = () => humanAction(async () => {
    if (!confirm(t("closeConfirm").replace("{n}", mod.name))) return;
    await api("/api/local/modules/" + encodeURIComponent(mod.name) + "/close", { method: "POST" });
    await loadModules(); loadChannels();
  });
  actions.append(open, rules, close);
  row.append(head, beats, actions, editor);
  return row;
}

$("module-dir").addEventListener("change", () => { $("module-dir-custom").hidden = $("module-dir").value !== "__custom__"; });
$("module-create").addEventListener("click", () => humanAction(async () => {
  const name = $("module-name").value.trim();
  const dir = $("module-dir").value === "__custom__" ? $("module-dir-custom").value.trim() : $("module-dir").value;
  if (!name || !dir) return;
  await api("/api/local/modules", { method: "POST", body: JSON.stringify({ name, dir }) });
  $("module-name").value = "";
  await loadModules(); await loadChannels(); detectLocal();
}));
$("local-settings-save").addEventListener("click", () => humanAction(async () => {
  await api("/api/local/settings", { method: "PUT", body: JSON.stringify({ claude_path: $("local-settings-claude").value.trim(), codex_path: $("local-settings-codex").value.trim(), default_mode: $("local-settings-mode").value }) });
  alert(t("hookRewritten"));
}));
```
`humanAction` 结尾调用 `loadAutoState()`；在模块页时 `channel` 可能为空——`loadAutoState` 已有 `try/catch` 且 `channel` 为 null 时 `encodeURIComponent(null)` 会请求 `/null/auto` 得 404 被 catch，可接受；更干净的做法是在 `loadAutoState` 开头加 `if (!channel) return;`（做这个）。

(d) 开题框：在 `loadAutoState` 里 `$("open-topic").hidden = !isLocal || !on || st.in_flight || st.resolved || !!st.needs_human_q;`。事件：
```js
$("topic-send").addEventListener("click", () => humanAction(async () => {
  const text = $("topic-body").value.trim();
  if (!text) return;
  const side = $("topic-first").value;
  const to = members.filter((m) => m.username !== me.username).map((m) => m.username);
  const summary = text.split(/\r?\n/)[0].slice(0, 80);
  await api("/api/channels/" + encodeURIComponent(channel) + "/messages", {
    method: "POST", body: JSON.stringify({ to, summary, body_md: "@" + side + " 先回\n\n" + text }),
  });
  try { localStorage.setItem("relais.topicFirst", side); } catch {}
  $("topic-body").value = "";
  refresh();
}));
try { $("topic-first").value = localStorage.getItem("relais.topicFirst") || "claude"; } catch {}
```
(e) 结论卡片：在 `renderMsg` 的 `kind === "conclusion" || "kickoff"` 分支里加按钮：
```js
    const copyK = document.createElement("button"); copyK.className = "toggle"; copyK.textContent = t("copyKickoff");
    copyK.onclick = async () => {
      await navigator.clipboard.writeText("读 relais/conclusions/" + channel + "-" + m.id + ".md，按结论开工");
      copyK.textContent = t("kickoffCopied"); setTimeout(() => { copyK.textContent = t("copyKickoff"); }, 2000);
    };
    div.append(copyK);
```
（kickoff 消息的 `m.id` 是 kickoff 自己的 id，而落盘文件名用的也是它——`pullOne` 用 `full.ID`——所以对 kickoff 卡片同样成立；对 conclusion 卡片在开工前点，文件尚不存在，按钮只在 `m.kind === "kickoff"` 时显示。）

(f) `style.css`：
```css
.module-row { border-top: 1px solid var(--line); padding: 10px 0; display: flex; flex-direction: column; gap: 6px; }
.module-row .head { display: flex; gap: 10px; align-items: baseline; flex-wrap: wrap; }
.dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 4px; }
.dot.ok { background: var(--ok); } .dot.dead { background: var(--muted); }
#open-topic textarea { width: 100%; min-height: 72px; }
#modules-view { padding-bottom: 40px; gap: 14px; }
#modules-view label { display: block; margin: 6px 0; }
```

- [ ] **Step 5: 跑测试与地板**

Run: `go test ./internal/server/ -run TestWeb -v && ./scripts/check.sh`
Expected: PASS，`node --check` 通过。手工冒烟可选。

- [ ] **Step 6: 提交**

```bash
git add internal/server/web internal/server/web_test.go
git commit -m "feat(web): 模块页（新建/规矩/关闭/设置/心跳）+ 开题框（@side 先回）+ 复制开工指令 + 引导（M8 Task 4）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: `relais local bootstrap` 子命令 + 双击安装脚本

**Files:**
- Modify: `internal/cli/local.go`（`RunLocal` 加 `bootstrap` 分支；常驻安装抽成 `installLocalServices(ld, scPath) error` 供 init 与 bootstrap 共用）
- Create: `安装 Relais 本地模式.command`（仓库根目录，`chmod +x`）
- Modify: `scripts/check.sh`（`bash -n` 该脚本）
- Test: `internal/cli/local_test.go`（bootstrap 子命令）、`scripts/check.sh` 本身

**Interfaces:**
- `relais local bootstrap [--claude P] [--codex P] [--listen 127.0.0.1:8080] [--no-service] [--json]`：调用 `localManager.bootstrap` + `installLocalServices`（除非 `--no-service`）+ `waitListen`；`--json` 时只在 stdout 打印一行 JSON `{"base_url":…,"human_user":…,"human_password":…,"password_shown":…}` 供安装脚本读；否则打印人话。
- 安装脚本流程见 spec §2；codex 找不到时 `osascript -e 'POSIX path of (choose file with prompt "请选择 codex 可执行文件")'`。

- [ ] **Step 1: 写失败的测试**

`internal/cli/local_test.go` 追加：
```go
func TestLocalBootstrapCommandJSON(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	out := captureStdout(t, func() {
		if err := RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/bin/cat", "--listen", "127.0.0.1:18098", "--no-service", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var res struct {
		BaseURL string `json:"base_url"`; HumanUser string `json:"human_user"`; HumanPassword string `json:"human_password"`; PasswordShown bool `json:"password_shown"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil || res.BaseURL != "http://127.0.0.1:18098" || res.HumanUser != "hou" || !res.PasswordShown || res.HumanPassword == "" {
		t.Fatalf("bootstrap --json 输出错: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(ld, "sides", "codex", "hooks", "auto-reply.sh")); err != nil {
		t.Fatal("bootstrap 应生成两侧 hook")
	}
	// 第二次：password_shown=false，不建模块
	out = captureStdout(t, func() { RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/bin/cat", "--listen", "127.0.0.1:18098", "--no-service", "--json"}) })
	if !strings.Contains(out, `"password_shown":false`) {
		t.Fatalf("第二次不应再显示密码: %q", out)
	}
	if err := RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/nonexistent", "--no-service"}); err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("codex 路径无效应报错: %v", err)
	}
}
```
`captureStdout` 若测试包里已有同名 helper（Task 6 of M7 加过 `TestLocalInitQuotesPaths`，看它怎么捕获输出）就复用；否则加：
```go
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	data, _ := io.ReadAll(r)
	return string(data)
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run TestLocalBootstrapCommandJSON -v`
Expected: FAIL `未知 local 子命令 "bootstrap"`。

- [ ] **Step 3: 实现 bootstrap 子命令**

`local.go`：`RunLocal` 加 `case "bootstrap": return runLocalBootstrap(args[1:])`。把 `runLocalInit` 里"5) 常驻"那段抽成：
```go
// installLocalServices：serve + 两个 bridge 的 launchd 常驻；已存在的 plist 跳过（spec §3.1）。
func installLocalServices(ld, scPath string) error {
	relais, _ := os.Executable()
	type svc struct {
		label string
		args  []string
		env   map[string]string
	}
	svcs := []svc{{"com.relais.local.serve", []string{relais, "serve", "--config", scPath}, nil}}
	for _, side := range []string{"claude", "codex"} {
		d := filepath.Join(ld, "sides", side)
		svcs = append(svcs, svc{"com.relais.local.bridge." + side,
			[]string{relais, "bridge", "--interval", "5", "--hook", filepath.Join(d, "hooks", "auto-reply.sh")},
			map[string]string{"RELAIS_CONFIG_DIR": d, "HOME": os.Getenv("HOME"), "PATH": os.Getenv("PATH")}})
	}
	for _, v := range svcs {
		if !shouldInstallPlist(plistPathFor(v.label)) {
			fmt.Printf("常驻 %s 已存在，跳过\n", v.label)
			continue
		}
		if _, err := installPlist(v.label, v.args, v.env); err != nil {
			return err
		}
	}
	return nil
}

func runLocalBootstrap(args []string) error {
	fs := flag.NewFlagSet("local bootstrap", flag.ContinueOnError)
	claudePath := fs.String("claude", "", "claude 可执行文件路径（默认 PATH 侦测）")
	codexPath := fs.String("codex", "", "codex 可执行文件路径（默认 PATH 侦测）")
	listen := fs.String("listen", "127.0.0.1:8080", "本地服务器监听地址")
	noService := fs.Bool("no-service", false, "不安装 launchd 常驻")
	asJSON := fs.Bool("json", false, "只输出一行 JSON（安装脚本用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ld, err := localDir()
	if err != nil {
		return err
	}
	claudeP, codexP := *claudePath, *codexPath
	if claudeP == "" {
		claudeP, _ = exec.LookPath("claude")
	}
	if codexP == "" {
		codexP, _ = exec.LookPath("codex")
	}
	mgr := newLocalManager(ld)
	res, err := mgr.bootstrap(*listen, claudeP, codexP)
	if err != nil {
		return err
	}
	if !*noService {
		if err := installLocalServices(ld, localServerConfigPath(ld)); err != nil {
			return err
		}
		waitListen(*listen, 5*time.Second)
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"base_url": res.BaseURL, "human_user": res.HumanUser, "human_password": res.HumanPassword, "password_shown": res.PasswordShown})
	}
	fmt.Printf("本地模式环境已就绪：%s\n控制台: %s（账号 %s）\n", shq(ld), res.BaseURL, res.HumanUser)
	if res.PasswordShown {
		fmt.Printf("初始密码: %s（已存到 %s）\n", res.HumanPassword, shq(filepath.Join(ld, "human.txt")))
	}
	return nil
}
```
`runLocalInit` 的常驻段改为调用 `installLocalServices`。（import `encoding/json`。）注意 `waitListen` 用 `*listen` 与 D42 一致（已知 minor）。

- [ ] **Step 4: 安装脚本**

`安装 Relais 本地模式.command`：
```bash
#!/bin/bash
# 双击安装 / 升级 Relais 本地模式（spec 2026-09-27 M8 §2）。重复双击 = 升级，数据不动。
set -euo pipefail
cd "$(dirname "$0")"

dialog() { osascript -e "display dialog \"$1\" buttons {\"好\"} default button 1 with title \"Relais 本地模式\"" >/dev/null 2>&1 || true; }
fail() { dialog "$1"; echo "错误: $1" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "没找到 Go。请先安装 Go（https://go.dev/dl/）再双击本文件。"

BIN_DIR=/opt/homebrew/bin
if [ ! -w "$BIN_DIR" ]; then BIN_DIR="$HOME/bin"; mkdir -p "$BIN_DIR"; fi
echo "编译 relais → $BIN_DIR/relais"
CGO_ENABLED=0 go build -o "$BIN_DIR/relais" .
RELAIS="$BIN_DIR/relais"

pick() { osascript -e "POSIX path of (choose file with prompt \"$1\")" 2>/dev/null || true; }
CLAUDE="$(command -v claude || true)"
[ -z "$CLAUDE" ] && CLAUDE="$(pick '请选择 claude 可执行文件')"
CODEX="$(command -v codex || true)"
[ -z "$CODEX" ] && [ -x "$HOME/.codex/plugins/.plugin-appserver/codex" ] && CODEX="$HOME/.codex/plugins/.plugin-appserver/codex"
[ -z "$CODEX" ] && CODEX="$(pick '请选择 codex 可执行文件（通常在 ~/.codex/plugins/.plugin-appserver/codex）')"
[ -n "$CLAUDE" ] && [ -n "$CODEX" ] || fail "没有选到 claude 或 codex，安装中止。"

echo "配置本地模式…"
OUT="$("$RELAIS" local bootstrap --claude "$CLAUDE" --codex "$CODEX" --json)" || fail "bootstrap 失败，详情见终端。"
BASE="$(printf '%s' "$OUT" | sed -n 's/.*"base_url":"\([^"]*\)".*/\1/p')"
SHOWN="$(printf '%s' "$OUT" | sed -n 's/.*"password_shown":\(true\|false\).*/\1/p')"
PW="$(printf '%s' "$OUT" | sed -n 's/.*"human_password":"\([^"]*\)".*/\1/p')"

# 升级路径：常驻已存在时重启三个服务，让新二进制生效
for L in com.relais.local.serve com.relais.local.bridge.claude com.relais.local.bridge.codex; do
  if [ -f "$HOME/Library/LaunchAgents/$L.plist" ]; then launchctl kickstart -k "gui/$(id -u)/$L" 2>/dev/null || true; fi
done

if [ "$SHOWN" = "true" ]; then
  dialog "安装完成。控制台：$BASE\n账号：hou\n初始密码：$PW\n（已存到 ~/Library/Application Support/relais-local/human.txt）"
else
  dialog "升级完成。控制台：$BASE\n账号：hou（密码见 ~/Library/Application Support/relais-local/human.txt）"
fi
open "$BASE"
```
`chmod +x` 后提交（git 记录可执行位）。`scripts/check.sh` 在 `go test ./...` 之后加：
```bash
bash -n "安装 Relais 本地模式.command"
```

- [ ] **Step 5: 跑测试与地板**

Run: `go test ./internal/cli/ -run 'TestLocal' -v -race && ./scripts/check.sh`
Expected: PASS；`bash -n` 通过。另外手工跑一次脚本的"干跑"：`RELAIS_LOCAL_DIR=$(mktemp -d) bash -c 'cd "$(git rev-parse --show-toplevel)"; go build -o /tmp/relais-m8 . && /tmp/relais-m8 local bootstrap --claude /bin/echo --codex /bin/cat --listen 127.0.0.1:18097 --no-service --json'` 确认 JSON 输出与 sed 提取一致（把三条 sed 贴到终端验证），然后删除临时目录与 `/tmp/relais-m8`。

- [ ] **Step 6: 提交**

```bash
git add internal/cli/local.go internal/cli/local_test.go "安装 Relais 本地模式.command" scripts/check.sh
git commit -m "feat(cli): relais local bootstrap（--json）+ 双击安装脚本 + check.sh bash -n（M8 Task 5）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: 工作脑说明、e2e 锚点、版本、README、D44、真实冒烟

**Files:**
- Modify: `internal/guide/guide.go`（`LocalText(side, channel, sideDir string) string`）+ `guide_test.go`
- Modify: `internal/cli/localmgr.go`（`writeLocalAgentGuide` 改用 `guide.LocalText`，两侧各一段）
- Modify: `e2e/e2e_test.go`（M8 锚点）
- Modify: `main.go`、`internal/server/static.go`（`0.6.0-m8`）
- Modify: `README.md`、`docs/decisions.md`（D44）
- Test: 上述

**Interfaces:**
- `guide.LocalText(side, channel, sideDir string) string`：给工作脑的本地模式说明段，含两条口令 → 动作：
  - "把这个拿去讨论 / 让 Codex（或 Claude）看看"：压缩背景写信（frontmatter `summary:`），执行 `RELAIS_CONFIG_DIR="<sideDir>" RELAIS_CHANNEL="<channel>" relais send <文件>`；不要问人，直接发。
  - "开工 / 按结论做"：读 `relais/conclusions/` 下 `<channel>-*.md` 中最新一份（按文件名排序取最后），按正文实施；实施完成后把要点写成一封信同样发出（`summary: 已完成：…`）。
  - 附：本侧身份是 `<side>`；`RULES.md` 是铁律；不要读 `relais/inbox` 里别的模块的信。
- `writeLocalAgentGuide(dir, ld, name)`：`AGENT.md` 末尾若无 `## 本地模式（模块 <name>）` 标记则追加 claude 侧与 codex 侧各一段（`LocalText("claude", name, ld/sides/claude)` 与 codex 同理）。

- [ ] **Step 1: 写失败的测试**

`internal/guide/guide_test.go` 追加：
```go
func TestLocalTextTeachesTwoCommands(t *testing.T) {
	s := LocalText("claude", "grammar", "/x/sides/claude")
	for _, want := range []string{"拿去讨论", "开工", `RELAIS_CONFIG_DIR="/x/sides/claude"`, `RELAIS_CHANNEL="grammar"`, "relais send", "relais/conclusions/", "grammar-", "RULES.md", "summary:", "claude"} {
		if !strings.Contains(s, want) {
			t.Fatalf("LocalText 缺 %q", want)
		}
	}
}
```
`internal/cli/localmgr_test.go` 追加：
```go
func TestCreateModuleWritesLocalGuideForBothSides(t *testing.T) {
	m, ld := newMgrForTest(t)
	proj := t.TempDir()
	if _, err := m.CreateModule("grammar", proj); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(proj, "relais", "AGENT.md"))
	s := string(data)
	for _, want := range []string{"## 本地模式（模块 grammar）", filepath.Join(ld, "sides", "claude"), filepath.Join(ld, "sides", "codex"), "拿去讨论", "开工"} {
		if !strings.Contains(s, want) {
			t.Fatalf("AGENT.md 缺 %q", want)
		}
	}
	m.CreateModule("grammar", proj)
	data2, _ := os.ReadFile(filepath.Join(proj, "relais", "AGENT.md"))
	if strings.Count(string(data2), "## 本地模式（模块 grammar）") != 1 {
		t.Fatal("重复创建不应重复追加说明")
	}
}
```
`e2e/e2e_test.go` 追加：
```go
// M8 锚点：本地管理接口的隔离 + 模块创建→bridge 拉到消息的闭环
func TestAnchorM8LocalConsole(t *testing.T) {
	w := newWorld(t)
	// 线上世界（未注入本地管理器）：/api/local/* 一律 404
	req, _ := http.NewRequest("GET", w.ts.URL+"/api/local/modules", nil)
	req.Header.Set("Authorization", "Bearer "+w.users["hou"].AgentToken)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 404 {
		t.Fatalf("锚点M8-1 线上不得暴露本地接口, got %d", resp.StatusCode)
	}
	// 本地世界：真 localManager + 真 serve handler
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	t.Setenv("HOME", t.TempDir())
	if err := cli.RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/bin/cat", "--listen", "127.0.0.1:18096", "--no-service"}); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(ld, "data", "relais.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := server.New(st, "http://127.0.0.1:18096", t.TempDir())
	srv.SetLocal(cli.NewLocalManagerForTest(ld))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	// 人登录（密码在 human.txt）
	pw := readPassword(t, filepath.Join(ld, "human.txt"))
	cookie := login(t, ts, "hou", pw)
	// agent token 打本地接口 → 403
	claudeU, _ := st.UserByName("claude")
	req, _ = http.NewRequest("GET", ts.URL+"/api/local/modules", nil)
	req.Header.Set("Authorization", "Bearer "+claudeU.AgentToken)
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("锚点M8-2 agent 打本地接口必须 403, got %d", resp.StatusCode)
	}
	// 人在网页新建模块
	proj := t.TempDir()
	body, _ := json.Marshal(map[string]string{"name": "grammar", "dir": proj})
	req, _ = http.NewRequest("POST", ts.URL+"/api/local/modules", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "relais_session", Value: cookie})
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 200 {
		t.Fatalf("锚点M8-3 新建模块应 200, got %d", resp.StatusCode)
	}
	for _, f := range []string{"relais/config.toml", "relais/RULES.md", "relais/AGENT.md"} {
		if _, err := os.Stat(filepath.Join(proj, f)); err != nil {
			t.Fatalf("锚点M8-3 项目应有 %s", f)
		}
	}
	// 人在网页开题（@codex 先回）→ codex 侧 bridge（重读登记表后）拉到；claude 侧被拒接话
	body, _ = json.Marshal(map[string]any{"to": []string{"claude", "codex"}, "summary": "议题", "body_md": "@codex 先回\n\n用什么缓存"})
	req, _ = http.NewRequest("POST", ts.URL+"/api/channels/grammar/messages", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: "relais_session", Value: cookie})
	req.Header.Set("Content-Type", "application/json")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 200 {
		t.Fatalf("锚点M8-4 开题应 200, got %d", resp.StatusCode)
	}
	t.Setenv("RELAIS_CONFIG_DIR", filepath.Join(ld, "sides", "codex"))
	cli.SaveGlobalForTest(ts.URL, mustUser(t, st, "codex").AgentToken, "codex") // 指向测试服务器而非 18096
	c, _ := cli.NewClientForTest()
	targets := cli.LoadBridgeTargetsForTest()
	if len(targets) != 1 || targets[0].Channel != "grammar" || targets[0].Dir != proj {
		t.Fatalf("锚点M8-4 codex 侧登记表应含 grammar: %+v", targets)
	}
	if n, err := cli.PollOnceForTest(c, "grammar", proj); err != nil || n != 1 {
		t.Fatalf("锚点M8-4 codex 侧应拉到 1 封: %d %v", n, err)
	}
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "inbox"))
	if len(entries) != 1 {
		t.Fatalf("锚点M8-4 应落 1 封: %v", entries)
	}
	// 心跳 → 模块列表 bridge_alive
	c.Heartbeat()
	req, _ = http.NewRequest("GET", ts.URL+"/api/local/modules", nil)
	req.AddCookie(&http.Cookie{Name: "relais_session", Value: cookie})
	resp, _ = http.DefaultClient.Do(req)
	var mods []api.LocalModule
	json.NewDecoder(resp.Body).Decode(&mods)
	if len(mods) != 1 || !mods[0].BridgeAlive["codex"] || mods[0].BridgeAlive["claude"] {
		t.Fatalf("锚点M8-5 心跳应显示 codex 在跑、claude 未跑: %+v", mods)
	}
	// 非回环 RemoteAddr → 403（直接调 handler）
	rr := httptest.NewRecorder()
	r2 := httptest.NewRequest("GET", "/api/local/modules", nil)
	r2.AddCookie(&http.Cookie{Name: "relais_session", Value: cookie})
	r2.RemoteAddr = "192.168.1.9:5555"
	srv.Handler().ServeHTTP(rr, r2)
	if rr.Code != 403 {
		t.Fatalf("锚点M8-6 非回环必须 403, got %d", rr.Code)
	}
}
```
需要的 e2e 小助手（放 e2e 文件里，若已有同名的复用）：
```go
func readPassword(t *testing.T, path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "初始密码: ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "初始密码: "))
		}
	}
	t.Fatal("human.txt 无密码行")
	return ""
}
func login(t *testing.T, ts *httptest.Server, user, pw string) string {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pw})
	resp, err := http.Post(ts.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("登录失败: %v %v", err, resp)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "relais_session" {
			return c.Value
		}
	}
	t.Fatal("无 session cookie")
	return ""
}
func mustUser(t *testing.T, st *store.Store, name string) *store.User {
	u, err := st.UserByName(name)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
```
并在 `internal/cli/testing.go` 加两个测试出口：
```go
// NewLocalManagerForTest 暴露本地管理器；生产代码不得调用。
func NewLocalManagerForTest(ld string) server.LocalManager { return newLocalManager(ld) }
// LoadBridgeTargetsForTest 暴露 bridge 目标重读；生产代码不得调用。
func LoadBridgeTargetsForTest() []bridgeTarget { ts, _ := loadBridgeTargets(); return ts }
```
（`bridgeTarget` 未导出但字段 `Channel/Dir` 导出，e2e 可读；若 vet 抱怨返回未导出类型，改为返回 `[]struct{ Channel, Dir string }` 转换一下。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/guide/ ./internal/cli/ ./e2e/ -run 'TestLocalText|TestCreateModuleWritesLocalGuide|TestAnchorM8' -v`
Expected: 编译错误（`LocalText`、`NewLocalManagerForTest` 未定义）。

- [ ] **Step 3: 实现**

`internal/guide/guide.go` 追加：
```go
// LocalText：本地单人模式给工作脑的说明（写进项目 relais/AGENT.md，M8 spec §7）。
func LocalText(side, channel, sideDir string) string {
	return fmt.Sprintf(`
## 本地模式（模块 %[2]s）· %[1]s 侧

你是雇主在模块 %[2]q 的「工作脑」，身份是 %[1]s 侧。讨论由另一套无头「讨论脑」自动进行，你只负责两件事，雇主一句话触发，不要反问：

1. 雇主说「把这个拿去讨论」「让对方看看」时：把当前上下文里与议题相关的背景压缩成一封信（Markdown，文件开头写
   ---
   summary: <一句话摘要>
   ---
   然后正文：背景、你的观点、要对方回答的问题），保存为文件后执行：
   RELAIS_CONFIG_DIR="%[3]s" RELAIS_CHANNEL="%[2]s" relais send <文件路径>
   发出后告诉雇主"已发出，去网页看进度"。不要等回信，回信由讨论脑处理。

2. 雇主说「开工」「按结论做」时：读 relais/conclusions/ 目录下文件名以 %[2]s- 开头的最新一份（按文件名排序取最后一个），按它的正文实施。
   实施完成后把做了什么、怎么验证写成一封信（summary: 已完成：…），用上面同样的命令发出。

规矩：relais/RULES.md 是本项目铁律，先读；不要读 relais/inbox/ 里其他模块的信；不要自己跑 relais auto-turn 等 hook 命令。
`, side, channel, sideDir)
}
```
`localmgr.go` 的 `writeLocalAgentGuide` 改为：
```go
func writeLocalAgentGuide(dir, ld, name string) error {
	p := filepath.Join(dir, "relais", "AGENT.md")
	data, _ := os.ReadFile(p)
	marker := fmt.Sprintf("## 本地模式（模块 %s）", name)
	if strings.Contains(string(data), marker) {
		return nil
	}
	add := guide.LocalText("claude", name, filepath.Join(ld, "sides", "claude")) + guide.LocalText("codex", name, filepath.Join(ld, "sides", "codex"))
	return os.WriteFile(p, append(data, []byte(add)...), 0o644)
}
```
（import `internal/guide`。）`testing.go` 加两个出口；`main.go`/`static.go` 版本 `0.6.0-m8`。

`README.md` 本地模式一节改写为：双击 `安装 Relais 本地模式.command` → 浏览器打开控制台 → 「模块」页新建模块（点选文件夹）→ 频道页「开题」→ 看时间线 → 握手后「确认开工」→ 结论卡片「复制开工指令」贴给工作脑；或者在工作脑里说"把这个拿去讨论"/"开工"。命令行仍可用（`relais local …`），但不再必需。

`docs/decisions.md` 顶部追加：
```markdown
## 2026-09-27 · D44 M8 执行期实现选择

- **问题**：spec §3.2 说把本地逻辑抽成 `internal/local` 包供 CLI 与服务器共用；`internal/cli` 已依赖 `internal/server`（RunServe），server 再依赖 local、local 再用 cli 的 hook/配置助手会成环。
- **考虑过**：把 hook 生成、两侧配置、项目初始化、plist 全搬进新包——被否：搬动量大、只为绕环。
- **选择**：server 定义 `LocalManager` 接口（`internal/server/local.go`），`internal/cli/localmgr.go` 实现，`RunServe` 在 `server.toml` 有 `local_dir` 时注入。共用逻辑仍只有一份。另：网页开题以本人身份发信、信首行 `@<side> 先回` 由接话规则识别（spec §6）；bridge 心跳只存服务器内存，20 秒内有心跳算在跑。
- **状态**：live（v0.6.0-m8）。
- **反转触发**：若第三个消费者（如 M9 密封轮）也要这套逻辑且不在 cli 包 → 届时再抽包。
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./... -race`
Expected: 全 PASS。

- [ ] **Step 5: 地板 + 真实冒烟**

`./scripts/check.sh` 绿。真实冒烟（隔离方式同 M7：`RELAIS_LOCAL_DIR=$(mktemp -d)`，`HOME` 不改，端口 18080，`--no-service`，三进程后台 + trap 杀掉，结束删干净）：
1. `go build -o /tmp/relais-m8 .`；`/tmp/relais-m8 local bootstrap --claude "$(which claude)" --codex "$HOME/.codex/plugins/.plugin-appserver/codex" --listen 127.0.0.1:18080 --no-service --json` → 记下密码。
2. 起 serve 与两个 bridge（bridge 的 `RELAIS_CONFIG_DIR` 指向各侧目录）。
3. 用 curl 走网页会做的事：登录取 cookie → `POST /api/local/modules {"name":"smoke","dir":"/tmp/relais-smoke"}`（先 `mkdir -p /tmp/relais-smoke`）→ `GET /api/local/modules` 确认两侧 `bridge_alive` 在 20 秒内变 true（bridge 重读登记表 + 心跳）→ `POST /api/channels/smoke/messages` 开题，`body_md` 以 `@codex 先回` 开头，正文说仓库是假想的按假设讨论。
4. 看日志：claude 侧应打印"由 codex 侧接话，本侧不回"，codex 侧回信；之后正常来回到握手；`POST auto/kickoff`；`conclusions/smoke-*.md` 出现；`GET /api/local/modules` 的 `conclusions` 计数为 1。
5. 安装脚本本身只做 `bash -n` 与"干跑"（Task 5 Step 5），不在本机真装 launchd。
6. 清理进程、目录、冒烟产生的 claude/codex 会话记录（只删 cwd 为 /tmp/relais-smoke 的）。
把冒烟记录写进提交信息或 `docs/`（不要新建 STATE 文件，写进 commit message 即可）。

- [ ] **Step 6: 提交**

```bash
git add internal/guide internal/cli e2e main.go internal/server/static.go README.md docs/decisions.md
git commit -m "feat(m8): 工作脑本地模式说明 + M8 锚点 + version 0.6.0-m8 + README + D44

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

## 执行顺序

Task 1 → 2 → 3 → 4 → 5 → 6，串行（4 只依赖 2，可与 3 并行，但为 check.sh 稳定仍串行）。

## 交付后

按 [[feedback-milestone-push]]：M8 全绿 + 冒烟通过 = 里程碑完成，合并 main（Hou 自己 ff-merge，见 M7 经验：自动模式无法合并）并打 `v0.6.0-m8`。线上不部署（D35）。
