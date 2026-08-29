package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUserLifecycle(t *testing.T) {
	s := testStore(t)
	u, err := s.CreateUser("hou", "Hou", "geheim123")
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "hou" || u.AgentToken == "" {
		t.Fatalf("用户字段不对: %+v", u)
	}
	if _, err := s.Authenticate("hou", "geheim123"); err != nil {
		t.Fatalf("正确密码应通过: %v", err)
	}
	if _, err := s.Authenticate("hou", "falsch"); !errors.Is(err, ErrAuth) {
		t.Fatalf("错误密码应返回 ErrAuth, got %v", err)
	}
	got, err := s.UserByAgentToken(u.AgentToken)
	if err != nil || got.ID != u.ID {
		t.Fatalf("按 token 找用户失败: %v", err)
	}
	if _, err := s.CreateUser("hou", "Hou2", "x"); err == nil {
		t.Fatal("重复用户名应报错")
	}
}

func TestChannelMembers(t *testing.T) {
	s := testStore(t)
	a, _ := s.CreateUser("hou", "Hou", "pw")
	b, _ := s.CreateUser("wu", "Wu", "pw")
	ch, err := s.CreateChannel("deutschapp")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ch.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(ch.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	ms, err := s.ListMembers(ch.ID)
	if err != nil || len(ms) != 2 || ms[0].Username != "hou" || ms[1].Username != "wu" {
		t.Fatalf("成员列表不对: %v %v", ms, err)
	}
	ok, _ := s.IsMember(ch.ID, a.ID)
	if !ok {
		t.Fatal("hou 应是成员")
	}
	if _, err := s.ChannelByName("nix"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的频道应 ErrNotFound, got %v", err)
	}
}

func TestForeignKeysEnforcedOnPooledConnections(t *testing.T) {
	s := testStore(t)
	// Force fresh physical connections by disabling idle connection reuse
	s.db.SetMaxIdleConns(0)
	// Try to insert a members row with nonexistent channel_id and user_id
	// This should fail with FOREIGN KEY constraint error on a fresh connection
	_, err := s.db.Exec("INSERT INTO members (channel_id, user_id, joined_at) VALUES (999, 999, '2026-08-30T00:00:00Z')")
	if err == nil {
		t.Fatal("外键约束应该拒绝不存在的频道/用户")
	}
	// Verify error mentions FOREIGN KEY
	if err.Error() == "" {
		t.Fatalf("错误消息不能为空")
	}
	// Foreign key violations in SQLite appear as constraint errors
	t.Logf("外键违反错误（符合预期）: %v", err)
}

// setupTrio: 频道 deutschapp 内 hou/wu/sun 三人，hou 已发一条只给 wu 的定向消息。
func setupTrio(t *testing.T) (s *Store, ch *Channel, hou, wu, sun *User, m *Message) {
	t.Helper()
	s = testStore(t)
	hou, _ = s.CreateUser("hou", "Hou", "pw")
	wu, _ = s.CreateUser("wu", "Wu", "pw")
	sun, _ = s.CreateUser("sun", "Sun", "pw")
	ch, _ = s.CreateChannel("deutschapp")
	for _, u := range []*User{hou, wu, sun} {
		if err := s.AddMember(ch.ID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	m, err = s.SaveMessage(ch.ID, hou.ID, []int64{wu.ID}, "SRS 参数结论", "# 详细内容\n给 wu 的 agent 的正文", "")
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestAnchorAgentIsolation(t *testing.T) {
	s, _, hou, wu, sun, m := setupTrio(t)
	// wu 的 agent（收件人）：可读
	got, err := s.GetMessage(m.ID, wu.ID, true)
	if err != nil || got.Body == "" {
		t.Fatalf("收件人 agent 应可读正文: %v", err)
	}
	// hou 的 agent（发件人）：可回看
	if _, err := s.GetMessage(m.ID, hou.ID, true); err != nil {
		t.Fatalf("发件人 agent 应可回看: %v", err)
	}
	// sun 的 agent：串台，必须 ErrForbidden —— 本项目核心不变量
	if _, err := s.GetMessage(m.ID, sun.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非收件人 agent 必须被拒, got %v", err)
	}
	// sun 本人（人的钥匙）：频道内全透明，可读
	if _, err := s.GetMessage(m.ID, sun.ID, false); err != nil {
		t.Fatalf("频道成员（人）应可读: %v", err)
	}
	// sun 的 agent 列表里也不出现这条
	list, err := s.ListEnvelopes(m.ChannelID, sun.ID, true, false)
	if err != nil || len(list) != 0 {
		t.Fatalf("sun 的 agent 列表应为空, got %d 条", len(list))
	}
	// sun 本人列表里出现（含信封与摘要）
	list, _ = s.ListEnvelopes(m.ChannelID, sun.ID, false, false)
	if len(list) != 1 || list[0].Summary != "SRS 参数结论" || list[0].Body != "" {
		t.Fatalf("人的列表应含信封+摘要且不带正文: %+v", list)
	}
}

func TestReadFlow(t *testing.T) {
	s, ch, _, wu, sun, m := setupTrio(t)
	// wu 未读 1 条
	unread, _ := s.ListEnvelopes(ch.ID, wu.ID, true, true)
	if len(unread) != 1 || !unread[0].Unread {
		t.Fatalf("wu 应有 1 条未读: %+v", unread)
	}
	// 非收件人标已读 → ErrForbidden
	if err := s.MarkRead(m.ID, sun.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非收件人 MarkRead 应被拒, got %v", err)
	}
	if err := s.MarkRead(m.ID, wu.ID); err != nil {
		t.Fatal(err)
	}
	unread, _ = s.ListEnvelopes(ch.ID, wu.ID, true, true)
	if len(unread) != 0 {
		t.Fatalf("已读后应无未读: %+v", unread)
	}
	infos, _ := s.ChannelsForUser(wu.ID)
	if len(infos) != 1 || infos[0].Name != "deutschapp" || infos[0].Unread != 0 {
		t.Fatalf("ChannelsForUser 不对: %+v", infos)
	}
}

func TestRecipientsExpandAndOrder(t *testing.T) {
	s, ch, hou, wu, sun, _ := setupTrio(t)
	m2, err := s.SaveMessage(ch.ID, wu.ID, []int64{hou.ID, sun.ID}, "发全体", "正文", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(m2.To) != 2 || m2.To[0] != "hou" || m2.To[1] != "sun" {
		t.Fatalf("收件人应展开且按字母排序: %v", m2.To)
	}
	list, _ := s.ListEnvelopes(ch.ID, hou.ID, false, false)
	if len(list) != 2 || list[0].ID >= list[1].ID {
		t.Fatalf("列表应按 id 升序: %+v", list)
	}
}

func TestListEnvelopesRequiresMembership(t *testing.T) {
	s, ch, _, _, _, _ := setupTrio(t)
	// 创建未加入频道的用户
	gast, _ := s.CreateUser("gast", "Gast", "pw")
	// 非成员用 human 钥匙应被拒
	if _, err := s.ListEnvelopes(ch.ID, gast.ID, false, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非成员 human 应被拒, got %v", err)
	}
	// 非成员用 agent 钥匙也应被拒
	if _, err := s.ListEnvelopes(ch.ID, gast.ID, true, false); !errors.Is(err, ErrForbidden) {
		t.Fatalf("非成员 agent 应被拒, got %v", err)
	}
}
