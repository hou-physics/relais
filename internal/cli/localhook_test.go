package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalHookClaude(t *testing.T) {
	dir := t.TempDir()
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: "/opt/claude", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hp)
	h := string(data)
	for _, want := range []string{
		"auto-turn", "local-prompt", "session get", "session set", "session clear",
		"--session-id", "--resume", `--allowedTools "Read,Grep,Glob"`, "/opt/claude",
		"RESOLVED:", "NEEDS_HUMAN:", "--kind resolved", "--idempotency-key", "needs-human",
		"uuidgen",
	} {
		if !strings.Contains(h, want) {
			t.Fatalf("claude hook 缺 %q", want)
		}
	}
	if !strings.HasPrefix(h, "#!/bin/sh\n") {
		t.Fatal("应是 sh 脚本")
	}
	st, _ := os.Stat(hp)
	if st.Mode()&0o100 == 0 {
		t.Fatal("hook 应可执行")
	}
	// 分支优先级：RESOLVED 的判断必须出现在 NEEDS_HUMAN 之前
	if strings.Index(h, "^RESOLVED:") > strings.Index(h, "^NEEDS_HUMAN:") {
		t.Fatal("RESOLVED 分支必须先于 NEEDS_HUMAN")
	}
	if out, err := exec.Command("sh", "-n", hp).CombinedOutput(); err != nil {
		t.Fatalf("hook 语法错误: %v %s", err, out)
	}
}

func TestLocalHookCodex(t *testing.T) {
	dir := t.TempDir()
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "codex", AgentPath: "/opt/codex", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hp)
	h := string(data)
	for _, want := range []string{"exec resume", "--json", "thread_id", `sandbox_mode="read-only"`, "/opt/codex", "-o "} {
		if !strings.Contains(h, want) {
			t.Fatalf("codex hook 缺 %q", want)
		}
	}
	if strings.Contains(h, "--allowedTools") {
		t.Fatal("codex hook 不该有 claude 参数")
	}
	if out, err := exec.Command("sh", "-n", hp).CombinedOutput(); err != nil {
		t.Fatalf("hook 语法错误: %v %s", err, out)
	}
}

// 用桩 relais（记录调用参数）+ 桩 agent（按 FAKE_OUT 输出）真的跑一遍 hook，验证五分支与会话登记。
func TestLocalHookBranchesExecute(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\ncase \"$1\" in\n  session) [ \"$2\" = get ] && printf '%s' \"${FAKE_SID:-}\";;\n  local-prompt) printf 'P';;\nesac\nexit 0\n"), 0o755)
	stubAgent := filepath.Join(dir, "claude")
	os.WriteFile(stubAgent, []byte("#!/bin/sh\ncat \"$FAKE_OUT\"\n"), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: stubAgent, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	run := func(agentOut, sid string) string {
		os.Remove(log)
		out := filepath.Join(dir, "out.txt")
		os.WriteFile(out, []byte(agentOut), 0o644)
		cmd := exec.Command("sh", hp)
		cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+proj, "RELAIS_MSG_PATH="+out,
			"RELAIS_MSG_ID=01X", "RELAIS_CHANNEL=m1", "FAKE_OUT="+out, "FAKE_SID="+sid)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook 失败: %v %s", err, b)
		}
		b, _ := os.ReadFile(log)
		return string(b)
	}
	// RESOLVED 分支：首次唤醒 → local-prompt --first、session set、send --kind resolved
	got := run("RESOLVED: 谈拢了\n---\nowner: codex\n---\n正文", "")
	for _, want := range []string{"auto-turn", "local-prompt --first", "session set ", "send --kind resolved --summary 谈拢了 --idempotency-key "} {
		if !strings.Contains(got, want) {
			t.Fatalf("RESOLVED 分支缺 %q:\n%s", want, got)
		}
	}
	// 续会话：不带 --first；NEEDS_HUMAN 分支
	got = run("NEEDS_HUMAN: 预算多少\n", "sid-1")
	if strings.Contains(got, "--first") || !strings.Contains(got, "needs-human 预算多少") || strings.Contains(got, "send ") {
		t.Fatalf("NEEDS_HUMAN 分支错:\n%s", got)
	}
	// 普通回信分支
	got = run("---\nsummary: 回\n---\n正文", "sid-1")
	if !strings.Contains(got, "send --idempotency-key ") || strings.Contains(got, "--kind") {
		t.Fatalf("普通分支错:\n%s", got)
	}
	// 格式不符 → 不 send 不 needs-human
	got = run("随便说点什么", "sid-1")
	if strings.Contains(got, "send ") || strings.Contains(got, "needs-human") {
		t.Fatalf("不符分支不应发送:\n%s", got)
	}
}

func TestLocalHookRejectsUnknownAgent(t *testing.T) {
	if _, err := writeLocalHook(t.TempDir(), SetupInfo{Agent: "kimi", AgentPath: "/k"}); err == nil {
		t.Fatal("本地模式只支持 claude/codex")
	}
}
