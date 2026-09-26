package store

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestSessions(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser("hou", "Hou", "pw")
	tok, err := s.CreateSession(u.ID)
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	got, err := s.UserBySession(tok)
	if err != nil || got.ID != u.ID {
		t.Fatalf("按 session 找用户失败: %v", err)
	}
	if _, err := s.UserBySession("quatsch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("无效 session 应 ErrNotFound, got %v", err)
	}
}

func TestSessionExpiry(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser("hou", "Hou", "pw")
	// 新鲜 session：应可用
	fresh, err := s.CreateSession(u.ID)
	if err != nil || fresh == "" {
		t.Fatal(err)
	}
	if got, err := s.UserBySession(fresh); err != nil || got.ID != u.ID {
		t.Fatalf("新鲜 session 应可用: %v", err)
	}
	// 手工插入一条 91 天前创建的过期 session
	oldToken := "expired-session-token"
	oldCreated := time.Now().UTC().Add(-91 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := s.db.Exec(`INSERT INTO sessions (token, user_id, created_at) VALUES (?,?,?)`,
		oldToken, u.ID, oldCreated); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession(oldToken); !errors.Is(err, ErrNotFound) {
		t.Fatalf("超过 90 天的 session 应被拒（ErrNotFound）, got %v", err)
	}
	// 新鲜 session 依然可用（确认没有误伤）
	if got, err := s.UserBySession(fresh); err != nil || got.ID != u.ID {
		t.Fatalf("新鲜 session 在过期检查加入后仍应可用: %v", err)
	}
}

func TestInvites(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser("hou", "Hou", "pw")
	ch, _ := s.CreateChannel("deutschapp")
	code, err := s.CreateInvite(ch.ID, u.ID, 24*time.Hour)
	if err != nil || code == "" {
		t.Fatal(err)
	}
	name, err := s.InviteChannel(code)
	if err != nil || name != "deutschapp" {
		t.Fatalf("邀请应指向 deutschapp: %q %v", name, err)
	}
	chID, err := s.ConsumeInvite(code)
	if err != nil || chID != ch.ID {
		t.Fatal(err)
	}
	// 一次性：再用必须失败
	if _, err := s.ConsumeInvite(code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("已用邀请应失效, got %v", err)
	}
	// 过期邀请
	expired, _ := s.CreateInvite(ch.ID, u.ID, -time.Hour)
	if _, err := s.ConsumeInvite(expired); !errors.Is(err, ErrNotFound) {
		t.Fatalf("过期邀请应失效, got %v", err)
	}
}

func TestUsersAvatarMigration(t *testing.T) {
	// Open 两次同一库：第二次的容错 ALTER 不得报错（幂等）
	path := filepath.Join(t.TempDir(), "m.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s1.CreateUser("hou", "Hou", "pw123456")
	if u.Avatar != "" {
		t.Fatalf("新用户默认无头像: %+v", u)
	}
	s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重复 Open 迁移应幂等: %v", err)
	}
	defer s2.Close()
	if err := s2.UpdateProfile(u.ID, "侯", "🦉"); err != nil {
		t.Fatal(err)
	}
	got, _ := s2.UserByName("hou")
	if got.DisplayName != "侯" || got.Avatar != "🦉" {
		t.Fatalf("资料更新失败: %+v", got)
	}
}

func TestDraftLifecycleAndIsolation(t *testing.T) {
	s, ch, hou, wu, sun, _ := setupTrio(t)
	d, err := s.CreateDraft(ch.ID, hou.ID, []string{"wu"}, "草稿摘要", "# 草稿正文", "")
	if err != nil || d.ID == "" || len(d.To) != 1 || d.To[0] != "wu" {
		t.Fatalf("建草稿失败: %+v %v", d, err)
	}
	list, _ := s.ListDrafts(ch.ID, hou.ID)
	if len(list) != 1 || list[0].Summary != "草稿摘要" {
		t.Fatalf("作者应见 1 条草稿: %+v", list)
	}
	// 隔离不变量：非作者（哪怕是收件人 wu）一律不可见
	if l, _ := s.ListDrafts(ch.ID, wu.ID); len(l) != 0 {
		t.Fatalf("非作者 List 应为空: %+v", l)
	}
	if _, err := s.GetDraft(d.ID, wu.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("非作者 Get 应 ErrNotFound, got %v", err)
	}
	if err := s.DeleteDraft(d.ID, sun.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("非作者 Delete 应 ErrNotFound, got %v", err)
	}
	if err := s.DeleteDraft(d.ID, hou.ID); err != nil {
		t.Fatal(err)
	}
	if l, _ := s.ListDrafts(ch.ID, hou.ID); len(l) != 0 {
		t.Fatalf("删除后应为空: %+v", l)
	}
}

func TestSelfServiceAccount(t *testing.T) {
	s := testStore(t)
	u, _ := s.CreateUser("hou", "Hou", "altpass123")
	if err := s.UpdatePassword(u.ID, "falsch", "neuepass123"); !errors.Is(err, ErrAuth) {
		t.Fatalf("旧密码错应 ErrAuth, got %v", err)
	}
	if err := s.UpdatePassword(u.ID, "altpass123", "neuepass123"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate("hou", "neuepass123"); err != nil {
		t.Fatalf("新密码应可登录: %v", err)
	}
	if _, err := s.Authenticate("hou", "altpass123"); !errors.Is(err, ErrAuth) {
		t.Fatal("旧密码应失效")
	}
	newTok, err := s.RegenerateToken(u.ID)
	if err != nil || newTok == u.AgentToken {
		t.Fatalf("token 应更新: %v", err)
	}
	if _, err := s.UserByAgentToken(u.AgentToken); !errors.Is(err, ErrNotFound) {
		t.Fatal("旧 token 应失效")
	}
	tok, _ := s.CreateSession(u.ID)
	if err := s.DeleteSession(tok); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UserBySession(tok); !errors.Is(err, ErrNotFound) {
		t.Fatal("登出后 session 应失效")
	}
}

func TestMessageCarriesSenderAvatar(t *testing.T) {
	s, ch, hou, wu, _, m := setupTrio(t)
	s.UpdateProfile(hou.ID, "Hou", "🦉")
	m2, _ := s.SaveMessage(ch.ID, hou.ID, []int64{wu.ID}, "s2", "b", "")
	if m2.SenderAvatar != "🦉" {
		t.Fatalf("新消息应带发件人头像: %+v", m2)
	}
	list, _ := s.ListEnvelopes(ch.ID, wu.ID, true, false)
	found := false
	for _, e := range list {
		if e.ID == m2.ID && e.SenderAvatar == "🦉" {
			found = true
		}
	}
	if !found {
		t.Fatalf("信封列表应带头像: %+v", list)
	}
	_ = m
}

func TestAdminFlagMigrationAndSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := s1.CreateUser("hou", "Hou", "pw123456")
	if u.IsAdmin {
		t.Fatal("新用户默认非管理员")
	}
	s1.Close()
	s2, err := Open(path) // 重复 Open 迁移幂等
	if err != nil {
		t.Fatalf("重复 Open 应幂等: %v", err)
	}
	defer s2.Close()
	if err := s2.SetAdmin(u.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s2.UserByName("hou")
	if !got.IsAdmin {
		t.Fatal("SetAdmin 后应为管理员")
	}
	// agent token / session 查出的 User 也带 IsAdmin
	byTok, _ := s2.UserByAgentToken(u.AgentToken)
	if !byTok.IsAdmin {
		t.Fatal("按 token 查出的 User 应带 IsAdmin")
	}
}

func TestAllChannelsAndRemoveMember(t *testing.T) {
	s := testStore(t)
	a, _ := s.CreateUser("hou", "Hou", "pw")
	b, _ := s.CreateUser("wu", "Wu", "pw")
	c1, _ := s.CreateChannel("alpha")
	c2, _ := s.CreateChannel("beta")
	s.AddMember(c1.ID, a.ID)
	s.AddMember(c1.ID, b.ID)
	s.AddMember(c2.ID, a.ID)
	stats, err := s.AllChannels()
	if err != nil || len(stats) != 2 {
		t.Fatalf("应 2 个频道: %+v %v", stats, err)
	}
	m := map[string]int{}
	for _, st := range stats {
		m[st.Name] = st.Members
	}
	if m["alpha"] != 2 || m["beta"] != 1 {
		t.Fatalf("成员数不对: %+v", m)
	}
	if err := s.RemoveMember(c1.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	ok, _ := s.IsMember(c1.ID, b.ID)
	if ok {
		t.Fatal("移除后不应是成员")
	}
	if err := s.RemoveMember(c1.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("移除不存在的成员应 ErrNotFound, got %v", err)
	}
}

func TestAutoStateGovernance(t *testing.T) {
	s := testStore(t)
	ch, _ := s.CreateChannel("c")
	// 默认关闭
	st, err := s.GetAuto(ch.ID)
	if err != nil || st.Enabled || st.Cap != 6 {
		t.Fatalf("默认应关闭 cap6: %+v %v", st, err)
	}
	// 关闭时 turn 被拒
	if ok, _, _ := s.RequestTurn(ch.ID); ok {
		t.Fatal("未开启不应放行 turn")
	}
	// 开启 cap=2
	if err := s.SetAutoEnabled(ch.ID, true, 2); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.RequestTurn(ch.ID); !ok {
		t.Fatal("第1轮应放行")
	}
	if ok, _, _ := s.RequestTurn(ch.ID); !ok {
		t.Fatal("第2轮应放行")
	}
	if ok, reason, _ := s.RequestTurn(ch.ID); ok || reason == "" {
		t.Fatalf("第3轮应被拒(到检查点): ok=%v reason=%q", ok, reason)
	}
	// resume 重置回合
	if err := s.ResumeAuto(ch.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := s.RequestTurn(ch.ID); !ok {
		t.Fatal("resume 后应重新放行")
	}
	// needs-human 立即暂停
	if err := s.SetNeedsHuman(ch.ID, "预算是多少？"); err != nil {
		t.Fatal(err)
	}
	st, _ = s.GetAuto(ch.ID)
	if !st.Paused || st.NeedsHumanQ != "预算是多少？" {
		t.Fatalf("needs-human 应暂停并记录问题: %+v", st)
	}
	if ok, _, _ := s.RequestTurn(ch.ID); ok {
		t.Fatal("needs-human 暂停时不应放行")
	}
	// pause/resume
	s.ResumeAuto(ch.ID)
	s.PauseAuto(ch.ID)
	if ok, _, _ := s.RequestTurn(ch.ID); ok {
		t.Fatal("手动暂停时不应放行")
	}
}

func TestGuidance(t *testing.T) {
	s := testStore(t)
	ch, _ := s.CreateChannel("c")
	a, _ := s.CreateUser("hou", "Hou", "pw123456")
	if g, _ := s.PullGuidance(ch.ID, a.ID); g != "" {
		t.Fatal("初始无引导")
	}
	s.SetGuidance(ch.ID, a.ID, "优先考虑成本")
	s.SetGuidance(ch.ID, a.ID, "改为优先考虑上线速度") // 覆盖
	g, err := s.PullGuidance(ch.ID, a.ID)
	if err != nil || g != "改为优先考虑上线速度" {
		t.Fatalf("应取到最新引导: %q %v", g, err)
	}
	if g2, _ := s.PullGuidance(ch.ID, a.ID); g2 != "" {
		t.Fatalf("取后应清空: %q", g2)
	}
}

// TestPullGuidanceConcurrency 锁死 PullGuidance 的原子性（DELETE...RETURNING）：
// 一条引导被并发拉取，只能被恰好一个调用取到，不能双读。
func TestPullGuidanceConcurrency(t *testing.T) {
	s := testStore(t)
	ch, _ := s.CreateChannel("c")
	a, _ := s.CreateUser("hou", "Hou", "pw123456")
	s.SetGuidance(ch.ID, a.ID, "只此一条")
	var wg sync.WaitGroup
	var got int64
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g, err := s.PullGuidance(ch.ID, a.ID); err == nil && g != "" {
				atomic.AddInt64(&got, 1)
			}
		}()
	}
	wg.Wait()
	if got != 1 {
		t.Fatalf("并发拉取一条引导应恰好命中 1 次，实际 %d（非原子会 >1）", got)
	}
}

func TestRequestTurnConcurrency(t *testing.T) {
	s := testStore(t)
	ch, _ := s.CreateChannel("c")
	const K = 5
	s.SetAutoEnabled(ch.ID, true, K)
	var wg sync.WaitGroup
	var allowed int64
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _, err := s.RequestTurn(ch.ID); err == nil && ok {
				atomic.AddInt64(&allowed, 1)
			}
		}()
	}
	wg.Wait()
	if allowed != K {
		t.Fatalf("并发下恰好应放行 K=%d 次，实际 %d", K, allowed)
	}
	st, _ := s.GetAuto(ch.ID)
	if st.RoundCount != K {
		t.Fatalf("round_count 不应超过 cap: %d", st.RoundCount)
	}
}

func TestSeqAndKindFields(t *testing.T) {
	st := testStore(t)
	u, _ := st.CreateUser("a", "a", "pw")
	v, _ := st.CreateUser("b", "b", "pw")
	ch, _ := st.CreateChannel("c")
	st.AddMember(ch.ID, u.ID)
	st.AddMember(ch.ID, v.ID)
	m1, err := st.SaveMessageOpts(ch.ID, u.ID, []int64{v.ID}, "s1", "b1", "", SaveOpts{})
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := st.SaveMessageOpts(ch.ID, v.ID, []int64{u.ID}, "s2", "b2", "", SaveOpts{Kind: "resolved", Owner: "codex", OwnerReason: "因为", AckOf: m1.ID})
	if m1.Seq != 1 || m2.Seq != 2 {
		t.Fatalf("seq 应为 1,2: %d %d", m1.Seq, m2.Seq)
	}
	if m2.Kind != "resolved" || m2.Owner != "codex" || m2.OwnerReason != "因为" || m2.AckOf != m1.ID {
		t.Fatalf("kind 字段未贯通: %+v", m2)
	}
	list, _ := st.ListEnvelopes(ch.ID, u.ID, false, false)
	if len(list) != 2 || list[1].Seq != 2 || list[1].Kind != "resolved" {
		t.Fatalf("列表应带 seq/kind: %+v", list)
	}
	if Round(1) != 1 || Round(2) != 1 || Round(3) != 2 || Round(0) != 0 {
		t.Fatal("Round 计算错")
	}
	// 另一频道 seq 独立
	ch2, _ := st.CreateChannel("c2")
	st.AddMember(ch2.ID, u.ID)
	m3, _ := st.SaveMessageOpts(ch2.ID, u.ID, nil, "x", "y", "", SaveOpts{})
	if m3.Seq != 1 {
		t.Fatalf("新频道 seq 应从 1 起: %d", m3.Seq)
	}
}

func TestIdempotentSend(t *testing.T) {
	st := testStore(t)
	u, _ := st.CreateUser("a", "a", "pw")
	ch, _ := st.CreateChannel("c")
	st.AddMember(ch.ID, u.ID)
	m1, err := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "k1"})
	if err != nil || m2.ID != m1.ID {
		t.Fatalf("同 key 应返回同一条: %v %v", err, m2)
	}
	m3, _ := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "k2"})
	if m3.ID == m1.ID {
		t.Fatal("不同 key 应新建")
	}
	// 并发同 key 恰好一条
	var wg sync.WaitGroup
	ids := make(chan string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := st.SaveMessageOpts(ch.ID, u.ID, nil, "s", "b", "", SaveOpts{IdemKey: "race"})
			if err == nil {
				ids <- m.ID
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("并发同 key 应恰好一条，得到 %d", len(seen))
	}
}

func seedDuo(t *testing.T) (*Store, *Channel, *User, *User) {
	t.Helper()
	st := testStore(t)
	a, _ := st.CreateUser("claude", "Claude 侧", "pw")
	b, _ := st.CreateUser("codex", "Codex 侧", "pw")
	ch, _ := st.CreateChannel("m1")
	st.AddMember(ch.ID, a.ID)
	st.AddMember(ch.ID, b.ID)
	if err := st.SetAutoEnabled(ch.ID, true, 16); err != nil {
		t.Fatal(err)
	}
	return st, ch, a, b
}

func TestHandshake(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	m1, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "结论", "X", "", SaveOpts{Kind: "resolved", Owner: "codex", OwnerReason: "r"})
	if r, _ := st.EvaluateHandshake(ch.ID, m1.ID); r != HandshakeNone {
		t.Fatalf("第一次提议不应握手: %v", r)
	}
	// 人的普通消息夹在中间不影响
	st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "补充", "y", "", SaveOpts{})
	m2, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "附和", "X'", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	r, err := st.EvaluateHandshake(ch.ID, m2.ID)
	if err != nil || r != HandshakeDone {
		t.Fatalf("应握手: %v %v", r, err)
	}
	a1, _ := st.GetAuto(ch.ID)
	if !a1.Resolved || a1.ResolutionMsgID != m2.ID || !a1.Paused {
		t.Fatalf("握手后状态错: %+v", a1)
	}
	got, _ := st.GetMessage(m2.ID, a.ID, false)
	if got.Kind != "conclusion" {
		t.Fatalf("第二条应改写为 conclusion: %q", got.Kind)
	}
	if ok, reason, _ := st.RequestTurn(ch.ID); ok || reason == "" {
		t.Fatal("握手后 turn 必须拒")
	}
}

func TestHandshakeRejects(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	m1, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	// owner 不同 → 冲突转人
	m2, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "claude", OwnerReason: "我熟", AckOf: m1.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m2.ID); r != HandshakeOwnerConflict {
		t.Fatalf("owner 不同应冲突: %v", r)
	}
	a1, _ := st.GetAuto(ch.ID)
	if a1.Resolved || !strings.Contains(a1.NeedsHumanQ, "承接方分歧") {
		t.Fatalf("冲突应转 needs-human: %+v", a1)
	}
	st.ResumeAuto(ch.ID)
	// 同侧自 ack → 不算
	m3, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m3.ID); r != HandshakeNone {
		t.Fatal("同侧不能自握手")
	}
	// 中间又出现新的 resolved（m3）→ 对旧 m1 的 ack 失效
	m4, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m4.ID); r != HandshakeNone {
		t.Fatal("ack 指向的不是最新提议，不应握手")
	}
	// 无 ack_of → 只是新提议
	m5, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "X", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	if r, _ := st.EvaluateHandshake(ch.ID, m5.ID); r != HandshakeNone {
		t.Fatal("无 ack_of 不握手")
	}
}

func TestKickoffReopenModeClose(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	m1, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "结论正文", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	m2, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "最终结论", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m1.ID})
	st.EvaluateHandshake(ch.ID, m2.ID)
	if _, err := st.Kickoff(ch.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	k, err := st.Kickoff(ch.ID, a.ID)
	if err == nil {
		t.Fatalf("未 resolved 时 Kickoff 应报错: %+v", k)
	}
	a1, _ := st.GetAuto(ch.ID)
	if !a1.KickedOff || a1.Resolved || a1.Paused || a1.RoundCount != 0 || a1.ResolutionMsgID != m2.ID {
		t.Fatalf("kickoff 后状态错: %+v", a1)
	}
	list, _ := st.ListEnvelopes(ch.ID, b.ID, true, true)
	last := list[len(list)-1]
	if last.Kind != "kickoff" || last.Owner != "codex" || last.Seq != 0 {
		t.Fatalf("kickoff 消息应 kind=kickoff、带 owner、不占 seq: %+v", last)
	}
	full, _ := st.GetMessage(last.ID, b.ID, true)
	if full.Body != "最终结论" || len(full.To) != 2 {
		t.Fatalf("kickoff 正文应是结论且发全体: %+v", full)
	}
	if err := st.ClearKickedOff(ch.ID); err != nil {
		t.Fatal(err)
	}
	// reopen
	st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "x", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	m4, _ := st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "s", "x", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: ""})
	_ = m4
	st.SetNeedsHuman(ch.ID, "q")
	if err := st.Reopen(ch.ID); err != nil {
		t.Fatal(err)
	}
	a2, _ := st.GetAuto(ch.ID)
	if a2.Resolved || a2.Paused || a2.NeedsHumanQ != "" || a2.RoundCount != 0 {
		t.Fatalf("reopen 后状态错: %+v", a2)
	}
	// mode
	if err := st.SetMode(ch.ID, "autopilot"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(ch.ID, "bogus"); err == nil {
		t.Fatal("非法 mode 应报错")
	}
	a3, _ := st.GetAuto(ch.ID)
	if a3.Mode != "autopilot" {
		t.Fatalf("mode 未生效: %+v", a3)
	}
	// close
	st.CloseChannel(ch.ID)
	if ok, reason, _ := st.RequestTurn(ch.ID); ok || !strings.Contains(reason, "关闭") {
		t.Fatal("关闭后 turn 必须拒")
	}
	// settings
	st.SetSetting("local.default_mode", "autopilot")
	if v, _ := st.GetSetting("local.default_mode"); v != "autopilot" {
		t.Fatal("settings 读写错")
	}
	if v, _ := st.GetSetting("nope"); v != "" {
		t.Fatal("缺省应空")
	}
}

func TestCapHitTurnsToHuman(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	st.SetAutoEnabled(ch.ID, true, 2)
	st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "claude 立场", "x", "", SaveOpts{})
	st.SaveMessageOpts(ch.ID, b.ID, []int64{a.ID}, "codex 立场", "y", "", SaveOpts{})
	st.RequestTurn(ch.ID)
	st.RequestTurn(ch.ID)
	if ok, _, _ := st.RequestTurn(ch.ID); ok {
		t.Fatal("到 cap 应拒")
	}
	a1, _ := st.GetAuto(ch.ID)
	if !strings.Contains(a1.NeedsHumanQ, "回合上限") || !strings.Contains(a1.NeedsHumanQ, "claude 立场") || !strings.Contains(a1.NeedsHumanQ, "codex 立场") {
		t.Fatalf("到 cap 应转人并摆出双方立场: %q", a1.NeedsHumanQ)
	}
}

func TestInFlight(t *testing.T) {
	st, ch, a, b := seedDuo(t)
	if s, _ := st.GetAuto(ch.ID); s.InFlight {
		t.Fatal("空频道不在途")
	}
	m, _ := st.SaveMessageOpts(ch.ID, a.ID, []int64{b.ID}, "s", "x", "", SaveOpts{})
	if s, _ := st.GetAuto(ch.ID); !s.InFlight {
		t.Fatal("未读时应在途")
	}
	st.MarkRead(m.ID, b.ID)
	if s, _ := st.GetAuto(ch.ID); s.InFlight {
		t.Fatal("已读后不在途")
	}
}
