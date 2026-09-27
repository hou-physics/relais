package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolTextIsTransportOnly(t *testing.T) {
	s := ProtocolText()
	if !strings.HasPrefix(s, ProtocolMarker) {
		t.Fatal("应以标记开头")
	}
	for _, must := range []string{"relais wait", "relais attach", "relais post", "--resolved --owner", "--ack", "--needs-human", "relais/mail/", "conclusion-", "outbox", "先回", "CLAUDECODE", "不管回不回信", "否则收不到下一封"} {
		if !strings.Contains(s, must) {
			t.Fatalf("协议缺 %q", must)
		}
	}
	for _, forbidden := range []string{"背景", "观点", "问题清单", "工程附录", "http://", "https://"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("协议不该含内容层/外链词 %q", forbidden)
		}
	}
	for i, h := range []string{"## 1", "## 2", "## 3", "## 4", "## 5", "## 6", "## 7", "## 8"} {
		if !strings.Contains(s, h) {
			t.Fatalf("应有第 %d 段", i+1)
		}
	}
}

func TestWriteProtocolRespectsCustom(t *testing.T) {
	proj := t.TempDir()
	if err := WriteProtocol(proj); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(proj, "relais", "PROTOCOL.md")
	data, _ := os.ReadFile(p)
	if string(data) != ProtocolText() {
		t.Fatal("首次写入应等于全文")
	}
	if err := WriteProtocol(proj); err != nil {
		t.Fatal("带标记的可重复覆盖")
	}
	os.WriteFile(p, []byte("# 我自己的协议\n"), 0o644)
	err := WriteProtocol(proj)
	if !errors.Is(err, ErrProtocolCustom) {
		t.Fatalf("手改过的不覆盖: %v", err)
	}
	data, _ = os.ReadFile(p)
	if string(data) != "# 我自己的协议\n" {
		t.Fatal("手改内容被动了")
	}
}

func TestEnsurePointerReplacesOldBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "CLAUDE.md")
	old := "# 项目\n\n原有内容\n\n<!-- relais-local -->\n## Relais 本地模式\n旧的三行说明\n<!-- /relais-local -->\n"
	os.WriteFile(p, []byte(old), 0o644)
	if err := EnsurePointer(p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if strings.Contains(s, "旧的三行说明") || strings.Count(s, PointerMarker) != 1 || !strings.Contains(s, "原有内容") || !strings.Contains(s, "relais/PROTOCOL.md") {
		t.Fatalf("旧块应被替换、原内容保留: %q", s)
	}
	before := s
	EnsurePointer(p)
	data, _ = os.ReadFile(p)
	if string(data) != before {
		t.Fatal("幂等")
	}
	p2 := filepath.Join(dir, "AGENTS.md")
	if err := EnsurePointer(p2); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(p2)
	if !strings.HasPrefix(string(data), PointerMarker) {
		t.Fatal("新文件应只含指针块")
	}
}
