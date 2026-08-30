package cli

import (
	"fmt"
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
			state = fmt.Sprintf("开启（第 %d/%d 轮）", st.RoundCount, st.Cap)
		}
		if st.Paused {
			state += " · 已暂停"
		}
		if st.NeedsHumanQ != "" {
			state += " · 等你回答：" + st.NeedsHumanQ
		}
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
	c, _, err := newClient()
	if err != nil {
		return err
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
