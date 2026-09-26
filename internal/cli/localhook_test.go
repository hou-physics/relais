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

// assertClaudeArgv 校验桩 claude 收到的完整参数：prompt 必须紧跟在 -p 后面
// （不能被 --allowedTools 的变长解析吞掉），--allowedTools 后必须恰好是
// "Read,Grep,Glob" 且它不是最后一个参数被 prompt 顶替（即其后要么没有参数，
// 要么是下一个 flag，绝不能是裸词 prompt）。
func assertClaudeArgv(t *testing.T, argsFile, wantPrompt string) {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("读取 args 失败: %v", err)
	}
	args := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	pIdx := -1
	atIdx := -1
	for i, a := range args {
		switch a {
		case "-p":
			pIdx = i
		case "--allowedTools":
			atIdx = i
		}
	}
	if pIdx == -1 || pIdx+1 >= len(args) || args[pIdx+1] != wantPrompt {
		t.Fatalf("-p 后应紧跟 prompt %q，实际参数: %v", wantPrompt, args)
	}
	if atIdx == -1 || atIdx+1 >= len(args) || args[atIdx+1] != "Read,Grep,Glob" {
		t.Fatalf("--allowedTools 后应恰好是 Read,Grep,Glob，实际参数: %v", args)
	}
	if atIdx+2 < len(args) && !strings.HasPrefix(args[atIdx+2], "-") {
		t.Fatalf("--allowedTools 的值后不应紧跟裸词（prompt 不应排在其后）: %v", args)
	}
}

// 用桩 relais（记录调用参数）+ 桩 agent（按 FAKE_OUT 输出）真的跑一遍 hook，验证五分支与会话登记。
func TestLocalHookBranchesExecute(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	argsLog := filepath.Join(dir, "args.log")
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\ncase \"$1\" in\n  session) [ \"$2\" = get ] && printf '%s' \"${FAKE_SID:-}\";;\n  local-prompt) printf 'P';;\nesac\nexit 0\n"), 0o755)
	stubAgent := filepath.Join(dir, "claude")
	os.WriteFile(stubAgent, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_ARGS\"\ncat \"$FAKE_OUT\"\n"), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: stubAgent, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	run := func(agentOut, sid string) string {
		os.Remove(log)
		os.Remove(argsLog)
		out := filepath.Join(dir, "out.txt")
		os.WriteFile(out, []byte(agentOut), 0o644)
		cmd := exec.Command("sh", hp)
		cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+proj, "RELAIS_MSG_PATH="+out,
			"RELAIS_MSG_ID=01X", "RELAIS_CHANNEL=m1", "FAKE_OUT="+out, "FAKE_SID="+sid, "FAKE_ARGS="+argsLog)
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
	assertClaudeArgv(t, argsLog, "P") // 首次唤醒：agentFirst 那行的参数顺序
	// 续会话：不带 --first；NEEDS_HUMAN 分支
	got = run("NEEDS_HUMAN: 预算多少\n", "sid-1")
	if strings.Contains(got, "--first") || !strings.Contains(got, "needs-human 预算多少") || strings.Contains(got, "send ") {
		t.Fatalf("NEEDS_HUMAN 分支错:\n%s", got)
	}
	assertClaudeArgv(t, argsLog, "P") // 续会话：agentResume 那行的参数顺序
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

// TestLocalHookResumeFailureRetries：桩 claude 在参数里出现 --resume 时非零退出，
// 验证 hook 会清 session、以 --first 重新生成提示词、换新 session id 后重试成功并发送。
func TestLocalHookResumeFailureRetries(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\ncase \"$1\" in\n  session) [ \"$2\" = get ] && printf '%s' \"${FAKE_SID:-}\";;\n  local-prompt) printf 'P';;\nesac\nexit 0\n"), 0o755)
	stubAgent := filepath.Join(dir, "claude")
	os.WriteFile(stubAgent, []byte("#!/bin/sh\nfor a in \"$@\"; do\n  if [ \"$a\" = \"--resume\" ]; then exit 1; fi\ndone\ncat \"$FAKE_OUT\"\n"), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: stubAgent, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	os.WriteFile(out, []byte("---\nsummary: 回\n---\n正文"), 0o644)
	cmd := exec.Command("sh", hp)
	cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+proj, "RELAIS_MSG_PATH="+out,
		"RELAIS_MSG_ID=01X", "RELAIS_CHANNEL=m1", "FAKE_OUT="+out, "FAKE_SID=sid-old")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook 失败: %v %s", err, b)
	}
	data, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	idxClear, idxFirst, idxSet := -1, -1, -1
	var newID string
	for i, l := range lines {
		switch {
		case l == "session clear":
			idxClear = i
		case l == "local-prompt --first":
			idxFirst = i
		case strings.HasPrefix(l, "session set "):
			idxSet = i
			newID = strings.TrimPrefix(l, "session set ")
		}
	}
	if idxClear == -1 || idxFirst == -1 || idxSet == -1 {
		t.Fatalf("缺少关键调用: clear=%d first=%d set=%d\n日志:\n%s", idxClear, idxFirst, idxSet, string(data))
	}
	if !(idxClear < idxFirst && idxFirst < idxSet) {
		t.Fatalf("顺序应为 session clear < local-prompt --first < session set，实际:\n%s", string(data))
	}
	if newID == "" || newID == "sid-old" {
		t.Fatalf("重来后应换一个不同于旧会话的新 id，得到 %q", newID)
	}
	if !strings.Contains(string(data), "send --idempotency-key ") {
		t.Fatalf("续会话重试成功后仍应发送:\n%s", string(data))
	}
}

// TestLocalHookCodexFirstWake：桩 codex 首次唤醒（无 FAKE_SID）时把事件流写到 stdout
// （含 thread_id），把最终回复写进 -o 指定的文件，验证 hook 能从事件流里取到 thread_id
// 并登记为 session，同时正常发送。
func TestLocalHookCodexFirstWake(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\ncase \"$1\" in\n  session) [ \"$2\" = get ] && printf '%s' \"${FAKE_SID:-}\";;\n  local-prompt) printf 'P';;\nesac\nexit 0\n"), 0o755)
	stubAgent := filepath.Join(dir, "codex")
	os.WriteFile(stubAgent, []byte(`#!/bin/sh
prev=""
outfile=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then outfile="$a"; fi
  prev="$a"
done
if [ -n "$outfile" ]; then cp "$FAKE_OUT" "$outfile"; fi
echo '{"type":"thread.started","thread_id":"t-1"}'
exit 0
`), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "codex", AgentPath: stubAgent, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	os.WriteFile(out, []byte("---\nsummary: 回\n---\n正文"), 0o644)
	cmd := exec.Command("sh", hp)
	cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+proj, "RELAIS_MSG_PATH="+out,
		"RELAIS_MSG_ID=01X", "RELAIS_CHANNEL=m1", "FAKE_OUT="+out, "FAKE_SID=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hook 失败: %v %s", err, b)
	}
	data, _ := os.ReadFile(log)
	got := string(data)
	if !strings.Contains(got, "session set t-1") {
		t.Fatalf("codex 首次唤醒应把事件流里的 thread_id 登记为 session: %s", got)
	}
	if !strings.Contains(got, "send --idempotency-key ") {
		t.Fatalf("codex 首次唤醒成功后应发送: %s", got)
	}
}

func TestLocalHookRejectsUnknownAgent(t *testing.T) {
	if _, err := writeLocalHook(t.TempDir(), SetupInfo{Agent: "kimi", AgentPath: "/k"}); err == nil {
		t.Fatal("本地模式只支持 claude/codex")
	}
}

// TestLocalHookCodexExitCodeNotMasked：冒烟发现——codex 首次唤醒非零退出时，
// 取 thread_id 的管道把 $? 覆盖成 0，hook 误报"输出格式不符"并吞掉 stderr。
// 退出码必须如实上报（打印 stderr），且不登记会话、不发送。
// 同时：codex 在非 git/未信任目录会直接拒跑、stdin 非终端时会读 stdin，
// 所以两条 codex 命令都要带 --skip-git-repo-check 并从 /dev/null 读 stdin。
func TestLocalHookCodexExitCodeNotMasked(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\necho \"$@\" >> \""+log+"\"\ncase \"$1\" in\n  session) [ \"$2\" = get ] && printf '%s' \"${FAKE_SID:-}\";;\n  local-prompt) printf 'P';;\nesac\nexit 0\n"), 0o755)
	stubAgent := filepath.Join(dir, "codex")
	os.WriteFile(stubAgent, []byte("#!/bin/sh\necho 'Not inside a trusted directory' >&2\nexit 1\n"), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "codex", AgentPath: stubAgent, Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(hp)
	for _, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, `"$AGENT" exec`) && (!strings.Contains(l, "--skip-git-repo-check") || !strings.Contains(l, "< /dev/null")) {
			t.Fatalf("codex 命令应带 --skip-git-repo-check 且 stdin 取 /dev/null: %s", l)
		}
	}
	cmd := exec.Command("sh", hp)
	cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+t.TempDir(),
		"RELAIS_MSG_ID=01X", "RELAIS_CHANNEL=m1", "FAKE_SID=")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook 失败: %v %s", err, b)
	}
	if !strings.Contains(string(b), "agent 退出码 1") || !strings.Contains(string(b), "trusted directory") {
		t.Fatalf("应如实上报退出码与 stderr: %s", b)
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "session set") || strings.Contains(string(data), "send ") {
		t.Fatalf("失败时不应登记会话或发送: %s", data)
	}
}

// TestLocalHookTurnRefusalEchoesReason：auto-turn 被拒时 hook 打印收到的原因，
// 原因为空时才退回通用文案（被仲裁跳过的一侧不应显示"已暂停"）。
func TestLocalHookTurnRefusalEchoesReason(t *testing.T) {
	dir := t.TempDir()
	stubRelais := filepath.Join(dir, "relais")
	os.WriteFile(stubRelais, []byte("#!/bin/sh\nif [ \"$1\" = auto-turn ]; then [ -n \"$FAKE_REASON\" ] && echo \"relais 错误: $FAKE_REASON\" >&2; exit 1; fi\nexit 0\n"), 0o755)
	hp, err := writeLocalHook(dir, SetupInfo{OS: "darwin", Agent: "claude", AgentPath: "/bin/false", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(reason string) string {
		cmd := exec.Command("sh", hp)
		cmd.Env = append(os.Environ(), "RELAIS_BIN="+stubRelais, "RELAIS_MSG_DIR="+t.TempDir(), "FAKE_REASON="+reason)
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook 失败: %v %s", err, b)
		}
		return string(b)
	}
	got := run("人的这条消息由 codex 侧接话，本侧不回")
	if !strings.Contains(got, "auto: 人的这条消息由 codex 侧接话，本侧不回") || strings.Contains(got, "已暂停") || strings.Contains(got, "relais 错误") {
		t.Fatalf("应打印收到的拒绝原因: %q", got)
	}
	if got := run(""); !strings.Contains(got, "已暂停/到上限/需人处理") {
		t.Fatalf("原因为空时应退回通用文案: %q", got)
	}
}
