package cli

import (
	"strings"
	"testing"
)

func TestLocalPromptContents(t *testing.T) {
	p := localPrompt("claude", "grammar", "/x/relais/inbox/1.md", "不碰 8000 端口", "优先速度", true)
	for _, want := range []string{
		"claude", "grammar", "/x/relais/inbox/1.md",
		"不碰 8000 端口",                  // RULES.md 原文
		"优先速度",                        // guidance
		"relais/inbox", "relais/sent", // 首次补读历史
		"RESOLVED:", "NEEDS_HUMAN:", "owner:", "ack_of:", "owner_reason:",
		"不得新增条款", "不替对方改代码", "不写密钥", "工程附录",
		"summary:",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("提示词缺 %q", want)
		}
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", false), "relais/sent") {
		t.Fatal("续会话不应要求补读历史")
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", false), "雇主引导") {
		t.Fatal("无 guidance 时不应出现引导段")
	}
}
