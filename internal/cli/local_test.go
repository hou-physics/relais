package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/local"
	"github.com/hou-physics/relais/internal/store"
)

func TestLocalInitIsIdempotent(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	proj := t.TempDir()
	args := []string{"init", "--project", proj, "--no-service", "grammar", "reader"}
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
	lms, _ := st.LocalModules()
	if len(lms) != 2 || lms[0].Name != "grammar" || lms[0].Dir != proj {
		t.Fatalf("登记表应有 2 个模块（重复 init 不重复登记）: %+v", lms)
	}
	// 项目目录：协议、信箱、指针块；不再有 sides/、human.txt、RULES.md、AGENT.md
	for _, p := range []string{"relais/PROTOCOL.md", "relais/mail/grammar/outbox", "relais/mail/reader/outbox", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(proj, p)); err != nil {
			t.Fatalf("应生成 %s", p)
		}
	}
	for _, p := range []string{filepath.Join(ld, "sides"), filepath.Join(ld, "human.txt"), filepath.Join(proj, "relais", "RULES.md"), filepath.Join(proj, "relais", "AGENT.md")} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("不应再生成 %s", p)
		}
	}
}

func TestLocalStatusAndClose(t *testing.T) {
	ld := t.TempDir()
	t.Setenv("RELAIS_LOCAL_DIR", ld)
	proj := t.TempDir()
	if err := RunLocal([]string{"init", "--project", proj, "--no-service", "m1"}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := RunLocal([]string{"status"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "m1") || !strings.Contains(out, "第 0/8 回合") || !strings.Contains(out, "未接入") || !strings.Contains(out, "claude:没在等") || !strings.Contains(out, "codex:未接入") {
		t.Fatalf("status 输出: %s", out)
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
	if lm, _ := st.LocalModuleByName("m1"); lm.ClosedAt == "" {
		t.Fatal("close 应记下关闭时间")
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
	err := RunLocal([]string{"init", "--project", t.TempDir(), "--no-service", "m1"})
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
	err := RunLocal([]string{"init", "--project", t.TempDir(), "--no-service", "m1"})
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"'" + ld + "'",
		"relais serve --config '" + filepath.Join(ld, "server.toml") + "'",
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
		if err := RunLocal([]string{"bootstrap", "--listen", "127.0.0.1:18098", "--no-service", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	var res map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &res); err != nil || len(res) != 2 || res["base_url"] != "http://127.0.0.1:18098" || res["human_user"] != "hou" {
		t.Fatalf("bootstrap --json 输出错: %q %v", out, err)
	}
	if n := strings.Count(strings.TrimSpace(out), "\n"); n != 0 {
		t.Fatalf("bootstrap --json 的 stdout 应恰好一行: %q", out)
	}
	for _, p := range []string{"sides", "human.txt"} {
		if _, err := os.Stat(filepath.Join(ld, p)); err == nil {
			t.Fatalf("bootstrap 不应再写 %s", p)
		}
	}
	// 第二次：幂等，输出不变
	out2 := captureStdout(t, func() {
		if err := RunLocal([]string{"bootstrap", "--listen", "127.0.0.1:18098", "--no-service", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	if out2 != out || strings.Contains(out2, "password") {
		t.Fatalf("第二次输出应相同且不含密码: %q", out2)
	}
}

func TestPrintBootstrapJSONOneLine(t *testing.T) {
	var b strings.Builder
	if err := printBootstrapJSON(&b, bootstrapResult{BaseURL: "http://127.0.0.1:8080", HumanUser: "hou"}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Fatalf("应恰好一行: %q", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil || len(m) != 2 || m["human_user"] != "hou" || m["base_url"] != "http://127.0.0.1:8080" {
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

func TestRunServeStartsDaemonInLocalMode(t *testing.T) {
	ld := t.TempDir()
	mgr := newLocalManager(ld)
	if _, err := mgr.bootstrap("127.0.0.1:18092"); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatal(err)
	}
	// 在 TempDir 之后登记：清理按 LIFO，先停服务与守卫，再删临时目录
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	t.Cleanup(func() { cancel(); <-served })
	go func() {
		defer close(served)
		if err := runServe(ctx, []string{"--config", localServerConfigPath(ld)}); err != nil {
			t.Errorf("runServe: %v", err)
		}
	}()
	waitListen("127.0.0.1:18092", 5*time.Second)
	md := local.MailDir(proj, "m")
	os.WriteFile(filepath.Join(md, "drafts", "a.md"), []byte("第一封\n"), 0o644)
	if _, err := local.Post(md, "claude", filepath.Join(md, "drafts", "a.md"), local.PostOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(md, "001-claude.md")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("守卫应在几秒内归档 outbox 里的信")
		}
		time.Sleep(200 * time.Millisecond)
	}
	resp, err := http.Get("http://127.0.0.1:18092/api/local/modules")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("回环免钥匙应 200, got %d", resp.StatusCode)
	}
	var mods []api.LocalModule
	json.NewDecoder(resp.Body).Decode(&mods)
	if len(mods) != 1 || mods[0].LastSeq != 1 || mods[0].LastFrom != "claude" || mods[0].WaitingFor != "codex" {
		t.Fatalf("状态: %+v", mods)
	}
}
