package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAgentPrefersClaude(t *testing.T) {
	lp := func(name string) (string, error) {
		switch name {
		case "claude":
			return "/usr/local/bin/claude", nil
		case "codex":
			return "/usr/local/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	agent, path := detectAgent(lp)
	if agent != "claude" || path != "/usr/local/bin/claude" {
		t.Fatalf("应优先 claude: %q %q", agent, path)
	}
	// 都没有 → assisted（空）
	none := func(string) (string, error) { return "", errors.New("nf") }
	if a, _ := detectAgent(none); a != "" {
		t.Fatalf("无 agent 应返回空: %q", a)
	}
}

func TestAgentCommandContainsHeadlessFlag(t *testing.T) {
	if !strings.Contains(agentCommand("claude"), "-p") {
		t.Fatal("claude 命令应含 -p 无头标志")
	}
	if !strings.Contains(agentCommand("codex"), "exec") {
		t.Fatal("codex 命令应含 exec")
	}
}

func TestWriteHookHasGuardrails(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	info := SetupInfo{OS: "darwin", Agent: "claude", AgentPath: "/bin/claude", Mode: "auto"}
	hp, err := writeHook(info)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hp)
	h := string(data)
	// hook 必须：先请求 turn；能取引导；能触发 needs-human；能 send
	for _, want := range []string{"auto-turn", "guidance-pull", "needs-human", "relais send", "NEEDS_HUMAN"} {
		if !strings.Contains(h, want) {
			t.Fatalf("hook 缺少 %q", want)
		}
	}
	// hook 里应有 agent 与 relais 的绝对路径（消除 PATH 问题）
	if !strings.Contains(h, "/bin/claude") {
		t.Fatal("hook 应写死 agent 绝对路径")
	}
	if filepath.Ext(hp) != ".sh" {
		t.Fatalf("unix 应生成 .sh: %s", hp)
	}
}
