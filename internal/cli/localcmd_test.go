package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/local"
)

func projectWithModule(t *testing.T) (string, string) {
	t.Helper()
	proj := t.TempDir()
	if err := local.EnsureMailDir(proj, "m"); err != nil {
		t.Fatal(err)
	}
	return proj, local.MailDir(proj, "m")
}

func TestRunPostFromSubdirDetectsSide(t *testing.T) {
	proj, md := projectWithModule(t)
	sub := filepath.Join(proj, "src")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "r.md"), []byte("回信\n"), 0o644)
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	os.Chdir(sub)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CODEX_HOME", "")
	var out bytes.Buffer
	if err := RunPost([]string{"m", "r.md"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "来自 claude") || !strings.Contains(out.String(), "relais wait m") {
		t.Fatalf("输出: %s", out.String())
	}
	items, _ := local.ScanOutbox(md)
	if len(items) != 1 || items[0].From != "claude" {
		t.Fatalf("outbox: %+v", items)
	}
	t.Setenv("CLAUDECODE", "")
	if err := RunPost([]string{"m", "r.md"}, &out); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("判断不出侧应提示 --as: %v", err)
	}
	out.Reset()
	if err := RunPost([]string{"m", "r.md", "--as", "codex", "--resolved", "--owner", "codex"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "relais wait") {
		t.Fatal("codex 侧不提示 wait")
	}
	if err := RunPost([]string{"nope", "r.md", "--as", "codex"}, &out); err == nil || !strings.Contains(err.Error(), "m") {
		t.Fatalf("模块不存在应列出已有模块: %v", err)
	}
}

func TestRunWaitPrintsDescriptions(t *testing.T) {
	proj, md := projectWithModule(t)
	os.WriteFile(filepath.Join(md, "001-codex.md"), local.RenderLetter(local.Letter{Seq: 1, From: "codex", Kind: "letter"}, "x"), 0o644)
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	os.Chdir(proj)
	var out bytes.Buffer
	if err := RunWait([]string{"m", "--as", "claude"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "第 1 封 来自 codex") || !strings.Contains(out.String(), "001-codex.md") || !strings.Contains(out.String(), "处理完后再在后台运行：relais wait m（否则收不到下一封）") {
		t.Fatalf("输出: %s", out.String())
	}
	out.Reset()
	if err := RunWait([]string{"m", "--as", "claude", "--timeout", "50ms"}, &out); err != nil {
		t.Fatalf("超时不算错误: %v", err)
	}
	if !strings.Contains(out.String(), "没等到新信") {
		t.Fatalf("超时提示: %s", out.String())
	}
}

func TestRunAttach(t *testing.T) {
	proj, md := projectWithModule(t)
	t.Setenv("CODEX_HOME", fakeCodexHomeFor(t, proj))
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	os.Chdir(proj)
	var out bytes.Buffer
	if err := RunAttach([]string{"m", "--as", "codex"}, &out); err != nil {
		t.Fatal(err)
	}
	a, ok := local.ReadAttach(md)
	if !ok || a.ThreadID != "t-new" || !strings.Contains(out.String(), "巡天主对话") {
		t.Fatalf("attach: %+v %s", a, out.String())
	}
	out.Reset()
	if err := RunAttach([]string{"m", "--as", "codex", "--thread", "t-old"}, &out); err != nil {
		t.Fatal(err)
	}
	if a, _ := local.ReadAttach(md); a.ThreadID != "t-old" {
		t.Fatal("--thread 按 id")
	}
	if err := RunAttach([]string{"m", "--as", "codex", "--thread", "没有"}, &out); err == nil {
		t.Fatal("找不到应报错")
	}
	out.Reset()
	if err := RunAttach([]string{"m", "--as", "claude"}, &out); err != nil || !strings.Contains(out.String(), "relais wait m") {
		t.Fatalf("claude 侧只提示 wait: %v %s", err, out.String())
	}
}
