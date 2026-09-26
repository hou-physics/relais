package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConclusionHelperIgnoresSubChannelPrefix 覆盖 D37 场景：一个项目目录同时承载
// 频道 duo 与子频道 duo-auth 的结论文件。字符串前缀匹配会把 duo-auth-<id>.md 也当成
// duo 的结论，且因排序 "duo-auth-..." 可能排在 "duo-<id>.md" 之后而被误判为"最新"。
func TestConclusionHelperIgnoresSubChannelPrefix(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "relais", "conclusions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	duoFile := "duo-01AAAAAAAAAAAAAAAAAAAAAAAA.md"
	authFile := "duo-auth-01BBBBBBBBBBBBBBBBBBBBBBBB.md"
	if err := os.WriteFile(filepath.Join(dir, duoFile), []byte("---\nid: x\n---\n\nduo 的结论"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, authFile), []byte("---\nid: y\n---\n\nduo-auth 的结论"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := latestConclusion(root, "duo")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != duoFile {
		t.Fatalf("应命中 duo 自己的结论文件，而非子频道 duo-auth 的: %s", p)
	}
	pAuth, err := latestConclusion(root, "duo-auth")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(pAuth) != authFile {
		t.Fatalf("子频道 duo-auth 应命中自己的结论文件: %s", pAuth)
	}
}

func TestConclusionCmdPrintsExactChannelMatch(t *testing.T) {
	_, _, root := setupCLITest(t, "hou", "duo")
	dir := filepath.Join(root, "relais", "conclusions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	duoFile := "duo-01AAAAAAAAAAAAAAAAAAAAAAAA.md"
	authFile := "duo-auth-01BBBBBBBBBBBBBBBBBBBBBBBB.md"
	if err := os.WriteFile(filepath.Join(dir, duoFile), []byte("---\nid: x\n---\n\nduo 的结论正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, authFile), []byte("---\nid: y\n---\n\nduo-auth 的结论正文"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	err := RunConclusion(nil)
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, duoFile) || !strings.Contains(out, "duo 的结论正文") {
		t.Fatalf("relais conclusion 应打印 duo 自己的结论，而非 duo-auth 的: %s", out)
	}
	if strings.Contains(out, authFile) || strings.Contains(out, "duo-auth 的结论正文") {
		t.Fatalf("不应串到子频道 duo-auth 的结论: %s", out)
	}
}
