package cli

import (
	"fmt"
	"os"

	"github.com/hou-physics/relais/internal/api"
)

func RunAuto(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais auto <on|off|status> [--cap N]")
	}
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	switch args[0] {
	case "on":
		// 开启自主对话属于人的操作，需要在网页上完成
		fmt.Printf("开启/关闭自主对话属于「人的操作」，请在网页 %s 的频道「%s」里操作。\n（命令行用的是 agent 钥匙，不能开关自主模式——这是为了防止 agent 擅自给自己开启。）\n", cfg.Server, proj.Channel)
		return nil
	case "off":
		// 关闭自主对话属于人的操作，需要在网页上完成
		fmt.Printf("开启/关闭自主对话属于「人的操作」，请在网页 %s 的频道「%s」里操作。\n（命令行用的是 agent 钥匙，不能开关自主模式——这是为了防止 agent 擅自给自己开启。）\n", cfg.Server, proj.Channel)
		return nil
	case "status":
		st, err := c.AutoGet(proj.Channel)
		if err != nil {
			return err
		}
		state := "关闭"
		if st.Enabled {
			state = fmt.Sprintf("开启（第 %d/%d 回合）", (st.RoundCount+1)/2, (st.Cap+1)/2)
		}
		if st.Paused {
			state += " · 已暂停"
		}
		if st.NeedsHumanQ != "" {
			state += " · 等你回答：" + st.NeedsHumanQ
		}
		if st.Resolved {
			state += " · 已握手待确认：" + st.ResolutionSummary + "（承接方 " + st.Owner + "）"
		}
		if st.KickedOff {
			state += " · 已开工（relais conclusion 查看）"
		}
		state += " · 模式 " + st.Mode
		fmt.Printf("频道 %q 自主状态：%s\n", proj.Channel, state)
		return nil
	default:
		return fmt.Errorf("未知 auto 子命令 %q", args[0])
	}
}

// RunAutoTurn 供 hook 调：放行返回 nil(exit0)；被拒返回错误(exit 非0) 并打印原因。
func RunAutoTurn(_ []string) error {
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	if who := humanMsgResponder(c, proj.Channel, cfg.Username); who != "" && who != cfg.Username {
		return fmt.Errorf("人的这条消息由 %s 侧接话，本侧不回", who)
	}
	tr, err := c.AutoTurn(proj.Channel)
	if err != nil {
		return err
	}
	if !tr.Allowed {
		return fmt.Errorf("%s", tr.Reason)
	}
	return nil
}

func RunNeedsHuman(args []string) error {
	if len(args) != 1 || args[0] == "" {
		return fmt.Errorf("用法: relais needs-human \"<要问人的问题>\"")
	}
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	c, _, err := newClient()
	if err != nil {
		return err
	}
	if err := c.NeedsHuman(proj.Channel, args[0]); err != nil {
		return err
	}
	fmt.Println("已标记：需要人处理，自主循环已暂停。")
	return nil
}

// RunGuidancePull 打印本 agent 主人的待读引导（无则打印空）。
func RunGuidancePull(_ []string) error {
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	c, _, err := newClient()
	if err != nil {
		return err
	}
	note, err := c.GuidancePull(proj.Channel)
	if err != nil {
		return err
	}
	fmt.Print(note)
	return nil
}

// humanMsgResponder：本地单人模式里人的消息（如 needs-human 的回答）同时发给两侧，
// 若两侧都回会交叉来信、立场互换、难收敛（冒烟发现）。只让一侧接话：
// 最近一封 agent 来信是谁写的，就由另一侧接；还没有 agent 来信时由 claude 接。
// 接话方须在这条消息的收件人里，否则收到的一侧照常接（人只发给一侧时不能让循环空转）。
// 不是本地侧、触发消息来自对侧 agent（正常轮流）、或查不到触发消息时返回空，不干预。
func humanMsgResponder(c *Client, channel, me string) string {
	from, msgID := os.Getenv("RELAIS_MSG_FROM"), os.Getenv("RELAIS_MSG_ID")
	peer := map[string]string{"claude": "codex", "codex": "claude"}[me]
	if from == "" || peer == "" || from == me || from == peer {
		return ""
	}
	msgs, err := c.Envelopes(channel, false)
	if err != nil {
		return "" // 查不到就不干预，交给服务器闸门
	}
	var trigger *api.Message
	for i := range msgs {
		if msgs[i].ID == msgID {
			trigger = &msgs[i]
		}
	}
	if trigger == nil {
		return ""
	}
	// 只看这条人的消息之前的 agent 来信
	lastSeq, last := 0, ""
	for _, m := range msgs {
		if m.Seq >= trigger.Seq || (m.From != me && m.From != peer) {
			continue
		}
		if m.Seq > lastSeq {
			lastSeq, last = m.Seq, m.From
		}
	}
	who := me
	switch last {
	case "":
		who = "claude"
	case me:
		who = peer
	}
	for _, to := range trigger.To {
		if to == who {
			return who
		}
	}
	return ""
}
