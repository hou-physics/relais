package guide

import (
	"strings"
	"testing"
)

func TestTextContainsEssentials(t *testing.T) {
	txt := Text("wu", "deutschapp")
	for _, want := range []string{
		"wu", "deutschapp", "relais inbox", "relais pull", "relais send",
		"--summary", "先向", "403", "relais draft", "relais bridge",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("说明缺少关键内容 %q", want)
		}
	}
}

func TestLocalTextTeachesTwoCommands(t *testing.T) {
	s := LocalText("claude", "grammar", "/x/sides/claude")
	for _, want := range []string{"拿去讨论", "开工", `RELAIS_CONFIG_DIR="/x/sides/claude"`, `RELAIS_CHANNEL="grammar"`, "relais send", "relais/conclusions/", "grammar-", "RULES.md", "summary:", "claude"} {
		if !strings.Contains(s, want) {
			t.Fatalf("LocalText 缺 %q", want)
		}
	}
}
