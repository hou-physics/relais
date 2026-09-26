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

func TestExtractSummary(t *testing.T) {
	in := []byte("---\nsummary: 给人看的摘要\n---\n\n# 正文\n内容")
	sum, body, ok := ExtractSummary(in)
	if !ok || sum != "给人看的摘要" || body != "# 正文\n内容" {
		t.Fatalf("提取失败: %q %q %v", sum, body, ok)
	}
	// 完整信封（pull 落盘的文件）同样适用
	env := Envelope{ID: "01X", Channel: "c", From: "hou", To: []string{"wu"},
		Sent: time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC), Summary: "S"}
	sum2, body2, ok2 := ExtractSummary(Render(env, "B"))
	if !ok2 || sum2 != "S" || body2 != "B" {
		t.Fatalf("信封文件提取失败: %q %q %v", sum2, body2, ok2)
	}
	// 无 frontmatter → ok=false，原文即正文
	sum3, body3, ok3 := ExtractSummary([]byte("纯文本"))
	if ok3 || sum3 != "" || body3 != "纯文本" {
		t.Fatalf("纯文本应原样返回: %q %q %v", sum3, body3, ok3)
	}
	// 有 frontmatter 但无 summary → ok=false，原文即正文（不静默丢头）
	raw := "---\nid: x\n---\n\n正文"
	if _, body4, ok4 := ExtractSummary([]byte(raw)); ok4 || body4 != raw {
		t.Fatalf("无 summary 应整体视为正文: %q %v", body4, ok4)
	}
}

func TestExtractHeader(t *testing.T) {
	h, body, ok := ExtractHeader([]byte("---\nsummary: 结论\nkind: resolved\nowner: codex\nowner_reason: 熟\nack_of: 01ABC\n---\n\n正文"))
	if !ok || h.Summary != "结论" || h.Kind != "resolved" || h.Owner != "codex" || h.OwnerReason != "熟" || h.AckOf != "01ABC" || body != "正文" {
		t.Fatalf("header 解析错: %+v %q %v", h, body, ok)
	}
	if _, body, ok := ExtractHeader([]byte("纯文本")); ok || body != "纯文本" {
		t.Fatal("无 frontmatter 应 ok=false 且原文返回")
	}
	env := Envelope{ID: "x", Seq: 3, Round: 2, Kind: "conclusion", Owner: "codex"}
	out := string(Render(env, "b"))
	for _, want := range []string{"seq: 3", "round: 2", "kind: conclusion", "owner: codex"} {
		if !strings.Contains(out, want) {
			t.Fatalf("Render 缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(string(Render(Envelope{ID: "y"}, "b")), "kind:") {
		t.Fatal("空字段应 omitempty")
	}
}
