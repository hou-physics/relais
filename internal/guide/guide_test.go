package guide

import (
	"strings"
	"testing"
)

func TestTextContainsEssentials(t *testing.T) {
	txt := Text("wu", "deutschapp")
	for _, want := range []string{
		"wu", "deutschapp", "relais inbox", "relais pull", "relais send",
		"--summary", "先向", "403",
	} {
		if !strings.Contains(txt, want) {
			t.Fatalf("说明缺少关键内容 %q", want)
		}
	}
}
