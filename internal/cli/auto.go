package cli

import (
	"flag"
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
	c, _, err := newHumanClient()
	if err != nil {
		return err
	}
	switch args[0] {
	case "on":
		fs := flag.NewFlagSet("auto on", flag.ContinueOnError)
		cap := fs.Int("cap", 6, "自动来回多少条消息后停成检查点")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := c.AutoConfig(proj.Channel, true, *cap); err != nil {
			return err
		}
		fmt.Printf("已开启频道 %q 的自主对话（每 %d 条自动消息后停下等你确认）。\n请确保 relais bridge 在运行（relais setup --service 可装成后台常驻）。\n", proj.Channel, *cap)
		return nil
	case "off":
		if err := c.AutoConfig(proj.Channel, false, 0); err != nil {
			return err
		}
		fmt.Printf("已关闭频道 %q 的自主对话（回到手动模式）。\n", proj.Channel)
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
