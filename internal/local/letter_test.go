package local

import (
	"strings"
	"testing"
	"time"
)

func TestRenderParseLetterRoundTrip(t *testing.T) {
	l := Letter{ID: "01X", Module: "黑客松", Seq: 7, From: "codex", Date: time.Date(2026, 9, 27, 21, 10, 3, 0, time.UTC),
		Kind: "resolved", ReplyTo: 6, Owner: "codex", AckOf: 5, Summary: "同意收敛"}
	data := RenderLetter(l, "# 同意收敛\n\n正文 `不动`\n")
	got, body, err := ParseLetter(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 7 || got.From != "codex" || got.Kind != "resolved" || got.AckOf != 5 || got.Owner != "codex" || got.ReplyTo != 6 || got.Module != "黑客松" {
		t.Fatalf("信封往返不一致: %+v", got)
	}
	if body != "# 同意收敛\n\n正文 `不动`\n" {
		t.Fatalf("正文被改动: %q", body)
	}
	if !strings.HasPrefix(string(data), "---\n") || !strings.Contains(string(data), "\n---\n\n# 同意收敛") {
		t.Fatalf("frontmatter 形状不对: %s", data)
	}
}

func TestLetterNames(t *testing.T) {
	if LetterName(7, "codex") != "007-codex.md" || LetterName(1000, "hou") != "1000-hou.md" {
		t.Fatal("LetterName 格式错")
	}
	if ConclusionName(6) != "conclusion-006.md" || KickoffName(6) != "kickoff-006.md" {
		t.Fatal("结论/开工文件名错")
	}
	seq, from, ok := ParseLetterName("012-claude.md")
	if !ok || seq != 12 || from != "claude" {
		t.Fatalf("ParseLetterName: %d %s %v", seq, from, ok)
	}
	for _, bad := range []string{"conclusion-006.md", "kickoff-006.md", "draft.md", "007-codex.txt", "outbox"} {
		if _, _, ok := ParseLetterName(bad); ok {
			t.Fatalf("%q 不该被当成归档信", bad)
		}
	}
}

func TestSummarize(t *testing.T) {
	if Summarize("## 标题 \n\n正文") != "标题" {
		t.Fatal("应去掉 # 与空白")
	}
	long := strings.Repeat("字", 100)
	if got := Summarize(long); len([]rune(got)) != 80 {
		t.Fatalf("应截到 80 字, got %d", len([]rune(got)))
	}
	if Summarize("\n\n  第三行才有字\n") != "第三行才有字" {
		t.Fatal("应跳过空行")
	}
}
