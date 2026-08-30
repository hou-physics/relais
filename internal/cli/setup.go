package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/BurntSushi/toml"
)

type SetupInfo struct {
	OS        string `toml:"os"`
	Agent     string `toml:"agent"`
	AgentPath string `toml:"agent_path"`
	Mode      string `toml:"mode"`
	HookPath  string `toml:"hook_path"`
}

// detectAgent 依次找 claude/codex/kimi（优先级即此顺序）。
func detectAgent(lookPath func(string) (string, error)) (string, string) {
	for _, name := range []string{"claude", "codex", "kimi"} {
		if p, err := lookPath(name); err == nil && p != "" {
			return name, p
		}
	}
	return "", ""
}

// agentCommand 返回无头调用模板；%PROMPT% 由 hook 用 shell 变量替换。
func agentCommand(agent string) string {
	switch agent {
	case "claude":
		return `"$AGENT" -p "$PROMPT"`
	case "codex":
		return `"$AGENT" exec "$PROMPT"`
	case "kimi":
		return `"$AGENT" -p "$PROMPT"`
	default:
		return `"$AGENT" -p "$PROMPT"`
	}
}

func hooksDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hooks"), nil
}

func writeHook(info SetupInfo) (string, error) {
	hd, err := hooksDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(hd, 0o755); err != nil {
		return "", err
	}
	relais, _ := os.Executable()
	if relais == "" {
		relais = "relais"
	}
	prompt := `读取文件 $RELAIS_MSG_PATH 里对方发来的消息，理解后写一条简短回复。` +
		`如果这件事需要你的雇主（人）来定夺、或你需要事实澄清，不要回复，请只输出一行：NEEDS_HUMAN: <你要问人的问题>，别的都不要输出。` +
		`否则请只输出一个 Relais 消息文件，严格格式（不要解释、不要代码围栏）：\n---\nsummary: <一句话摘要>\n---\n<正文一两句>。` +
		`如果下面这段"雇主引导"非空，请优先遵循它：`
	// unix hook
	script := "#!/bin/sh\n" +
		"AGENT=\"" + info.AgentPath + "\"\n" +
		"RELAIS=\"" + relais + "\"\n" +
		"export PATH=\"" + filepath.Dir(info.AgentPath) + ":" + filepath.Dir(relais) + ":$PATH\"\n" +
		"cd \"$RELAIS_MSG_DIR\" || exit 1\n" +
		"# 1) 先向服务器请求发言权（服务器托管的安全闸门）；被拒就不自动回复\n" +
		"\"" + relais + "\" auto-turn || { echo \"auto: 已暂停/到检查点/需人处理，本条不自动回复\"; exit 0; }\n" +
		"# 2) 取雇主的私有引导（若有）\n" +
		"GUIDANCE=\"$(\"" + relais + "\" guidance-pull)\"\n" +
		"# 3) 驱动本地 agent 起草\n" +
		"PROMPT=\"" + prompt + " $GUIDANCE\"\n" +
		"OUT=\"$(mktemp)\"\n" +
		agentCommand(info.Agent) + " > \"$OUT\" 2>/dev/null\n" +
		"# 4) agent 若要求人处理 → 触发 needs-human；否则 relais send\n" +
		"if head -1 \"$OUT\" | grep -q '^NEEDS_HUMAN:'; then\n" +
		"  Q=\"$(head -1 \"$OUT\" | sed 's/^NEEDS_HUMAN: *//')\"\n" +
		"  \"" + relais + "\" needs-human \"$Q\"\n" +
		"elif head -1 \"$OUT\" | grep -q '^---'; then\n" +
		"  \"" + relais + "\" send \"$OUT\"\n" +
		"else\n" +
		"  echo \"auto: agent 输出格式不符，跳过本条\"\n" +
		"fi\n" +
		"rm -f \"$OUT\"\n"
	name := "auto-reply.sh"
	if info.OS == "windows" {
		name = "auto-reply.cmd" // Windows 版：M5 生成占位（bridge 用 cmd /C 调），实现见下
	}
	hp := filepath.Join(hd, name)
	if err := os.WriteFile(hp, []byte(script), 0o755); err != nil {
		return "", err
	}
	return hp, nil
}

func saveSetup(info SetupInfo) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "setup.toml"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(info)
}

func RunSetup(args []string) error {
	info := SetupInfo{OS: runtime.GOOS}
	info.Agent, info.AgentPath = detectAgent(exec.LookPath)
	if info.Agent == "" {
		info.Mode = "assisted"
		fmt.Println("没侦测到命令行 AI（claude/codex/kimi）。")
		fmt.Println("→ 你将以「网页半自动」模式使用：在网页收发、用 ChatGPT 生成消息粘贴。")
		fmt.Println("  若想全自动：装一个命令行 agent（如 Codex CLI）后重跑 relais setup。")
	} else {
		info.Mode = "auto"
		hp, err := writeHook(info)
		if err != nil {
			return err
		}
		info.HookPath = hp
		fmt.Printf("侦测到 %s（%s）→ 自动模式。\n已生成自动回复脚本：%s\n", info.Agent, info.AgentPath, hp)
	}
	if err := saveSetup(info); err != nil {
		return err
	}
	fmt.Println("下一步：relais doctor 自检；relais setup --service 装成开机常驻；或在项目里 relais auto on 开启自主对话。")
	return nil
}
