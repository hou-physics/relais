package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftCreatesServerDraft(t *testing.T) {
	_, users, proj := setupCLITest(t, "hou", "duo")
	md := filepath.Join(proj, "d.md")
	os.WriteFile(md, []byte("---\nsummary: 草稿摘要\n---\n\n草稿正文"), 0o644)
	if err := RunDraft([]string{md}); err != nil {
		t.Fatal(err)
	}
	// 作者能列到；收件人 wu 的 agent 列不到（隔离在 server 测试已锚定，这里验证客户端链路）
	c, _, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	ds, err := c.Drafts("duo")
	if err != nil || len(ds) != 1 || ds[0].Summary != "草稿摘要" || ds[0].To[0] != "wu" {
		t.Fatalf("草稿应建立: %+v %v", ds, err)
	}
	_ = users
}

func TestDraftTrioRequiresTo(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "trio")
	md := filepath.Join(proj, "d.md")
	os.WriteFile(md, []byte("---\nsummary: s\n---\n\nx"), 0o644)
	if err := RunDraft([]string{md}); err == nil || !strings.Contains(err.Error(), "--to") {
		t.Fatalf("三人频道缺 --to 应报错: %v", err)
	}
}
