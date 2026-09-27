package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
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
	mk("Library/proj-x", true) // macOS 受 TCC 保护的目录，不进
	mk("Music/proj-y", true)
	mk("Documents/proj-z", true) // 文稿受 TCC 保护，扫描不进
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
	// 目录名含 ".." 字样但不是上级目录段：合法
	dotted := filepath.Join(t.TempDir(), "foo..bar")
	os.MkdirAll(dotted, 0o755)
	if _, err := m.CreateModule("dotted", dotted); err != nil {
		t.Fatalf("foo..bar 应被接受: %v", err)
	}
	// 已绑定的模块不能改绑到别的目录
	if _, err := m.CreateModule("grammar", t.TempDir()); !errors.Is(err, server.ErrLocalInvalid) || !strings.Contains(err.Error(), "不能改绑") {
		t.Fatalf("改绑目录应 ErrLocalInvalid: %v", err)
	}
	if err := m.CloseModule("dotted"); err != nil {
		t.Fatal(err)
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
	if err != nil || len(mods) != 3 {
		t.Fatalf("应列出 3 个模块（含已关闭的 dotted）: %v %v", mods, err)
	}
	// 结论计数
	os.WriteFile(filepath.Join(proj, "relais", "conclusions", "grammar-01AAAAAAAAAAAAAAAAAAAAAAAA.md"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(proj, "relais", "conclusions", "reader-01BBBBBBBBBBBBBBBBBBBBBBBB.md"), []byte("x"), 0o644)
	mods, _ = m.ListModules()
	for _, md := range mods {
		if md.Name != "dotted" && md.Conclusions != 1 {
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

func decodeTOMLFile(t *testing.T, path string, v any) {
	t.Helper()
	if _, err := toml.DecodeFile(path, v); err != nil {
		t.Fatal(err)
	}
}

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
	if strings.Contains(string(data2), "relais draft") || strings.Contains(string(data2), "# Relais — agent 使用说明") {
		t.Fatalf("本地模块的 AGENT.md 不应含联网说明:\n%s", data2)
	}
	if !strings.HasPrefix(string(data2), "# Relais — 本地模式 agent 说明") {
		t.Fatalf("AGENT.md 应以本地开头:\n%s", data2)
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md"} {
		b, err := os.ReadFile(filepath.Join(proj, f))
		if err != nil {
			t.Fatalf("应创建 %s: %v", f, err)
		}
		if strings.Count(string(b), "<!-- relais-local -->") != 1 || !strings.Contains(string(b), "relais/AGENT.md") {
			t.Fatalf("%s 指针块应恰好一次:\n%s", f, b)
		}
	}
}

func TestCreateModuleKeepsExistingClaudeMD(t *testing.T) {
	m, _ := newMgrForTest(t)
	proj := t.TempDir()
	orig := "# 我的项目\n\n已有规矩：别动 main。\n"
	os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte(orig), 0o644)
	for i := 0; i < 2; i++ {
		if _, err := m.CreateModule("grammar", proj); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md"))
	if !strings.HasPrefix(string(b), orig) || strings.Count(string(b), "<!-- relais-local -->") != 1 {
		t.Fatalf("已有 CLAUDE.md 内容应保留且只追加一次指针块:\n%s", b)
	}
}

func TestCreateModuleTwoModulesSameDirGuide(t *testing.T) {
	m, _ := newMgrForTest(t)
	proj := t.TempDir()
	for _, n := range []string{"grammar", "reader", "grammar", "reader"} {
		if _, err := m.CreateModule(n, proj); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile(filepath.Join(proj, "relais", "AGENT.md"))
	s := string(b)
	for _, n := range []string{"grammar", "reader"} {
		if c := strings.Count(s, "## 本地模式（模块 "+n+"）"); c != 1 {
			t.Fatalf("模块 %s 说明段应恰好一次，实为 %d:\n%s", n, c, s)
		}
	}
	if !strings.Contains(s, `RELAIS_CHANNEL="reader"`) || strings.Contains(s, "relais draft") {
		t.Fatalf("reader 段缺失或混入联网说明:\n%s", s)
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md"} {
		b, _ := os.ReadFile(filepath.Join(proj, f))
		if strings.Count(string(b), "<!-- relais-local -->") != 1 {
			t.Fatalf("%s 指针块应恰好一次", f)
		}
	}
}

func TestCreateModuleNetworkedAgentMDReplaced(t *testing.T) {
	// 旧版本（M8 修复前）留下的 AGENT.md：联网开头 + 已有本地段；重跑后换开头、保留本地段
	m, _ := newMgrForTest(t)
	proj := t.TempDir()
	if _, err := m.CreateModule("grammar", proj); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(proj, "relais", "AGENT.md")
	b, _ := os.ReadFile(p)
	legacy := strings.Replace(string(b), "# Relais — 本地模式 agent 说明", "# Relais — agent 使用说明\n\n用 relais draft 起草", 1)
	os.WriteFile(p, []byte(legacy), 0o644)
	if _, err := m.CreateModule("grammar", proj); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(p)
	if strings.Contains(string(b2), "relais draft") || strings.Count(string(b2), "## 本地模式（模块 grammar）") != 1 {
		t.Fatalf("旧联网开头应被替换、本地段保留:\n%s", b2)
	}
}

func TestCreateModulePermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	m, _ := newMgrForTest(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "proj")
	os.MkdirAll(dir, 0o755)
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	_, err := m.CreateModule("grammar", dir)
	if err == nil || !strings.Contains(err.Error(), "macOS 未授权 relais 访问该文件夹") {
		t.Fatalf("无权限应给出授权提示，实为: %v", err)
	}
}
