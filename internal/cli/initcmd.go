package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hou-physics/relais/internal/guide"
)

func RunInit(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: relais init <频道名>")
	}
	channel := args[0]
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	// 验证频道存在且自己是成员（顺带确认服务器可达）
	if _, err := c.Members(channel); err != nil {
		return fmt.Errorf("无法绑定频道 %q: %w", channel, err)
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	for _, sub := range []string{"inbox", "sent", "drafts"} {
		if err := os.MkdirAll(filepath.Join(root, "relais", sub), 0o755); err != nil {
			return err
		}
	}
	conf := fmt.Sprintf("server = %q\nchannel = %q\n", cfg.Server, channel)
	if err := os.WriteFile(filepath.Join(root, "relais", "config.toml"), []byte(conf), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, "relais", "AGENT.md"),
		[]byte(guide.Text(cfg.Username, channel)), 0o644); err != nil {
		return err
	}
	if err := registerProject(channel, root); err != nil {
		fmt.Printf("警告：写入项目注册表失败（bridge 将无法自动覆盖本项目）: %v\n", err)
	}
	gitignoreNote := ensureGitignore(root)
	fmt.Printf(`已绑定频道 %q → %s
    relais/config.toml  绑定配置
    relais/AGENT.md     agent 使用说明（把它的内容贴进 CLAUDE.md / AGENTS.md，或让 agent 直接读）
    relais/inbox|sent|drafts/  消息落盘目录
    已登记到本机项目注册表（relais bridge 会自动照看此项目）
%s`, channel, cfg.Server, gitignoreNote)
	return nil
}

// ensureGitignore 在项目根的 .gitignore 里补 relais/（消息不进 git，事实源在服务器）。
func ensureGitignore(root string) string {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		if _, gitErr := os.Stat(filepath.Join(root, ".git")); gitErr == nil {
			os.WriteFile(path, []byte("relais/\n"), 0o644)
			return "  已创建 .gitignore 并写入 relais/\n"
		}
		return "  提示：若此目录是 git 仓库，请把 relais/ 加进 .gitignore\n"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "relais/" {
			return ""
		}
	}
	os.WriteFile(path, append(data, []byte("\nrelais/\n")...), 0o644)
	return "  已在 .gitignore 追加 relais/\n"
}

func RunGuide(args []string) error {
	cfg, err := loadGlobal()
	if err != nil {
		return err
	}
	channel := "<频道名>"
	if _, p, err := findProject(); err == nil {
		channel = p.Channel
	}
	fmt.Print(guide.Text(cfg.Username, channel))
	return nil
}
