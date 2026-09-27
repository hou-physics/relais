package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, md, name string, l Letter, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(md, name), RenderLetter(l, body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewLettersSkipsOwnAndSeen(t *testing.T) {
	_, md := setupMail(t)
	put(t, md, "001-hou.md", Letter{Seq: 1, From: "hou", Kind: "letter"}, "@codex 先回\n\n题")
	put(t, md, "002-codex.md", Letter{Seq: 2, From: "codex", Kind: "letter"}, "x")
	put(t, md, "003-claude.md", Letter{Seq: 3, From: "claude", Kind: "letter"}, "x")
	ls, c, err := NewLetters(md, "claude", Cursor{})
	if err != nil || len(ls) != 2 || ls[0].Seq != 1 || ls[1].Seq != 2 || c.Seq != 3 {
		t.Fatalf("应看到 1、2，游标推到 3: %+v %+v %v", ls, c, err)
	}
	ls, c, _ = NewLetters(md, "claude", c)
	if len(ls) != 0 {
		t.Fatal("没有新信")
	}
	put(t, md, "kickoff-003.md", Letter{Seq: 3, From: "relais", Kind: "kickoff", Owner: "codex"}, "开工")
	ls, c, _ = NewLetters(md, "claude", c)
	if len(ls) != 1 || ls[0].Kind != "kickoff" || c.Kickoff != "kickoff-003.md" || c.Seq != 3 {
		t.Fatalf("kickoff 文件应被当新信: %+v %+v", ls, c)
	}
	if ls, _, _ := NewLetters(md, "claude", c); len(ls) != 0 {
		t.Fatal("同一 kickoff 不重复")
	}
}

func TestNewLettersKickoffSeqNotStringCompare(t *testing.T) {
	_, md := setupMail(t)
	put(t, md, "kickoff-1000.md", Letter{Seq: 1000, From: "relais", Kind: "kickoff", Owner: "codex"}, "开工")
	ls, c, err := NewLetters(md, "claude", Cursor{Kickoff: "kickoff-999.md"})
	if err != nil || len(ls) != 1 || c.Kickoff != "kickoff-1000.md" {
		t.Fatalf("kickoff-1000 应比 kickoff-999 新（按数值比较非字符串）: %+v %+v %v", ls, c, err)
	}
	ls, _, err = NewLetters(md, "claude", Cursor{Kickoff: "kickoff-1000.md"})
	if err != nil {
		t.Fatal(err)
	}
	put(t, md, "kickoff-999.md", Letter{Seq: 999, From: "relais", Kind: "kickoff", Owner: "codex"}, "开工")
	if ls, _, _ := NewLetters(md, "claude", Cursor{Kickoff: "kickoff-1000.md"}); len(ls) != 0 {
		t.Fatalf("游标已到 1000 时，序号 999 的 kickoff 不应被当新信: %+v", ls)
	}
}

func TestWaitReturnsOnNewLetterAndCleansMarker(t *testing.T) {
	_, md := setupMail(t)
	done := make(chan []Letter, 1)
	go func() {
		ls, _ := Wait(context.Background(), md, "claude", 20*time.Millisecond, 0, "sess-1")
		done <- ls
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if m, ok := ReadWaitMarker(md, "claude"); ok && m.SessionID == "sess-1" && m.PID == os.Getpid() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待中应有 .wait-claude 标记")
		}
		time.Sleep(10 * time.Millisecond)
	}
	put(t, md, "001-codex.md", Letter{Seq: 1, From: "codex", Kind: "letter"}, "x")
	select {
	case ls := <-done:
		if len(ls) != 1 || ls[0].Seq != 1 {
			t.Fatalf("应返回第 1 封: %+v", ls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait 没有醒")
	}
	if _, ok := ReadWaitMarker(md, "claude"); ok {
		t.Fatal("返回后标记应删除")
	}
	if c := ReadCursor(md, "claude"); c.Seq != 1 {
		t.Fatalf("游标应写 1: %+v", c)
	}
}

func TestWaitImmediateAndTimeout(t *testing.T) {
	_, md := setupMail(t)
	put(t, md, "001-codex.md", Letter{Seq: 1, From: "codex", Kind: "letter"}, "x")
	ls, err := Wait(context.Background(), md, "claude", 10*time.Millisecond, 0, "")
	if err != nil || len(ls) != 1 {
		t.Fatalf("已有未读信应立即返回: %v %v", ls, err)
	}
	_, err = Wait(context.Background(), md, "claude", 10*time.Millisecond, 50*time.Millisecond, "")
	if err != ErrWaitTimeout {
		t.Fatalf("应超时: %v", err)
	}
	if _, ok := ReadWaitMarker(md, "claude"); ok {
		t.Fatal("超时后标记应删除")
	}
}

func TestFirstResponderAndDescribe(t *testing.T) {
	h := Letter{Seq: 3, From: "hou", Kind: "letter"}
	prior := []Letter{{Seq: 1, From: "claude", Kind: "letter"}, {Seq: 2, From: "codex", Kind: "letter"}}
	if FirstResponder(h, "@codex 先回\n\n题", prior) != "codex" {
		t.Fatal("首行 @codex 先回 优先")
	}
	if FirstResponder(h, "题", prior) != "claude" {
		t.Fatal("否则是最近 agent 信的另一侧")
	}
	if FirstResponder(h, "题", nil) != "claude" {
		t.Fatal("尚无 agent 信时 claude 先回")
	}
	d := Describe(Letter{Seq: 3, From: "hou", Kind: "letter", Path: "/p/003-hou.md"}, "题", "codex", prior)
	if !strings.Contains(d, "第 3 封") || !strings.Contains(d, "来自 hou") || !strings.Contains(d, "/p/003-hou.md") || !strings.Contains(d, "由 claude 先回，本侧不用回") {
		t.Fatalf("Describe 雇主信: %q", d)
	}
	d = Describe(Letter{Seq: 4, From: "relais", Kind: "kickoff", Owner: "codex", Path: "/p/kickoff-004.md"}, "", "codex", prior)
	if !strings.Contains(d, "你是承接方") || !strings.Contains(d, "conclusion-004.md") {
		t.Fatalf("Describe kickoff 承接方: %q", d)
	}
	d = Describe(Letter{Seq: 4, From: "relais", Kind: "kickoff", Owner: "codex", Path: "/p/kickoff-004.md"}, "", "claude", prior)
	if !strings.Contains(d, "承接方是 codex，本侧不用开工") {
		t.Fatalf("Describe kickoff 非承接方: %q", d)
	}
	d = Describe(Letter{Seq: 4, From: "relais", Kind: "kickoff", Owner: "user", Path: "/p/kickoff-004.md"}, "", "claude", prior)
	if !strings.Contains(d, "承接方是雇主") {
		t.Fatalf("Describe kickoff owner=user: %q", d)
	}
	// 终审修复 minor：附和（conclusion）与 agent 的 needs-human 都不用回信
	conc := Letter{Seq: 5, From: "claude", Kind: "conclusion", Owner: "codex", Path: "/p/005-claude.md"}
	if d = Describe(conc, "", "codex", prior); !strings.Contains(d, "这是对方的附和，已握手；不用回信，等开工通知") {
		t.Fatalf("Describe conclusion: %q", d)
	}
	if d = DeliveryText("m", conc, "", prior); strings.Contains(d, "回信。") || !strings.Contains(d, "不用回信") {
		t.Fatalf("DeliveryText conclusion 不该叫它回信: %q", d)
	}
	nh := Letter{Seq: 6, From: "claude", Kind: "needs-human", Path: "/p/006-claude.md"}
	if d = Describe(nh, "", "codex", prior); !strings.Contains(d, "对方在等雇主定夺；不用回信") {
		t.Fatalf("Describe needs-human: %q", d)
	}
	if d = DeliveryText("m", nh, "", prior); strings.Contains(d, "回信。") {
		t.Fatalf("DeliveryText needs-human 不该叫它回信: %q", d)
	}
	if d = DeliveryText("m", Letter{Seq: 7, From: "claude", Kind: "letter", Path: "/p/007-claude.md"}, "", prior); !strings.Contains(d, "按 relais/PROTOCOL.md 回信。") {
		t.Fatalf("普通信仍应叫它回信: %q", d)
	}
}
