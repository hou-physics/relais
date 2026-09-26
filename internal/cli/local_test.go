package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/hou-physics/relais/internal/store"
)

func TestLocalInitIsIdempotent(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	proj := t.TempDir()
	args := []string{"init", "--claude", "/bin/echo", "--codex", "/bin/cat", "--project", proj, "--no-service", "grammar", "reader"}
	if err := RunLocal(args); err != nil {
		t.Fatal(err)
	}
	if err := RunLocal(args); err != nil {
		t.Fatalf("第二次应幂等: %v", err)
	}
	// 服务器配置
	var sc ServerConfig
	if _, err := toml.DecodeFile(filepath.Join(ld, "server.toml"), &sc); err != nil || sc.Listen != "127.0.0.1:8080" || sc.DataDir != filepath.Join(ld, "data") {
		t.Fatalf("server.toml 错: %+v %v", sc, err)
	}
	// 库：三用户、两频道各三成员、auto 开启 cap=16 supervised
	st, err := store.Open(filepath.Join(ld, "data", "relais.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, n := range []string{"claude", "codex", "hou"} {
		if _, err := st.UserByName(n); err != nil {
			t.Fatalf("用户 %s 应存在: %v", n, err)
		}
	}
	hou, _ := st.UserByName("hou")
	if !hou.IsAdmin {
		t.Fatal("hou 应是管理员（网页登录用）")
	}
	for _, chName := range []string{"grammar", "reader"} {
		ch, err := st.ChannelByName(chName)
		if err != nil {
			t.Fatalf("频道 %s 应存在", chName)
		}
		if ms, _ := st.ListMembers(ch.ID); len(ms) != 3 {
			t.Fatalf("频道 %s 应 3 成员: %v", chName, ms)
		}
		a, _ := st.GetAuto(ch.ID)
		if !a.Enabled || a.Cap != 16 || a.Mode != "supervised" {
			t.Fatalf("本地频道默认 enabled cap=16 supervised: %+v", a)
		}
	}
	// 两侧配置目录
	for _, side := range []string{"claude", "codex"} {
		d := filepath.Join(ld, "sides", side)
		var g GlobalConfig
		if _, err := toml.DecodeFile(filepath.Join(d, "config.toml"), &g); err != nil || g.Username != side || g.Server != "http://127.0.0.1:8080" || g.Token == "" {
			t.Fatalf("%s config.toml 错: %+v %v", side, g, err)
		}
		u, _ := st.UserByName(side)
		if g.Token != u.AgentToken {
			t.Fatalf("%s token 应与库一致", side)
		}
		var si SetupInfo
		toml.DecodeFile(filepath.Join(d, "setup.toml"), &si)
		if si.Agent != side || si.Mode != "auto" || !strings.HasSuffix(si.HookPath, "auto-reply.sh") {
			t.Fatalf("%s setup.toml 错: %+v", side, si)
		}
		if _, err := os.Stat(si.HookPath); err != nil {
			t.Fatalf("%s hook 应存在", side)
		}
		ps, _ := loadProjectsIn(d)
		if len(ps) != 2 {
			t.Fatalf("%s projects.toml 应登记 2 个频道（重复 init 不重复登记）: %v", side, ps)
		}
	}
	// 项目目录
	if _, err := os.Stat(filepath.Join(proj, "relais", "RULES.md")); err != nil {
		t.Fatal("应生成 RULES.md")
	}
	var pc ProjectConfig
	toml.DecodeFile(filepath.Join(proj, "relais", "config.toml"), &pc)
	if pc.Channel != "grammar" { // 第一个模块作默认绑定；其余模块靠 RELAIS_CHANNEL（Task 4）
		t.Fatalf("项目绑定错: %+v", pc)
	}
	// 人的账号只写一次
	if data, err := os.ReadFile(filepath.Join(ld, "human.txt")); err != nil || !strings.Contains(string(data), "hou") {
		t.Fatalf("human.txt 应含账号: %v", err)
	}
}

func TestLocalInitRequiresBothAgents(t *testing.T) {
	t.Setenv("RELAIS_LOCAL_DIR", t.TempDir())
	err := RunLocal([]string{"init", "--claude", "/bin/echo", "--codex", "/nonexistent/codex", "--project", t.TempDir(), "--no-service", "m1"})
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("codex 路径不存在应报错: %v", err)
	}
}

func TestLocalStatusAndClose(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	proj := t.TempDir()
	if err := RunLocal([]string{"init", "--claude", "/bin/echo", "--codex", "/bin/cat", "--project", proj, "--no-service", "m1"}); err != nil {
		t.Fatal(err)
	}
	sessionSet(filepath.Join(ld, "sides", "claude"), "m1", "sid-1")
	if err := RunLocal([]string{"status"}); err != nil {
		t.Fatal(err)
	}
	if err := RunLocal([]string{"close", "m1"}); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Open(filepath.Join(ld, "data", "relais.db"))
	defer st.Close()
	ch, _ := st.ChannelByName("m1")
	if a, _ := st.GetAuto(ch.ID); !a.Closed {
		t.Fatal("close 后应 closed")
	}
	if id, _ := sessionGet(filepath.Join(ld, "sides", "claude"), "m1"); id != "" {
		t.Fatal("close 应作废会话 id")
	}
	if err := RunLocal([]string{"close", "nope"}); err == nil {
		t.Fatal("关闭不存在的频道应报错")
	}
}
