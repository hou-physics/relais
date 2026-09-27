package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupMail(t *testing.T) (string, string) {
	t.Helper()
	proj := t.TempDir()
	if err := EnsureMailDir(proj, "m"); err != nil {
		t.Fatal(err)
	}
	return proj, MailDir(proj, "m")
}

func TestPostWritesOutboxWithHeader(t *testing.T) {
	_, md := setupMail(t)
	draft := filepath.Join(md, "drafts", "a.md")
	os.WriteFile(draft, []byte("# 我的看法\n\n正文\n"), 0o644)
	out, err := Post(md, "claude", draft, PostOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != filepath.Join(md, "outbox") || !strings.HasPrefix(filepath.Base(out), "claude-") {
		t.Fatalf("outbox 路径不对: %s", out)
	}
	data, _ := os.ReadFile(out)
	l, body, err := ParseLetter(data)
	if err != nil || l.From != "claude" || l.Kind != "letter" || l.Summary != "我的看法" || body != "# 我的看法\n\n正文\n" {
		t.Fatalf("outbox 头/正文: %+v %q %v", l, body, err)
	}
	if _, err := os.Stat(draft); err != nil {
		t.Fatal("原文件不能动")
	}
	items, errs := ScanOutbox(md)
	if len(errs) != 0 || len(items) != 1 || items[0].Key != strings.TrimSuffix(filepath.Base(out), ".md") || items[0].Body != body || items[0].From != "claude" {
		t.Fatalf("ScanOutbox: %+v %v", items, errs)
	}
}

func TestPostFlags(t *testing.T) {
	_, md := setupMail(t)
	f := filepath.Join(md, "drafts", "a.md")
	os.WriteFile(f, []byte("提议收敛\n"), 0o644)
	if _, err := Post(md, "claude", f, PostOpts{Resolved: true}); err == nil {
		t.Fatal("--resolved 无 --owner 应报错")
	}
	if _, err := Post(md, "claude", f, PostOpts{Resolved: true, Owner: "kimi"}); err == nil {
		t.Fatal("owner 只能 claude|codex|user")
	}
	if _, err := Post(md, "claude", f, PostOpts{Ack: true, Resolved: true, Owner: "claude"}); err == nil {
		t.Fatal("--ack 与 --resolved 互斥")
	}
	if _, err := Post(md, "claude", f, PostOpts{Ack: true}); err == nil || !strings.Contains(err.Error(), "resolved") {
		t.Fatalf("对方没有 resolved 信时 --ack 应报错: %v", err)
	}
	// 对方的 resolved 信到了
	os.WriteFile(filepath.Join(md, "005-codex.md"), RenderLetter(Letter{Seq: 5, From: "codex", Kind: "resolved", Owner: "codex"}, "x"), 0o644)
	os.WriteFile(filepath.Join(md, "006-codex.md"), RenderLetter(Letter{Seq: 6, From: "codex", Kind: "letter"}, "x"), 0o644)
	out, err := Post(md, "claude", f, PostOpts{Ack: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	l, _, _ := ParseLetter(data)
	if l.Kind != "resolved" || l.AckOf != 5 || l.Owner != "codex" {
		t.Fatalf("--ack 应 kind=resolved ack_of=5 owner=对方提名: %+v", l)
	}
	out, _ = Post(md, "claude", f, PostOpts{NeedsHuman: true})
	data, _ = os.ReadFile(out)
	l, _, _ = ParseLetter(data)
	if l.Kind != "needs-human" || l.Summary != "提议收敛" {
		t.Fatalf("--needs-human: %+v", l)
	}
	out, _ = Post(md, "codex", f, PostOpts{Resolved: true, Owner: "user"})
	data, _ = os.ReadFile(out)
	l, _, _ = ParseLetter(data)
	if l.Kind != "resolved" || l.Owner != "user" || l.From != "codex" {
		t.Fatalf("--resolved --owner user: %+v", l)
	}
	empty := filepath.Join(md, "drafts", "empty.md")
	os.WriteFile(empty, []byte("  \n"), 0o644)
	if _, err := Post(md, "claude", empty, PostOpts{}); err == nil {
		t.Fatal("空文件应报错")
	}
	if _, err := Post(md, "kimi", f, PostOpts{}); err == nil {
		t.Fatal("侧只能 claude|codex")
	}
}

func TestScanOutboxReportsBadFiles(t *testing.T) {
	_, md := setupMail(t)
	os.WriteFile(filepath.Join(md, "outbox", "claude-bad.md"), []byte("没有 frontmatter"), 0o644)
	os.WriteFile(filepath.Join(md, "outbox", "codex-02.md"), []byte("---\nfrom: codex\nkind: letter\nsummary: s\n---\n\n正文"), 0o644)
	items, errs := ScanOutbox(md)
	if len(items) != 1 || items[0].From != "codex" || len(errs) != 1 || !strings.Contains(errs[0].Error(), "claude-bad.md") {
		t.Fatalf("items=%+v errs=%v", items, errs)
	}
}
