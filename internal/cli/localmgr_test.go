package cli

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/local"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

// bootstrapped：临时 ld + bootstrap 完成的本地管理器。
func bootstrapped(t *testing.T) (string, *localManager) {
	t.Helper()
	ld := t.TempDir()
	mgr := newLocalManager(ld)
	if _, err := mgr.bootstrap("127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	return ld, mgr
}

// fakeCodexHomeFor：照 internal/local/attach_test.go 的 fakeCodexHome 抄的假 Codex 状态库。
func fakeCodexHomeFor(t *testing.T, proj string) string {
	t.Helper()
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, title TEXT NOT NULL, name TEXT, archived INTEGER NOT NULL DEFAULT 0, updated_at_ms INTEGER)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id, cwd, title, name string
		archived             int
		ms                   int64
	}{
		{"t-old", proj, "旧对话", "", 0, 1000},
		{"t-new", proj, "新对话", "巡天主对话", 0, 3000},
		{"t-arch", proj, "已归档", "", 1, 5000},
		{"t-other", "/elsewhere", "别的项目", "", 0, 9000},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO threads VALUES (?,?,?,?,?,?)`, r.id, r.cwd, r.title, r.name, r.archived, r.ms); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestBootstrapIdempotentAndWritesLocalDir(t *testing.T) {
	ld, m := bootstrapped(t)
	res2, err := m.bootstrap("127.0.0.1:18080")
	if err != nil || res2.HumanUser != "hou" || res2.BaseURL != "http://127.0.0.1:18080" {
		t.Fatalf("第二次 bootstrap 应幂等: %+v %v", res2, err)
	}
	data, _ := os.ReadFile(filepath.Join(ld, "server.toml"))
	if !strings.Contains(string(data), "local_dir = ") {
		t.Fatalf("server.toml 应含 local_dir: %s", data)
	}
	cfg, err := loadServerConfig(filepath.Join(ld, "server.toml"))
	if err != nil || cfg.LocalDir != ld {
		t.Fatalf("LocalDir 应可读回: %+v %v", cfg, err)
	}
	for _, p := range []string{"sides", "human.txt"} {
		if _, err := os.Stat(filepath.Join(ld, p)); err == nil {
			t.Fatalf("不应再写 %s", p)
		}
	}
	st, _ := store.Open(filepath.Join(ld, "data", "relais.db"))
	defer st.Close()
	for _, n := range []string{"claude", "codex", "hou"} {
		if _, err := st.UserByName(n); err != nil {
			t.Fatalf("用户 %s 应存在", n)
		}
	}
	if u, _ := st.UserByName("hou"); !u.IsAdmin {
		t.Fatal("hou 应是管理员")
	}
	for k, want := range map[string]string{"local.default_mode": "supervised", "local.default_cap": "8"} {
		if v, _ := st.GetSetting(k); v != want {
			t.Fatalf("setting %s = %q, want %q", k, v, want)
		}
	}
	if _, err := st.GetSetting("local.codex_path"); err != nil {
		t.Fatalf("local.codex_path 读取不应报错: %v", err)
	}
	if _, err := m.bootstrap("0.0.0.0:80"); err == nil || !strings.HasPrefix(err.Error(), "本地模式只允许监听回环") {
		t.Fatalf("非回环应拒绝: %v", err)
	}
}

func TestScanRepos(t *testing.T) {
	_, m := bootstrapped(t)
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

func TestCreateModuleWritesMailboxProtocolPointer(t *testing.T) {
	ld, mgr := bootstrapped(t)
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("# 项目\n"), 0o644)
	m, err := mgr.CreateModule(api.LocalModuleRequest{Name: "黑客松", Dir: proj})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "未接入" || m.RoundCap != 8 || m.Mode != "supervised" {
		t.Fatalf("初始状态: %+v", m)
	}
	for _, p := range []string{"relais/mail/黑客松/outbox", "relais/mail/黑客松/drafts", "relais/PROTOCOL.md", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(proj, p)); err != nil {
			t.Fatalf("缺 %s", p)
		}
	}
	cl, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md"))
	if !strings.HasPrefix(string(cl), "# 项目\n") || !strings.Contains(string(cl), "relais/PROTOCOL.md") || strings.Contains(string(cl), "relais/AGENT.md") {
		t.Fatalf("指针块: %q", cl)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "AGENT.md")); err == nil {
		t.Fatal("不再生成 AGENT.md")
	}
	if _, err := os.Stat(filepath.Join(ld, "sides")); err == nil {
		t.Fatal("不再生成 sides/")
	}
	// 幂等 + 同名换目录拒绝
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "黑客松", Dir: proj}); err != nil {
		t.Fatal("重复创建应幂等")
	}
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "黑客松", Dir: t.TempDir()}); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("同名换目录应拒绝: %v", err)
	}
	mods, _ := mgr.ListModules()
	if len(mods) != 1 || mods[0].Name != "黑客松" {
		t.Fatalf("ListModules: %+v", mods)
	}
	// 非法输入
	for _, bad := range []struct{ name, dir string }{{"a/b", proj}, {"", proj}, {"x", "/nonexistent/dir"}, {"x", proj + "/../" + filepath.Base(proj)}, {"x", "relative"}} {
		if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: bad.name, Dir: bad.dir}); !errors.Is(err, server.ErrLocalInvalid) {
			t.Fatalf("%+v 应为 ErrLocalInvalid: %v", bad, err)
		}
	}
}

func TestCreateModuleKeepsCustomProtocol(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, "relais"), 0o755)
	os.WriteFile(filepath.Join(proj, "relais", "PROTOCOL.md"), []byte("我自己的协议\n"), 0o644)
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatalf("手改过的协议只记日志、不报错: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(proj, "relais", "PROTOCOL.md")); string(b) != "我自己的协议\n" {
		t.Fatalf("手改过的协议不应被覆盖: %q", b)
	}
}

func TestModuleStateFromMailbox(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	md := filepath.Join(proj, "relais", "mail", "m")
	os.WriteFile(filepath.Join(md, "001-hou.md"), []byte("---\nseq: 1\nfrom: hou\nkind: letter\nsummary: 议题\n---\n\n@codex 先回\n\n用什么缓存"), 0o644)
	mods, _ := mgr.ListModules()
	if m := mods[0]; m.State != "讨论中" || m.LastSeq != 1 || m.LastFrom != "hou" || m.WaitingFor != "codex" {
		t.Fatalf("雇主开题后: %+v", m)
	}
	os.WriteFile(filepath.Join(md, "002-codex.md"), []byte("---\nseq: 2\nfrom: codex\nkind: letter\nsummary: 回\n---\n\n用 redis"), 0o644)
	mods, _ = mgr.ListModules()
	if m := mods[0]; m.WaitingFor != "claude" || m.LastSeq != 2 {
		t.Fatalf("codex 回信后应等 claude: %+v", m)
	}
	os.WriteFile(filepath.Join(md, "outbox", "x.md.rejected"), []byte("坏信"), 0o644)
	mods, _ = mgr.ListModules()
	if m := mods[0]; m.State != "等你" || len(m.Rejected) != 1 || m.Rejected[0] != "x.md.rejected" {
		t.Fatalf("有被拒的信应等你: %+v", m)
	}
}

func TestPatchRenameMovesMailDirAndRejectsTaken(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "a", Dir: proj})
	mgr.CreateModule(api.LocalModuleRequest{Name: "b", Dir: proj})
	os.WriteFile(filepath.Join(proj, "relais", "mail", "a", "001-hou.md"), []byte("---\nseq: 1\nfrom: hou\nkind: letter\n---\n\nx"), 0o644)
	if _, err := mgr.PatchModule("a", api.LocalModulePatch{Name: "b"}); err == nil {
		t.Fatal("重名应拒绝")
	}
	m, err := mgr.PatchModule("a", api.LocalModulePatch{Name: "c", Mode: "autopilot", RoundCap: 3})
	if err != nil || m.Name != "c" || m.Mode != "autopilot" || m.RoundCap != 3 || m.LastSeq != 1 {
		t.Fatalf("patch: %+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "c", "001-hou.md")); err != nil {
		t.Fatal("信箱目录应随改名移动")
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "a")); err == nil {
		t.Fatal("旧目录应不在")
	}
	// 目标信箱目录已存在（残留）：拒绝，且频道名回滚
	os.MkdirAll(filepath.Join(proj, "relais", "mail", "d"), 0o755)
	if _, err := mgr.PatchModule("c", api.LocalModulePatch{Name: "d"}); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("目标目录已存在应拒绝: %v", err)
	}
	if mods, _ := mgr.ListModules(); mods[1].Name != "c" {
		t.Fatalf("频道名应保持 c: %+v", mods)
	}
	if _, err := mgr.PatchModule("c", api.LocalModulePatch{Mode: "yolo"}); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("非法模式应拒绝: %v", err)
	}
	if _, err := mgr.PatchModule("nope", api.LocalModulePatch{Mode: "autopilot"}); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("不存在的模块应拒绝: %v", err)
	}
}

func TestPatchCapKeepsRoundCount(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	st, _, _ := mgr.open()
	lm, _ := st.LocalModuleByName("m")
	st.CountLocalTurn(lm.ChannelID)
	st.CountLocalTurn(lm.ChannelID)
	st.Close()
	m, err := mgr.PatchModule("m", api.LocalModulePatch{RoundCap: 5})
	if err != nil || m.RoundCap != 5 || m.Round != 1 {
		t.Fatalf("改上限不应清零回合: %+v %v", m, err)
	}
}

func TestCloseReopenDeleteModule(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	mgr.CloseModule("m")
	mods, _ := mgr.ListModules()
	if mods[0].State != "已关闭" || !mods[0].Closed {
		t.Fatalf("close: %+v", mods[0])
	}
	if refs, _ := mgr.moduleRefs(); len(refs) != 0 {
		t.Fatalf("已关闭模块不进守卫: %+v", refs)
	}
	mgr.ReopenModule("m")
	mods, _ = mgr.ListModules()
	if mods[0].Closed {
		t.Fatal("reopen")
	}
	if refs, _ := mgr.moduleRefs(); len(refs) != 1 || refs[0].Name != "m" || refs[0].Dir != proj {
		t.Fatalf("重开后进守卫: %+v", refs)
	}
	if err := mgr.DeleteModule("m", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "m")); err != nil {
		t.Fatal("默认不删文件")
	}
	// 保留的信箱里有归档信：同名重建会从 seq 1 覆盖旧信，必须拒绝
	letter := filepath.Join(proj, "relais", "mail", "m", "001-hou.md")
	os.WriteFile(letter, []byte("---\nseq: 1\nfrom: hou\nkind: letter\n---\n\n旧信"), 0o644)
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); !errors.Is(err, server.ErrLocalInvalid) || !strings.Contains(err.Error(), "旧信件") {
		t.Fatalf("有旧信件时同名重建应拒绝: %v", err)
	}
	if b, _ := os.ReadFile(letter); !strings.Contains(string(b), "旧信") {
		t.Fatal("旧信不应被动")
	}
	os.Remove(letter) // 雇主清掉旧信后可重建
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatalf("清掉旧信后应可重建: %v", err)
	}
	os.WriteFile(letter, []byte("---\nseq: 1\nfrom: hou\nkind: letter\n---\n\n新信"), 0o644)
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatalf("已登记模块的重复创建仍应幂等: %v", err)
	}
	if err := mgr.DeleteModule("m", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "m")); err == nil {
		t.Fatal("files=1 应删信箱目录")
	}
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatalf("连文件删除后应可重建: %v", err)
	}
	mgr.DeleteModule("m", true)
	if _, err := os.Stat(filepath.Join(proj, "relais", "PROTOCOL.md")); err != nil {
		t.Fatal("删模块不应删协议")
	}
	if mods, _ := mgr.ListModules(); len(mods) != 0 {
		t.Fatal("删除后列表应空")
	}
	for _, err := range []error{mgr.CloseModule("nope"), mgr.ReopenModule("nope"), mgr.DeleteModule("nope", true)} {
		if !errors.Is(err, server.ErrLocalInvalid) {
			t.Fatalf("不存在的模块应 ErrLocalInvalid: %v", err)
		}
	}
}

func TestDeleteModuleRefusesSymlinkedMailRoot(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj, outside := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(proj, "relais"), 0o755)
	if err := os.Symlink(outside, filepath.Join(proj, "relais", "mail")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.DeleteModule("m", true); !errors.Is(err, server.ErrLocalInvalid) || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("符号链接信箱应拒绝删除: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "m", "outbox")); err != nil {
		t.Fatal("项目外的目标不应被动")
	}
	if mods, _ := mgr.ListModules(); len(mods) != 1 {
		t.Fatal("拒绝删除时模块应仍在")
	}
}

func TestModuleInfoReportsMailboxReadError(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	md := filepath.Join(proj, "relais", "mail", "m")
	os.RemoveAll(md)
	os.WriteFile(md, []byte("不是目录"), 0o644) // ReadDir 报 ENOTDIR，而非不存在
	if _, err := mgr.ListModules(); err == nil {
		t.Fatal("信箱读失败应报错而不是显示未接入")
	}
	os.Remove(md)
	if mods, err := mgr.ListModules(); err != nil || mods[0].State != "未接入" || !mods[0].MailboxMissing {
		t.Fatalf("信箱目录不在时按空信箱显示，并报 mailbox_missing: %+v %v", mods, err)
	}
}

func TestAttachAndConversations(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	t.Setenv("CODEX_HOME", fakeCodexHomeFor(t, proj))
	t.Setenv("HOME", t.TempDir())
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	convs, err := mgr.Conversations("codex", proj)
	if err != nil || len(convs) != 2 || convs[0].Name != "巡天主对话" {
		t.Fatalf("conversations: %+v %v", convs, err)
	}
	if convs, err := mgr.Conversations("claude", proj); err != nil || convs == nil || len(convs) != 0 {
		t.Fatalf("claude 侧无会话应为空数组: %+v %v", convs, err)
	}
	if _, err := mgr.Conversations("kimi", proj); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("未知侧应 invalid: %v", err)
	}
	m, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "codex"})
	if err != nil || !m.Codex.Attached || m.Codex.ThreadName != "巡天主对话" {
		t.Fatalf("attach 默认取最新: %+v %v", m, err)
	}
	st, _, _ := mgr.open()
	lm, _ := st.LocalModuleByName("m")
	st.Close()
	if lm.CodexThreadID != "t-new" || lm.CodexThreadName != "巡天主对话" {
		t.Fatalf("登记表应记下接入: %+v", lm)
	}
	if m, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "codex", Thread: "t-old"}); err != nil || m.Codex.ThreadName != "旧对话" {
		t.Fatalf("按 id 接入（无名用标题）: %+v %v", m, err)
	}
	if _, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "codex", Thread: "不存在"}); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("找不到应拒绝: %v", err)
	}
	if _, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "claude"}); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("claude 侧不登记: %v", err)
	}
	n, err := mgr.CreateModule(api.LocalModuleRequest{Name: "n", Dir: proj, CodexThread: "t-old"})
	if err != nil || !n.Codex.Attached {
		t.Fatalf("创建时顺手接入: %+v %v", n, err)
	}
}

func TestRedeliverNeedsDaemon(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	if err := mgr.Redeliver("m"); err == nil {
		t.Fatal("守卫未启动时应报错")
	}
	d, err := mgr.newDaemon(func(int64, *store.Message, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Store.Close()
	if d.Users["hou"] == 0 || d.Users["claude"] == 0 || d.Users["codex"] == 0 {
		t.Fatalf("守卫应知道三个账号: %+v", d.Users)
	}
	if refs, err := d.Modules(); err != nil || len(refs) != 1 {
		t.Fatalf("守卫模块列表: %+v %v", refs, err)
	}
	if err := mgr.Redeliver("m"); err == nil || !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("没有需要投给 Codex 的信: %v", err)
	}
	if err := mgr.Redeliver("nope"); !errors.Is(err, server.ErrLocalInvalid) {
		t.Fatalf("不存在的模块: %v", err)
	}
}

func TestSettingsAndState(t *testing.T) {
	_, mgr := bootstrapped(t)
	s, _ := mgr.Settings()
	if s.DefaultMode != "supervised" || s.DefaultCap != 8 {
		t.Fatalf("默认设置: %+v", s)
	}
	fake := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(fake, []byte("#!/bin/sh\necho ok"), 0o755)
	if err := mgr.PutSettings(api.LocalSettings{CodexPath: fake, DefaultMode: "autopilot", DefaultCap: 5, NotifyEveryLetter: true}); err != nil {
		t.Fatal(err)
	}
	s, _ = mgr.Settings()
	if s.CodexPath != fake || !s.CodexOK || s.DefaultMode != "autopilot" || s.DefaultCap != 5 || !s.NotifyEveryLetter {
		t.Fatalf("settings: %+v", s)
	}
	for _, bad := range []api.LocalSettings{
		{CodexPath: "/nope/codex", DefaultMode: "supervised", DefaultCap: 8},
		{CodexPath: fake, DefaultMode: "yolo", DefaultCap: 8},
		{CodexPath: fake, DefaultMode: "supervised", DefaultCap: 0},
		{CodexPath: t.TempDir(), DefaultMode: "supervised", DefaultCap: 8},
	} {
		if err := mgr.PutSettings(bad); !errors.Is(err, server.ErrLocalInvalid) {
			t.Fatalf("%+v 应拒绝: %v", bad, err)
		}
	}
	if st := mgr.State(); st.Version == "" || !st.CodexOK || st.StartedAt.IsZero() {
		t.Fatalf("state: %+v", st)
	}
	// 新建模块沿用默认模式与上限
	m, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: t.TempDir()})
	if err != nil || m.Mode != "autopilot" || m.RoundCap != 5 {
		t.Fatalf("新模块应沿用默认: %+v %v", m, err)
	}
}

func TestMigrateProjectsToml(t *testing.T) {
	ld, mgr := bootstrapped(t)
	proj := t.TempDir()
	// 模拟 M8 遗留：频道存在、sides/claude/projects.toml 有登记、local_modules 没有
	st, _, _ := mgr.open()
	ch, _ := st.CreateChannel("old")
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.UserByName(n)
		st.AddMember(ch.ID, u.ID)
	}
	u, _ := st.UserByName("hou")
	st.SaveMessage(ch.ID, u.ID, nil, "旧信", "x", "")
	seedHistoryWithKickoff(t, st, ch.ID) // 终审修复 #3：迁移前已有握手与 kickoff
	st.Close()
	os.MkdirAll(filepath.Join(ld, "sides", "claude"), 0o755)
	registerProjectIn(filepath.Join(ld, "sides", "claude"), "old", proj)
	if _, err := mgr.bootstrap("127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	mods, _ := mgr.ListModules()
	if len(mods) != 1 || mods[0].Name != "old" || mods[0].Dir != proj {
		t.Fatalf("应导入旧登记: %+v", mods)
	}
	assertNoReplay(t, mgr, proj, "old")
	st, _, _ = mgr.open()
	defer st.Close()
	lm, _ := st.LocalModuleByName("old")
	if lm.ArchivedSeq != 4 {
		t.Fatalf("旧信不重新归档，archived_seq 应为当前最大 seq: %d", lm.ArchivedSeq)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "PROTOCOL.md")); err != nil {
		t.Fatal("升级应给旧模块写协议")
	}
	// 幂等：再跑一次不改 archived_seq
	st.SetArchivedSeq(lm.ChannelID, 7)
	if err := mgr.migrateProjectsToml(); err != nil {
		t.Fatal(err)
	}
	if lm, _ := st.LocalModuleByName("old"); lm.ArchivedSeq != 7 {
		t.Fatalf("已登记的不再导入: %d", lm.ArchivedSeq)
	}
}

func TestCreateModulePermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 不受目录权限限制")
	}
	_, m := bootstrapped(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "proj")
	os.MkdirAll(dir, 0o755)
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	_, err := m.CreateModule(api.LocalModuleRequest{Name: "grammar", Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "macOS 未授权 relais 访问该文件夹") {
		t.Fatalf("无权限应给出授权提示，实为: %v", err)
	}
}

// 终审修复 #1：wait 收信即退出，Claude 回信期间 waiting=false；游标读到最后一封给它的信 = 正在回。
func TestModuleInfoClaudeWorking(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	md := filepath.Join(proj, "relais", "mail", "m")
	os.WriteFile(filepath.Join(md, "001-claude.md"), []byte("---\nseq: 1\nfrom: claude\nkind: letter\nsummary: 开题\n---\n\nx"), 0o644)
	os.WriteFile(filepath.Join(md, "002-codex.md"), []byte("---\nseq: 2\nfrom: codex\nkind: letter\nsummary: 回\n---\n\ny"), 0o644)
	os.WriteFile(filepath.Join(md, ".cursor-claude"), []byte(`{"seq":1}`), 0o644)
	mods, _ := mgr.ListModules()
	if m := mods[0]; m.Claude.Working || m.Claude.Cursor != 1 || m.WaitingFor != "claude" {
		t.Fatalf("游标落后于最后一封 codex 信，不算正在回: %+v", m.Claude)
	}
	os.WriteFile(filepath.Join(md, ".cursor-claude"), []byte(`{"seq":2}`), 0o644)
	mods, _ = mgr.ListModules()
	if m := mods[0]; !m.Claude.Working || m.Claude.Waiting || m.Claude.Cursor != 2 || m.State != "讨论中" {
		t.Fatalf("游标等于最后一封 codex 信应算正在回: %+v state=%s", m.Claude, m.State)
	}
}

// seedHistoryWithKickoff：频道里写一封雇主给 codex 的信、一次 codex 提议 + claude 附和的握手、再开工。
func seedHistoryWithKickoff(t *testing.T, st *store.Store, chID int64) {
	t.Helper()
	id := map[string]int64{}
	for _, n := range []string{"claude", "codex", "hou"} {
		u, err := st.UserByName(n)
		if err != nil {
			t.Fatal(err)
		}
		id[n] = u.ID
	}
	if _, err := st.SaveMessage(chID, id["hou"], []int64{id["codex"], id["claude"]}, "问", "@codex 先回\n\n问", ""); err != nil {
		t.Fatal(err)
	}
	m1, err := st.SaveMessageOpts(chID, id["codex"], []int64{id["claude"], id["hou"]}, "提议", "提议", "", store.SaveOpts{Kind: "resolved", Owner: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := st.SaveMessageOpts(chID, id["claude"], []int64{id["codex"], id["hou"]}, "附和", "附和", "", store.SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := st.EvaluateHandshake(chID, m2.ID); err != nil || res != store.HandshakeDone {
		t.Fatalf("握手: %v %v", res, err)
	}
	if _, err := st.Kickoff(chID, id["hou"]); err != nil {
		t.Fatal(err)
	}
}

// assertNoReplay：给已登记模块接上 codex、起守卫跑一轮——旧 kickoff 不重写、旧信不补投。
func assertNoReplay(t *testing.T, mgr *localManager, proj, name string) {
	t.Helper()
	logf := filepath.Join(t.TempDir(), "codex.log")
	script := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+logf+"\"\n"), 0o755)
	if err := mgr.PutSettings(api.LocalSettings{CodexPath: script, DefaultMode: "supervised", DefaultCap: 8}); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(proj, "relais", "mail", name)
	if err := local.WriteAttach(md, local.Attach{ThreadID: "t-1", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d, err := mgr.newDaemon(func(int64, *store.Message, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Store.Close(); mgr.shared, mgr.daemon = nil, nil }()
	if err := d.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if ks, _ := filepath.Glob(filepath.Join(md, "kickoff-*.md")); len(ks) != 0 {
		t.Fatalf("旧 kickoff 不该重新归档: %v", ks)
	}
	if data, err := os.ReadFile(logf); err == nil {
		t.Fatalf("旧信不该补投给 codex: %q", data)
	}
}

// 终审修复 #3：CreateModule 收编已有历史（含 kickoff）的频道，同样不重放。
func TestCreateModuleAdoptsChannelWithoutReplay(t *testing.T) {
	_, mgr := bootstrapped(t)
	st, _, _ := mgr.open()
	ch, _ := st.CreateChannel("legacy")
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.UserByName(n)
		st.AddMember(ch.ID, u.ID)
	}
	st.SetAutoEnabled(ch.ID, true, 16)
	seedHistoryWithKickoff(t, st, ch.ID)
	st.Close()
	proj := t.TempDir()
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "legacy", Dir: proj}); err != nil {
		t.Fatal(err)
	}
	st, _, _ = mgr.open()
	lm, _ := st.LocalModuleByName("legacy")
	st.Close()
	if lm.ArchivedSeq != 3 {
		t.Fatalf("收编已有频道：archived_seq 应为当前最大 seq: %d", lm.ArchivedSeq)
	}
	assertNoReplay(t, mgr, proj, "legacy")
}

// 终审修复 #4：守卫在跑时改名走 daemon.Do 串行；改完旧名目录不会被守卫"复活"，
// 信箱目录不见了时模块状态报 mailbox_missing。
func TestPatchRenameWithDaemonRunning(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "a", Dir: proj}); err != nil {
		t.Fatal(err)
	}
	d, err := mgr.newDaemon(func(int64, *store.Message, string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { d.Store.Close(); mgr.shared, mgr.daemon = nil, nil }()
	d.Interval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.Run(ctx); close(done) }()
	for i := 0; i < 5; i++ {
		from, to := "a", "b"
		if i%2 == 1 {
			from, to = "b", "a"
		}
		if _, err := mgr.PatchModule(from, api.LocalModulePatch{Name: to}); err != nil {
			cancel()
			<-done
			t.Fatalf("第 %d 次改名 %s→%s: %v", i, from, to, err)
		}
		time.Sleep(15 * time.Millisecond)
	}
	cancel()
	<-done
	d.RunOnce()
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "b")); err != nil {
		t.Fatal("改名后的信箱应在")
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "a")); !os.IsNotExist(err) {
		t.Fatal("旧名目录不该被守卫重建")
	}
	os.RemoveAll(filepath.Join(proj, "relais", "mail", "b"))
	d.RunOnce()
	mods, _ := mgr.ListModules()
	if len(mods) != 1 || !mods[0].MailboxMissing {
		t.Fatalf("信箱不见了应报 mailbox_missing: %+v", mods)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "b")); !os.IsNotExist(err) {
		t.Fatal("守卫不该重建不见了的信箱")
	}
}
