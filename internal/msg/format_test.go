package msg

import (
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	env := Envelope{
		ID: "01JG8KQ2TEST", Channel: "deutschapp", From: "wu", To: []string{"hou"},
		InReplyTo: "01JG7XOLD", Sent: time.Date(2026, 8, 29, 14, 30, 0, 0, time.UTC),
		Summary: "SRS 参数结论 + 三个待确认点",
	}
	body := "# 详细内容\n\n| 参数 | 值 |\n|---|---|\n| ease | 2.5 |\n\n---\n\n正文里的分隔线不能破坏解析。\n"
	out := Render(env, body)
	if !strings.HasPrefix(string(out), "---\n") {
		t.Fatal("应以 frontmatter 开头")
	}
	got, gotBody, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != env.ID || got.From != "wu" || len(got.To) != 1 || got.To[0] != "hou" ||
		got.Summary != env.Summary || !got.Sent.Equal(env.Sent) || got.InReplyTo != env.InReplyTo {
		t.Fatalf("信封往返失真: %+v", got)
	}
	if gotBody != body {
		t.Fatalf("正文往返失真:\n%q\nvs\n%q", gotBody, body)
	}
}

func TestParseErrors(t *testing.T) {
	if _, _, err := Parse([]byte("没有 frontmatter")); err == nil {
		t.Fatal("缺 frontmatter 应报错")
	}
	if _, _, err := Parse([]byte("---\nid: x\n没有结束线")); err == nil {
		t.Fatal("frontmatter 未闭合应报错")
	}
}
