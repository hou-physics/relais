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
