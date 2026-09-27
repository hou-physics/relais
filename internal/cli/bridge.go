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
		// 通过 argv 传给 AppleScript（on run argv）：不经 shell，注入安全；
		// 不用 `system attribute` 读环境变量——它按 MacRoman 解码，中文摘要会成乱码。
		cmd = exec.Command("osascript",
			"-e", "on run argv",
			"-e", "display notification (item 1 of argv) with title (item 2 of argv)",
			"-e", "end run",
			summary, title)
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
	if st, err := os.Stat(hook); err == nil && st.Mode().IsRegular() {
		// hook 是一个存在的文件（本地模式生成的 auto-reply.sh，路径含 "Application Support" 的空格）：
		// 直接 exec，不经 shell 拆词——否则 sh -c 会把路径在空格处截断（exit 127）。
		cmd = exec.Command(hook)
	} else if runtime.GOOS == "windows" {
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
		"RELAIS_CHANNEL="+m.Channel,
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
			if envMsg.Kind == "kickoff" {
				fmt.Printf("[%s] 已开工 · 承接方 %s → %s\n  在工作脑里执行：relais conclusion %s\n", tgt.Channel, envMsg.Owner, path, tgt.Channel)
				continue
			}
			if envMsg.Kind == "conclusion" {
				// 握手已成立的那封不需要回复（甩手模式下服务器已同时 kickoff，若跑 hook 讨论脑会对结论再回一封）
				fmt.Printf("[%s] 已握手：%s（承接方 %s）\n", tgt.Channel, envMsg.Summary, envMsg.Owner)
				continue
			}
			runHook(hook, path, tgt.Dir, envMsg)
		}
	}
	return landed, lastErr
}

// loadBridgeTargets：每轮重读项目登记表（新建模块无需重启 bridge，spec §4）。
func loadBridgeTargets() ([]bridgeTarget, error) {
	ps, err := loadProjects()
	if err != nil {
		return nil, err
	}
	var targets []bridgeTarget
	for _, p := range ps {
		if st, err := os.Stat(p.Dir); err == nil && st.IsDir() {
			targets = append(targets, bridgeTarget{Channel: p.Channel, Dir: p.Dir})
		}
	}
	return targets, nil
}

// bridgeStartTargets：bridge 启动时的照看目标。登记表为空时退回当前目录的项目；
// 仍没有时，显式指定了身份目录（RELAIS_CONFIG_DIR，本地模式两侧 bridge 都是）则返回空表、进循环等登记表，
// 否则报错提示 relais init——本地模式 bootstrap 后 bridge 先于任何模块启动（M8 冒烟）。
func bridgeStartTargets() ([]bridgeTarget, error) {
	targets, err := loadBridgeTargets()
	if err != nil || len(targets) > 0 {
		return targets, err
	}
	if root, proj, err := findProject(); err == nil {
		return []bridgeTarget{{Channel: proj.Channel, Dir: root}}, nil
	}
	if os.Getenv("RELAIS_CONFIG_DIR") != "" {
		return nil, nil
	}
	return nil, fmt.Errorf("没有已注册的项目：请先在项目目录里 relais init <频道名>")
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
	targets, err := bridgeStartTargets()
	if err != nil {
		return err
	}
	fmt.Printf("relais bridge 已启动，照看 %d 个项目（间隔 %d 秒，Ctrl+C 退出）：\n", len(targets), *interval)
	if len(targets) == 0 {
		fmt.Println("  （尚无模块：每轮重读登记表，等控制台新建模块）")
	}
	for _, tgt := range targets {
		fmt.Printf("  %s → %s\n", tgt.Channel, tgt.Dir)
	}
	backoff := *interval
	for {
		if fresh, err := loadBridgeTargets(); err == nil && len(fresh) > 0 {
			if len(fresh) != len(targets) {
				fmt.Printf("项目登记表已更新，现照看 %d 个项目\n", len(fresh))
			}
			targets = fresh
		}
		_, err := pollOnce(c, targets, *hook, notifyDesktop)
		c.Heartbeat()
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
