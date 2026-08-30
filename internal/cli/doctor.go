package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

type check struct {
	name string
	ok   bool
	fix  string
}

func loadSetup() (SetupInfo, bool) {
	dir, err := configDir()
	if err != nil {
		return SetupInfo{}, false
	}
	var info SetupInfo
	if _, err := toml.DecodeFile(filepath.Join(dir, "setup.toml"), &info); err != nil {
		return SetupInfo{}, false
	}
	return info, true
}

func runChecks() []check {
	var out []check

	// 1) 已登录（能读全局配置）
	if _, err := loadGlobal(); err != nil {
		out = append(out, check{"已登录 Relais", false, "运行 relais login <服务器地址> --token <你的token>（邀请页上有）"})
	} else {
		out = append(out, check{"已登录 Relais", true, ""})
	}

	// 2) 能连服务器（/api/me）
	if c, _, err := newClient(); err == nil {
		if _, err := c.Me(); err == nil {
			out = append(out, check{"连接服务器", true, ""})
		} else {
			out = append(out, check{"连接服务器", false, "检查网络/域名；或 token 失效，重新 relais login"})
		}
	} else {
		out = append(out, check{"连接服务器", false, "先 relais login"})
	}

	// 3) setup 已跑 + 侦测到 agent
	info, ok := loadSetup()
	if !ok {
		out = append(out, check{"已运行 setup", false, "运行 relais setup"})
	} else if info.Mode == "assisted" {
		out = append(out, check{"本地 AI（自动模式）", false, "未侦测到命令行 AI：装 Codex CLI/Claude Code 等并登录一次，再 relais setup；不装则用网页半自动"})
	} else {
		// 4) agent 无头能应答
		if agentHeadlessOK(info) {
			out = append(out, check{"本地 AI 无头可用", true, ""})
		} else {
			out = append(out, check{"本地 AI 无头可用", false, fmt.Sprintf("先在终端运行 %s 登录一次；确认它能用", info.Agent)})
		}
		// 5) hook 存在可执行
		if st, err := os.Stat(info.HookPath); err == nil && st.Mode()&0o111 != 0 {
			out = append(out, check{"自动回复脚本就绪", true, ""})
		} else {
			out = append(out, check{"自动回复脚本就绪", false, "重新运行 relais setup 生成 hook"})
		}
	}
	return out
}

func agentHeadlessOK(info SetupInfo) bool {
	var cmd *exec.Cmd
	switch info.Agent {
	case "codex":
		cmd = exec.Command(info.AgentPath, "exec", "只回复一个词：pong")
	default:
		cmd = exec.Command(info.AgentPath, "-p", "只回复一个词：pong")
	}
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return false
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		return false
	}
}

func RunDoctor(_ []string) error {
	fmt.Println("Relais 自检：")
	allOK := true
	for _, c := range runChecks() {
		if c.ok {
			fmt.Printf("  ✅ %s\n", c.name)
		} else {
			allOK = false
			fmt.Printf("  ❌ %s —— 怎么修：%s\n", c.name, c.fix)
		}
	}
	if allOK {
		fmt.Println("全部通过，可以用了。")
	} else {
		fmt.Println("按上面的「怎么修」逐项处理后再跑一次 relais doctor。")
	}
	return nil
}
