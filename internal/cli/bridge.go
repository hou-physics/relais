package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

type bridgeTarget struct {
	Channel string
	Dir     string
}

func notifyCmd(from, summary string) *exec.Cmd {
	title := "Relais · " + from
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		// AppleScript reads the env vars via `system attribute`
		cmd = exec.Command("osascript", "-e",
			`display notification (system attribute "RELAIS_NT_SUMMARY") with title (system attribute "RELAIS_NT_TITLE")`)
	case "windows":
		cmd = exec.Command("powershell", "-NoProfile", "-Command",
			`[void][System.Reflection.Assembly]::LoadWithPartialName('System.Windows.Forms');`+
				`$n=New-Object System.Windows.Forms.NotifyIcon;$n.Icon=[System.Drawing.SystemIcons]::Information;`+
				`$n.Visible=$true;$n.ShowBalloonTip(5000,$env:RELAIS_NT_TITLE,$env:RELAIS_NT_SUMMARY,[System.Windows.Forms.ToolTipIcon]::Info)`)
	default:
		// Linux: notify-send is exec'd directly (not shell-interpreted), so passing argv is injection-safe.
		// The "--" sentinel guards against title/summary that happen to start with "-" being parsed as options.
		cmd = exec.Command("notify-send", "--", title, summary)
	}
	cmd.Env = append(os.Environ(), "RELAIS_NT_TITLE="+title, "RELAIS_NT_SUMMARY="+summary)
	return cmd
}

func notifyDesktop(from, summary string) {
	cmd := notifyCmd(from, summary)
	if err := cmd.Run(); err != nil {
		fmt.Printf("（系统通知发送失败，仅终端提示）\n")
	}
}

func runHook(hook, msgPath, dir string, m api.Message) {
	if hook == "" {
		return
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", hook)
	} else {
		cmd = exec.Command("sh", "-c", hook)
	}
	cmd.Env = append(os.Environ(),
		"RELAIS_MSG_PATH="+msgPath,
		"RELAIS_MSG_DIR="+dir,
		"RELAIS_MSG_FROM="+m.From,
		"RELAIS_MSG_SUMMARY="+m.Summary,
		"RELAIS_MSG_ID="+m.ID,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("hook 执行失败: %v\n", err)
	}
}

func pollOnce(c *Client, targets []bridgeTarget, hook string, notify func(from, summary string)) (int, error) {
	landed := 0
	var lastErr error
	for _, tgt := range targets {
		unread, err := c.Envelopes(tgt.Channel, true)
		if err != nil {
			fmt.Printf("[%s] 拉取失败: %v\n", tgt.Channel, err)
			lastErr = err
			continue
		}
		for _, envMsg := range unread {
			path, err := pullOne(c, tgt.Dir, envMsg)
			if err != nil {
				fmt.Printf("[%s] 落盘失败: %v\n", tgt.Channel, err)
				lastErr = err
				continue
			}
			landed++
			fmt.Printf("[%s] 新消息 ← %s · %s\n  %s\n", tgt.Channel, envMsg.From, envMsg.Summary, path)
			if notify != nil {
				notify(envMsg.From, envMsg.Summary)
			}
			runHook(hook, path, tgt.Dir, envMsg)
		}
	}
	return landed, lastErr
}

func RunBridge(args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	interval := fs.Int("interval", 15, "轮询间隔（秒）")
	hook := fs.String("hook", "", "每条新消息落盘后执行的命令（可选）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, _, err := newClient()
	if err != nil {
		return err
	}
	var targets []bridgeTarget
	ps, err := loadProjects()
	if err != nil {
		return err
	}
	for _, p := range ps {
		if st, err := os.Stat(p.Dir); err == nil && st.IsDir() {
			targets = append(targets, bridgeTarget{Channel: p.Channel, Dir: p.Dir})
		} else {
			fmt.Printf("跳过已失效的注册项目 %s（目录 %s 不存在）\n", p.Channel, p.Dir)
		}
	}
	if len(targets) == 0 {
		root, proj, err := findProject()
		if err != nil {
			return fmt.Errorf("没有已注册的项目：请先在项目目录里 relais init <频道名>")
		}
		targets = append(targets, bridgeTarget{Channel: proj.Channel, Dir: root})
	}
	fmt.Printf("relais bridge 已启动，照看 %d 个项目（间隔 %d 秒，Ctrl+C 退出）：\n", len(targets), *interval)
	for _, tgt := range targets {
		fmt.Printf("  %s → %s\n", tgt.Channel, tgt.Dir)
	}
	backoff := *interval
	for {
		_, err := pollOnce(c, targets, *hook, notifyDesktop)
		if err != nil {
			if backoff < 300 {
				backoff *= 2
				if backoff > 300 {
					backoff = 300
				}
			}
		} else {
			backoff = *interval
		}
		time.Sleep(time.Duration(backoff) * time.Second)
	}
}
