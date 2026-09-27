package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

// spec §11：已存在的 server.toml 若监听非回环地址，init 必须报错，不能因"已存在不覆盖"而绕过。
func TestLocalInitRejectsNonLoopbackExistingConfig(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	sc := "listen = \"0.0.0.0:8080\"\ndata_dir = " + strconv.Quote(filepath.Join(ld, "data")) + "\nbase_url = \"http://0.0.0.0:8080\"\n"
	if err := os.WriteFile(filepath.Join(ld, "server.toml"), []byte(sc), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RunLocal([]string{"init", "--claude", "/bin/echo", "--codex", "/bin/cat", "--project", t.TempDir(), "--no-service", "m1"})
	if err == nil || !strings.Contains(err.Error(), "回环") {
		t.Fatalf("非回环 listen 的既有 server.toml 应报错: %v", err)
	}
}

func TestPlistNeedsInstall(t *testing.T) {
	p := filepath.Join(t.TempDir(), "com.relais.local.serve.plist")
	exe := "/opt/homebrew/bin/relais"
	if !plistNeedsInstall(p, exe) {
		t.Fatal("plist 不存在时应安装")
	}
	os.WriteFile(p, []byte("<plist><dict><key>ProgramArguments</key><array><string>"+exe+"</string><string>serve</string></array></dict></plist>"), 0o644)
	if plistNeedsInstall(p, exe) {
		t.Fatal("plist 已指向当前二进制时应跳过")
	}
	if !plistNeedsInstall(p, "/usr/local/bin/relais") {
		t.Fatal("plist 指向别的二进制（升级换了安装位置）时应重装")
	}
}

func TestLocalInitQuotesPaths(t *testing.T) {
	ld := filepath.Join(t.TempDir(), "Application Support", "relais-local")
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := RunLocal([]string{"init", "--claude", "/bin/echo", "--codex", "/bin/cat", "--project", t.TempDir(), "--no-service", "m1"})
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"'" + filepath.Join(ld, "human.txt") + "'",
		"relais serve --config '" + filepath.Join(ld, "server.toml") + "'",
		"--hook '" + filepath.Join(ld, "sides", "codex", "hooks", "auto-reply.sh") + "'",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("输出里的路径应加引号 %q:\n%s", want, out)
		}
	}
}

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

func TestLocalBootstrapCommandJSON(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	out := captureStdout(t, func() {
		if err := RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/bin/cat", "--listen", "127.0.0.1:18098", "--no-service", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var res struct {
		BaseURL       string `json:"base_url"`
		HumanUser     string `json:"human_user"`
		HumanPassword string `json:"human_password"`
		PasswordShown bool   `json:"password_shown"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil || res.BaseURL != "http://127.0.0.1:18098" || res.HumanUser != "hou" || !res.PasswordShown || res.HumanPassword == "" {
		t.Fatalf("bootstrap --json 输出错: %q %v", out, err)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 0 {
		t.Fatalf("bootstrap --json 的 stdout 应恰好一行: %q", out)
	}
	if _, err := os.Stat(filepath.Join(ld, "sides", "codex", "hooks", "auto-reply.sh")); err != nil {
		t.Fatal("bootstrap 应生成两侧 hook")
	}
	// 第二次：password_shown=false，不建模块
	out = captureStdout(t, func() {
		RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/bin/cat", "--listen", "127.0.0.1:18098", "--no-service", "--json"})
	})
	if !strings.Contains(out, `"password_shown":false`) {
		t.Fatalf("第二次不应再显示密码: %q", out)
	}
	if err := RunLocal([]string{"bootstrap", "--claude", "/bin/echo", "--codex", "/nonexistent", "--no-service"}); err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("codex 路径无效应报错: %v", err)
	}
}

func TestPrintBootstrapJSONOneLine(t *testing.T) {
	var b strings.Builder
	if err := printBootstrapJSON(&b, bootstrapResult{BaseURL: "http://127.0.0.1:8080", HumanUser: "hou", HumanPassword: "pw", PasswordShown: true}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("应恰好一行: %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil || len(m) != 4 || m["password_shown"] != true || m["base_url"] != "http://127.0.0.1:8080" {
		t.Fatalf("JSON 键不对: %q %v", out, err)
	}
}

// spec §11：server.toml 带 local_dir 却监听非回环地址时，serve 必须拒绝启动（不能把本地管理接口挂到公网）。
func TestRunServeRefusesLocalDirOnNonLoopback(t *testing.T) {
	d := t.TempDir()
	sc := "listen = \"0.0.0.0:0\"\ndata_dir = " + strconv.Quote(filepath.Join(d, "data")) + "\nbase_url = \"http://x\"\nlocal_dir = " + strconv.Quote(d) + "\n"
	p := filepath.Join(d, "server.toml")
	os.WriteFile(p, []byte(sc), 0o600)
	err := RunServe([]string{"--config", p})
	if err == nil || !strings.Contains(err.Error(), "不是回环地址") {
		t.Fatalf("应拒绝启动: %v", err)
	}
}
