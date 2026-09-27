package cli

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

func TestRegistryUpsert(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if ps, _ := loadProjects(); len(ps) != 0 {
		t.Fatalf("空注册表应为空: %+v", ps)
	}
	if err := registerProject("phineuro", "/tmp/a"); err != nil {
		t.Fatal(err)
	}
	if err := registerProject("general", "/tmp/b"); err != nil {
		t.Fatal(err)
	}
	if err := registerProject("phineuro", "/tmp/neu"); err != nil { // 覆盖
		t.Fatal(err)
	}
	ps, err := loadProjects()
	if err != nil || len(ps) != 2 {
		t.Fatalf("应 2 条: %+v %v", ps, err)
	}
	m := map[string]string{}
	for _, p := range ps {
		m[p.Channel] = p.Dir
	}
	if m["phineuro"] != "/tmp/neu" || m["general"] != "/tmp/b" {
		t.Fatalf("upsert 语义不对: %+v", m)
	}
}

func TestInitRegistersProject(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "duo") // setupCLITest 内部已跑 RunInit
	ps, err := loadProjects()
	if err != nil || len(ps) != 1 || ps[0].Channel != "duo" || ps[0].Dir != proj {
		t.Fatalf("init 应注册项目: %+v %v", ps, err)
	}
}

func TestPollOnceLandsNotifiesAndHooks(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	seedIncoming(t, st, users, "duo", 2)
	c, _, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	var notified []string
	marker := filepath.Join(t.TempDir(), "hook-ran")
	// hook 把消息路径追加进 marker 文件，验证环境变量与执行
	hook := "echo \"$RELAIS_MSG_ID $RELAIS_MSG_DIR\" >> " + marker
	landed, err := pollOnce(c, []bridgeTarget{{Channel: "duo", Dir: proj}}, hook,
		func(from, summary string) { notified = append(notified, from+":"+summary) })
	if err != nil || landed != 2 {
		t.Fatalf("应落 2 条: %d %v", landed, err)
	}
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "inbox"))
	if len(entries) != 2 {
		t.Fatalf("inbox 应 2 个文件: %v", entries)
	}
	if len(notified) != 2 || !strings.HasPrefix(notified[0], "wu:") {
		t.Fatalf("应通知 2 次: %v", notified)
	}
	data, err := os.ReadFile(marker)
	if err != nil || strings.Count(string(data), "\n") != 2 || !strings.Contains(string(data), proj) {
		t.Fatalf("hook 应执行 2 次并带 RELAIS_MSG_DIR: %q %v", data, err)
	}
	// 第二轮：无未读 → 0 落盘，不重复通知
	landed, err = pollOnce(c, []bridgeTarget{{Channel: "duo", Dir: proj}}, "", nil)
	if err != nil || landed != 0 {
		t.Fatalf("第二轮应 0: %d %v", landed, err)
	}
}

func TestNotifyCommandHasNoContentInjection(t *testing.T) {
	// Payload containing shell metacharacters that could break out of string interpolation
	from := `' $(id) `
	summary := `' "whoami" ` + "`ps`"

	cmd := notifyCmd(from, summary)
	title := "Relais · " + from

	// For darwin and windows: verify payload does NOT leak into cmd.Args
	// (command strings are never shell-interpolated, so injection is impossible)
	if runtime.GOOS != "linux" {
		for _, arg := range cmd.Args {
			if strings.Contains(arg, from) || strings.Contains(arg, summary) ||
				strings.Contains(arg, "$(id)") || strings.Contains(arg, "whoami") ||
				strings.Contains(arg, "`ps`") {
				t.Fatalf("Message content leaked into cmd.Args on %s: %v", runtime.GOOS, cmd.Args)
			}
		}
	}

	// For linux: verify exact title and summary ARE in cmd.Args (notify-send is safe)
	if runtime.GOOS == "linux" {
		foundTitle, foundSummary := false, false
		for _, arg := range cmd.Args {
			if arg == title {
				foundTitle = true
			}
			if arg == summary {
				foundSummary = true
			}
		}
		if !foundTitle || !foundSummary {
			t.Fatalf("Linux notify-send should have title and summary in Args: %v", cmd.Args)
		}
	}

	// Verify content is in env vars for all platforms (harmless for linux, needed for darwin/windows)
	foundInEnv := false
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "RELAIS_NT_SUMMARY=") || strings.HasPrefix(env, "RELAIS_NT_TITLE=") {
			foundInEnv = true
			break
		}
	}
	if !foundInEnv {
		t.Fatalf("Message content not found in cmd.Env")
	}
}

func TestFindProjectHonorsChannelEnv(t *testing.T) {
	_, _, _ = setupCLITest(t, "hou", "duo")
	_, proj, err := findProject()
	if err != nil || proj.Channel != "duo" {
		t.Fatalf("默认应为 config.toml 的频道: %+v %v", proj, err)
	}
	t.Setenv("RELAIS_CHANNEL", "trio")
	_, proj, _ = findProject()
	if proj.Channel != "trio" {
		t.Fatalf("RELAIS_CHANNEL 应覆盖: %+v", proj)
	}
}

func TestRunHookPassesChannelEnv(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "env")
	runHook("echo \"$RELAIS_CHANNEL\" > "+marker, "/p", t.TempDir(), api.Message{ID: "1", Channel: "m9", From: "x"})
	data, _ := os.ReadFile(marker)
	if strings.TrimSpace(string(data)) != "m9" {
		t.Fatalf("hook 应收到 RELAIS_CHANNEL=m9: %q", data)
	}
}

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

func TestPollOnceRoutesKickoffToConclusions(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	duo, _ := st.ChannelByName("duo")
	st.SetAutoEnabled(duo.ID, true, 16)
	m1, _ := st.SaveMessageOpts(duo.ID, users["wu"].ID, []int64{users["hou"].ID}, "s", "结论草", "", store.SaveOpts{Kind: "resolved", Owner: "codex"})
	m2, _ := st.SaveMessageOpts(duo.ID, users["hou"].ID, []int64{users["wu"].ID}, "s", "结论全文", "", store.SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	st.EvaluateHandshake(duo.ID, m2.ID)
	if _, err := st.Kickoff(duo.ID, users["wu"].ID); err != nil {
		t.Fatal(err)
	}
	// hou 侧先把 m1 拉掉（它是发给 hou 的普通 resolved），只留 kickoff 待处理
	st.MarkRead(m1.ID, users["hou"].ID)
	c, _, _ := newClient()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	pr, pw, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = pw
	n, err := pollOnce(c, []bridgeTarget{{Channel: "duo", Dir: proj}}, "touch "+marker, nil)
	pw.Close()
	os.Stdout = oldOut
	printed, _ := io.ReadAll(pr)
	if err != nil || n != 1 {
		t.Fatalf("应落 1 条: %d %v", n, err)
	}
	// 多模块项目里裸 relais conclusion 会打印错的结论，提示须带频道名
	if !strings.Contains(string(printed), "relais conclusion duo") {
		t.Fatalf("开工提示应带频道名: %s", printed)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("kickoff 不应触发 hook")
	}
	// conclusion（m2 发给 wu 的那封）在 wu 侧同样不跑 hook
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	saveGlobal(&GlobalConfig{Server: c.Server, Token: users["wu"].AgentToken, Username: "wu"})
	cw, _, _ := newClient()
	wuProj := t.TempDir()
	if n, err := pollOnce(cw, []bridgeTarget{{Channel: "duo", Dir: wuProj}}, "touch "+marker, nil); err != nil || n < 1 {
		t.Fatalf("wu 应拉到 conclusion+kickoff: %d %v", n, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("conclusion 不应触发 hook")
	}
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "conclusions"))
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "duo-") {
		t.Fatalf("应落到 conclusions/duo-<id>.md: %v", entries)
	}
	data, _ := os.ReadFile(filepath.Join(proj, "relais", "conclusions", entries[0].Name()))
	if !strings.Contains(string(data), "结论全文") || !strings.Contains(string(data), "owner: codex") || !strings.Contains(string(data), "kind: kickoff") {
		t.Fatalf("结论文件内容错: %s", data)
	}
	if inbox, _ := os.ReadDir(filepath.Join(proj, "relais", "inbox")); len(inbox) != 0 {
		t.Fatal("kickoff 不应落 inbox")
	}
	if err := RunConclusion(nil); err != nil {
		t.Fatal(err)
	}
}
