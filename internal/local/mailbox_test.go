package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureMailDirAndFind(t *testing.T) {
	proj := t.TempDir()
	if err := EnsureMailDir(proj, "黑客松"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"outbox", "drafts"} {
		if st, err := os.Stat(filepath.Join(proj, "relais", "mail", "黑客松", sub)); err != nil || !st.IsDir() {
			t.Fatalf("缺 %s", sub)
		}
	}
	deep := filepath.Join(proj, "src", "pkg")
	os.MkdirAll(deep, 0o755)
	got, err := FindModuleDir(deep, "黑客松")
	if err != nil || got != proj {
		t.Fatalf("向上找模块目录失败: %q %v", got, err)
	}
	_, err = FindModuleDir(deep, "不存在")
	if err == nil || !contains(err.Error(), "黑客松") {
		t.Fatalf("找不到时应列出已有模块: %v", err)
	}
	mods, _ := ListModulesIn(proj)
	if len(mods) != 1 || mods[0] != "黑客松" {
		t.Fatalf("ListModulesIn: %v", mods)
	}
}

func TestListLettersOrdersAndSkipsOthers(t *testing.T) {
	proj := t.TempDir()
	EnsureMailDir(proj, "m")
	md := MailDir(proj, "m")
	write := func(name string, l Letter) {
		os.WriteFile(filepath.Join(md, name), RenderLetter(l, "x"), 0o644)
	}
	write("002-codex.md", Letter{Seq: 2, From: "codex", Kind: "letter"})
	write("001-hou.md", Letter{Seq: 1, From: "hou", Kind: "letter"})
	write("003-claude.md", Letter{Seq: 3, From: "claude", Kind: "conclusion", Owner: "claude"})
	write("kickoff-003.md", Letter{Seq: 3, From: "relais", Kind: "kickoff", Owner: "claude"})
	write("conclusion-003.md", Letter{Seq: 3, From: "relais", Kind: "conclusion"})
	os.WriteFile(filepath.Join(md, "drafts", "x.md"), []byte("草稿"), 0o644)
	os.WriteFile(filepath.Join(md, "outbox", "claude-01.md"), []byte("---\nfrom: claude\n---\n\n待发"), 0o644)
	ls, err := ListLetters(md)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range ls {
		names = append(names, filepath.Base(l.Path))
	}
	want := []string{"001-hou.md", "002-codex.md", "003-claude.md", "kickoff-003.md"}
	if len(names) != len(want) {
		t.Fatalf("got %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v want %v", names, want)
		}
	}
}

func TestValidModuleName(t *testing.T) {
	for _, ok := range []string{"黑客松", "m1", "a b"} {
		if !ValidModuleName(ok) {
			t.Fatalf("%q 应合法", ok)
		}
	}
	for _, bad := range []string{"", " x", "x ", "a/b", "..", "a..b"} {
		if ValidModuleName(bad) {
			t.Fatalf("%q 应非法", bad)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
