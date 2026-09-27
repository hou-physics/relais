package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/store"
)

func TestLocalPromptContents(t *testing.T) {
	p := localPrompt("claude", "grammar", "/x/relais/inbox/1.md", "不碰 8000 端口", "优先速度", "- 第 3 条（hou）：预算 5 万", true)
	for _, want := range []string{
		"claude", "grammar", "/x/relais/inbox/1.md",
		"不碰 8000 端口",                  // RULES.md 原文
		"优先速度",                        // guidance
		"relais/inbox", "relais/sent", // 首次补读历史
		"RESOLVED:", "NEEDS_HUMAN:", "owner:", "ack_of:", "owner_reason:",
		"不得新增条款", "不替对方改代码", "不写密钥", "工程附录",
		"summary:",
		"雇主在频道里说过的话（你上次发言之后）", "预算 5 万",
		"@ 开头",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("提示词缺 %q", want)
		}
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", "", false), "relais/sent") {
		t.Fatal("续会话不应要求补读历史")
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", "", false), "雇主引导") {
		t.Fatal("无 guidance 时不应出现引导段")
	}
	if strings.Contains(localPrompt("codex", "g", "/m", "", "", "", false), "雇主在频道里说过的话") {
		t.Fatal("无人话时不应出现该段")
	}
}

// D42 ⑦：人的回答只由一侧接话，被拒的一侧下次唤醒时须在提示词里读到它。
func TestRunLocalPromptIncludesHumanNotes(t *testing.T) {
	st, users, _ := setupCLITest(t, "hou", "duo")
	cl, _ := st.CreateUser("claude", "Claude 侧", "pw-c")
	cx, _ := st.CreateUser("codex", "Codex 侧", "pw-x")
	hou := users["hou"]
	ch, _ := st.CreateChannel("smoke")
	for _, u := range []*store.User{cl, cx, hou} {
		st.AddMember(ch.ID, u.ID)
	}
	t.Setenv("RELAIS_CHANNEL", "smoke")
	g, _ := loadGlobal()
	if err := saveGlobal(&GlobalConfig{Server: g.Server, Token: cl.AgentToken, Username: "claude"}); err != nil {
		t.Fatal(err)
	}
	st.SaveMessageOpts(ch.ID, hou.ID, []int64{cl.ID, cx.ID}, "旧话", "上次发言前说的", "", store.SaveOpts{})
	st.SaveMessageOpts(ch.ID, cl.ID, []int64{cx.ID}, "开题", "b", "", store.SaveOpts{})
	st.SaveMessageOpts(ch.ID, cx.ID, []int64{cl.ID}, "问人", "b", "", store.SaveOpts{})
	st.SaveMessageOpts(ch.ID, hou.ID, []int64{cl.ID, cx.ID}, "人的回答", "预算上限五万", "", store.SaveOpts{})
	trig, _ := st.SaveMessageOpts(ch.ID, cx.ID, []int64{cl.ID}, "codex 接着说", "对侧正文", "", store.SaveOpts{})
	t.Setenv("RELAIS_MSG_ID", trig.ID)
	t.Setenv("RELAIS_MSG_PATH", "/x/relais/inbox/t.md")
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	err := RunLocalPrompt(nil)
	w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	p := string(out)
	for _, want := range []string{"雇主在频道里说过的话（你上次发言之后）", "人的回答", "预算上限五万"} {
		if !strings.Contains(p, want) {
			t.Fatalf("提示词缺 %q:\n%s", want, p)
		}
	}
	for _, not := range []string{"上次发言前说的", "对侧正文", "codex 接着说"} {
		if strings.Contains(p, not) {
			t.Fatalf("提示词不应含 %q（本侧上次发言前的人话 / 对侧来信 / 触发信）", not)
		}
	}
}
