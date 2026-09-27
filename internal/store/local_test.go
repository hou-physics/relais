package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openLocalTest(t *testing.T) (*Store, *Channel, map[string]*User) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	us := map[string]*User{}
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.CreateUser(n, n, "pw-"+n)
		us[n] = u
	}
	ch, _ := st.CreateChannel("m")
	for _, u := range us {
		st.AddMember(ch.ID, u.ID)
	}
	return st, ch, us
}

func TestLocalModuleLifecycle(t *testing.T) {
	st, ch, _ := openLocalTest(t)
	if err := st.UpsertLocalModule(ch.ID, "/p"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLocalModule(ch.ID, "/p2"); err != nil {
		t.Fatal(err)
	}
	m, err := st.LocalModuleByName("m")
	if err != nil || m.Dir != "/p2" || m.Name != "m" || m.ArchivedSeq != 0 || m.CreatedAt == "" {
		t.Fatalf("upsert 后: %+v %v", m, err)
	}
	st.SetArchivedSeq(ch.ID, 3)
	st.SetCodexAttach(ch.ID, "tid", "名", "2026-09-27T00:00:00Z")
	st.SetCodexDeliveryError(ch.ID, "boom")
	st.SetLocalClosed(ch.ID, "2026-09-27T01:00:00Z")
	ms, _ := st.LocalModules()
	if len(ms) != 1 || ms[0].ArchivedSeq != 3 || ms[0].CodexThreadID != "tid" || ms[0].CodexThreadName != "名" || ms[0].CodexDeliveryError != "boom" || ms[0].ClosedAt == "" {
		t.Fatalf("字段没存住: %+v", ms)
	}
	if err := st.RenameChannel(ch.ID, "m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LocalModuleByName("m"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("旧名应查不到: %v", err)
	}
	if m, err := st.LocalModuleByName("m2"); err != nil || m.ChannelID != ch.ID {
		t.Fatalf("新名应查到: %v", err)
	}
	st.CreateChannel("taken")
	if err := st.RenameChannel(ch.ID, "taken"); !errors.Is(err, ErrChannelExists) {
		t.Fatalf("重名应 ErrChannelExists, got %v", err)
	}
	st.ReopenChannel(ch.ID)
	if a, _ := st.GetAuto(ch.ID); a.Closed || a.Paused {
		t.Fatal("重开后不应 closed/paused")
	}
	if err := st.DeleteChannel(ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ChannelByName("m2"); err == nil {
		t.Fatal("删除后频道应不存在")
	}
	if ms, _ := st.LocalModules(); len(ms) != 0 {
		t.Fatal("删除后登记表应空")
	}
}

func TestListAfterSeqAndKickoffs(t *testing.T) {
	st, ch, us := openLocalTest(t)
	to := []int64{us["claude"].ID, us["hou"].ID}
	m1, _ := st.SaveMessage(ch.ID, us["codex"].ID, to, "一", "正文一", "")
	m2, _ := st.SaveMessageOpts(ch.ID, us["codex"].ID, to, "二", "正文二", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	ls, err := st.ListAfterSeq(ch.ID, 1)
	if err != nil || len(ls) != 1 || ls[0].ID != m2.ID || ls[0].Sender != "codex" || ls[0].Body != "正文二" || len(ls[0].To) != 2 {
		t.Fatalf("ListAfterSeq: %+v %v", ls, err)
	}
	if seq, _ := st.SeqOf(m1.ID); seq != 1 {
		t.Fatalf("SeqOf: %d", seq)
	}
	if id, _ := st.MessageIDBySeq(ch.ID, 2); id != m2.ID {
		t.Fatal("MessageIDBySeq")
	}
	// 握手 → kickoff（seq=0）
	m3, _ := st.SaveMessageOpts(ch.ID, us["claude"].ID, []int64{us["codex"].ID, us["hou"].ID}, "三", "附和", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m2.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m3.ID); r != HandshakeDone {
		t.Fatalf("应握手, got %v", r)
	}
	k, err := st.Kickoff(ch.ID, us["hou"].ID)
	if err != nil {
		t.Fatal(err)
	}
	ks, _ := st.ListKickoffs(ch.ID)
	if len(ks) != 1 || ks[0].ID != k.ID || ks[0].AckOf != m3.ID || ks[0].Owner != "codex" {
		t.Fatalf("ListKickoffs: %+v", ks)
	}
	if ls, _ := st.ListAfterSeq(ch.ID, 0); len(ls) != 3 {
		t.Fatalf("kickoff 不占 seq，ListAfterSeq(0) 应 3 封, got %d", len(ls))
	}
}

func TestDeliveriesAndLocalTurn(t *testing.T) {
	st, ch, us := openLocalTest(t)
	m, _ := st.SaveMessage(ch.ID, us["codex"].ID, []int64{us["claude"].ID}, "一", "x", "")
	st.RecordDelivery(m.ID, "codex", "error", "no attach")
	st.RecordDelivery(m.ID, "codex", "ok", "")
	if status, e, at, err := st.Delivery(m.ID, "codex"); err != nil || status != "ok" || e != "" || at == "" {
		t.Fatalf("Delivery upsert: %s %s %s %v", status, e, at, err)
	}
	if id, status, _, _, _ := st.LastDelivery(ch.ID, "codex"); id != m.ID || status != "ok" {
		t.Fatalf("LastDelivery: %s %s", id, status)
	}
	st.SetAutoEnabled(ch.ID, true, 2) // 2 条 = 1 回合
	if hit, _ := st.CountLocalTurn(ch.ID); hit {
		t.Fatal("第 1 条不应到顶")
	}
	if hit, _ := st.CountLocalTurn(ch.ID); !hit {
		t.Fatal("第 2 条应到顶")
	}
	if a, _ := st.GetAuto(ch.ID); a.NeedsHumanQ == "" || !a.Paused || a.RoundCount != 2 {
		t.Fatalf("到顶应 needs-human: %+v", a)
	}
	ch2, _ := st.CreateChannel("noauto")
	if hit, err := st.CountLocalTurn(ch2.ID); err != nil || hit {
		t.Fatalf("无 channel_auto 行也要能计数: %v %v", hit, err)
	}
}

func TestSetCapAndMaxSeq(t *testing.T) {
	st, ch, us := openLocalTest(t)
	st.SetAutoEnabled(ch.ID, true, 6)
	st.CountLocalTurn(ch.ID)
	if seq, err := st.MaxSeq(ch.ID); err != nil || seq != 0 {
		t.Fatalf("空频道 MaxSeq 应为 0: %d %v", seq, err)
	}
	if err := st.SetCap(ch.ID, 9); err != nil {
		t.Fatal(err)
	}
	a, err := st.GetAuto(ch.ID)
	if err != nil || a.Cap != 9 {
		t.Fatalf("SetCap 后 Cap 应变: %+v %v", a, err)
	}
	if a.RoundCount != 1 {
		t.Fatalf("SetCap 不应影响 RoundCount: %+v", a)
	}
	m, _ := st.SaveMessage(ch.ID, us["codex"].ID, []int64{us["claude"].ID}, "一", "x", "")
	if seq, err := st.MaxSeq(ch.ID); err != nil || seq != 1 {
		t.Fatalf("保存一条后 MaxSeq 应为 1: %d %v", seq, err)
	}
	if got, _ := st.SeqOf(m.ID); got != 1 {
		t.Fatalf("SeqOf 应与 MaxSeq 一致: %d", got)
	}
}
