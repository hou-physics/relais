package cli

import (
	"testing"

	"github.com/hou-physics/relais/internal/store"
)

// 本地模式冒烟发现：人的消息（needs-human 回答）同时发给两侧 → 两侧 hook 同时回 →
// 交叉来信、立场互换、难收敛。auto-turn 须只放行"该接话"的那一侧：
// 最近一封 agent 来信是谁写的，就由另一侧接；还没有 agent 来信时由 claude 接。
func TestAutoTurnHumanMessageOnlyOneSideReplies(t *testing.T) {
	st, users, _ := setupCLITest(t, "hou", "duo")
	cl, _ := st.CreateUser("claude", "Claude 侧", "pw-c")
	cx, _ := st.CreateUser("codex", "Codex 侧", "pw-x")
	hou := users["hou"]
	ch, _ := st.CreateChannel("smoke")
	for _, u := range []*store.User{cl, cx, hou} {
		st.AddMember(ch.ID, u.ID)
	}
	st.SetAutoEnabled(ch.ID, true, 16)
	t.Setenv("RELAIS_CHANNEL", "smoke")
	g, _ := loadGlobal()
	as := func(u *store.User, from, msgID string) error {
		t.Helper()
		if err := saveGlobal(&GlobalConfig{Server: g.Server, Token: u.AgentToken, Username: u.Username}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("RELAIS_MSG_FROM", from)
		t.Setenv("RELAIS_MSG_ID", msgID)
		return RunAutoTurn(nil)
	}
	// 还没有 agent 来信：人开题发两侧 → claude 接，codex 不接
	h0, _ := st.SaveMessageOpts(ch.ID, hou.ID, []int64{cl.ID, cx.ID}, "人开题", "b", "", store.SaveOpts{})
	if err := as(cx, "hou", h0.ID); err == nil || err.Error() != "人的这条消息由 claude 侧接话，本侧不回" {
		t.Fatalf("无 agent 来信时 codex 不应接人的消息: %v", err)
	}
	if err := as(cl, "hou", h0.ID); err != nil {
		t.Fatalf("无 agent 来信时 claude 应接: %v", err)
	}
	// claude 开题 → codex 提 needs-human → 人回答发两侧 → 该 codex 接，claude 不接
	st.SaveMessageOpts(ch.ID, cl.ID, []int64{cx.ID}, "开题", "b", "", store.SaveOpts{})
	h1, _ := st.SaveMessageOpts(ch.ID, hou.ID, []int64{cl.ID, cx.ID}, "回答", "b", "", store.SaveOpts{})
	if err := as(cl, "hou", h1.ID); err == nil || err.Error() != "人的这条消息由 codex 侧接话，本侧不回" {
		t.Fatalf("最近 agent 来信是 claude 写的，claude 不应再接人的消息: %v", err)
	}
	if err := as(cx, "hou", h1.ID); err != nil {
		t.Fatalf("codex 应接: %v", err)
	}
	// 对侧 agent 来信照常放行（不受此规则影响）
	m2, _ := st.SaveMessageOpts(ch.ID, cx.ID, []int64{cl.ID}, "回", "b", "", store.SaveOpts{})
	if err := as(cl, "codex", m2.ID); err != nil {
		t.Fatalf("对侧来信应放行: %v", err)
	}
}

// 接话方须在人这条消息的收件人里：人只发给 codex、而最近 agent 来信也是 codex 写的时，
// 规则会选 claude，但 claude 没收到这封——codex 必须照常接，否则循环空转。
func TestAutoTurnHumanMessageSingleRecipient(t *testing.T) {
	st, users, _ := setupCLITest(t, "hou", "duo")
	cl, _ := st.CreateUser("claude", "Claude 侧", "pw-c")
	cx, _ := st.CreateUser("codex", "Codex 侧", "pw-x")
	hou := users["hou"]
	ch, _ := st.CreateChannel("smoke")
	for _, u := range []*store.User{cl, cx, hou} {
		st.AddMember(ch.ID, u.ID)
	}
	st.SetAutoEnabled(ch.ID, true, 16)
	t.Setenv("RELAIS_CHANNEL", "smoke")
	g, _ := loadGlobal()
	if err := saveGlobal(&GlobalConfig{Server: g.Server, Token: cx.AgentToken, Username: "codex"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RELAIS_MSG_FROM", "hou")
	// 无 agent 历史、人只发 codex 开题 → codex 照常接
	h0, _ := st.SaveMessageOpts(ch.ID, hou.ID, []int64{cx.ID}, "人开题", "b", "", store.SaveOpts{})
	t.Setenv("RELAIS_MSG_ID", h0.ID)
	if err := RunAutoTurn(nil); err != nil {
		t.Fatalf("只发给 codex 的开题，codex 应接: %v", err)
	}
	// 最近 agent 来信是 codex 写的，人只发 codex → codex 仍照常接
	st.SaveMessageOpts(ch.ID, cx.ID, []int64{cl.ID}, "codex 说", "b", "", store.SaveOpts{})
	h1, _ := st.SaveMessageOpts(ch.ID, hou.ID, []int64{cx.ID}, "只问 codex", "b", "", store.SaveOpts{})
	t.Setenv("RELAIS_MSG_ID", h1.ID)
	if err := RunAutoTurn(nil); err != nil {
		t.Fatalf("接话方 claude 不在收件人里，codex 应照常接: %v", err)
	}
}
