package cli

import (
	"flag"
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
	hp := filepath.Join(hd, "auto-reply.sh")
	if err := os.WriteFile(hp, []byte(script), 0o755); err != nil {
		return "", err
	}
	return hp, nil
}

func writeHookWindows(info SetupInfo) (string, error) {
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
	agentCmd := "\"" + info.AgentPath + "\" -p \"%PROMPT%\""
	if info.Agent == "codex" {
		agentCmd = "\"" + info.AgentPath + "\" exec \"%PROMPT%\""
	}
	// cmd 批处理：请求 turn → 取引导 → 跑 agent → 三分支（NEEDS_HUMAN/格式校验/跳过）
	// 安全：GUIDANCE（雇主自由文本）与 Q（agent 输出）在拼进命令行前先剥离双引号，
	// 否则一个 " 就会截断 -p "%PROMPT%" / needs-human "!Q!" 的引号参数（伙伴在 Windows）。
	// 延迟展开(!VAR!)在解析后替换，值里的 & | < > 不会被当分隔符，故此惯用法安全。
	script := "@echo off\r\n" +
		"setlocal enabledelayedexpansion\r\n" +
		"cd /d \"%RELAIS_MSG_DIR%\" || exit /b 1\r\n" +
		"\"" + relais + "\" auto-turn || (echo auto: 已暂停/检查点/需人处理 & exit /b 0)\r\n" +
		"for /f \"delims=\" %%g in ('\"" + relais + "\" guidance-pull') do set \"GUIDANCE=%%g\"\r\n" +
		"set GUIDANCE=!GUIDANCE:\"=!\r\n" +
		"set \"PROMPT=读取 %RELAIS_MSG_PATH% 的消息并简短回复；若需人定夺只输出一行 NEEDS_HUMAN: 问题；否则只输出以 --- 开头的消息文件（--- summary --- 正文）。雇主引导：!GUIDANCE!\"\r\n" +
		"set \"OUT=%TEMP%\\relais-reply-%RANDOM%.md\"\r\n" +
		agentCmd + " > \"%OUT%\"\r\n" +
		"set \"FIRST=\"\r\n" +
		"set /p FIRST=<\"%OUT%\"\r\n" +
		// Q 的计算与去引号放在 if 块外（顶层行）：块内一个未配对的 " 会让 cmd
		// 把括号块的引号开合状态算错、找不到匹配的 )，破坏整块解析。顶层行则逐行独立解析，安全。
		"set \"Q=!FIRST:NEEDS_HUMAN: =!\"\r\n" +
		"set Q=!Q:\"=!\r\n" +
		"echo(!FIRST!| findstr /b /c:\"NEEDS_HUMAN:\" >nul\r\n" +
		"if !errorlevel!==0 (\r\n" +
		"  \"" + relais + "\" needs-human \"!Q!\"\r\n" +
		") else (\r\n" +
		"  echo(!FIRST!| findstr /b /c:\"---\" >nul\r\n" +
		"  if !errorlevel!==0 (\r\n" +
		"    \"" + relais + "\" send \"%OUT%\"\r\n" +
		"  ) else (\r\n" +
		"    echo auto: agent 输出格式不符，跳过本条\r\n" +
		"  )\r\n" +
		")\r\n" +
		"endlocal\r\n"
	hp := filepath.Join(hd, "auto-reply.cmd")
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
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	service := fs.Bool("service", false, "装成开机启动后台服务")
	fs.Parse(args)

	// 如果指定了 --service，直接安装服务
	if *service {
		msg, err := installService()
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	}

	info := SetupInfo{OS: runtime.GOOS}
	info.Agent, info.AgentPath = detectAgent(exec.LookPath)
	if info.Agent == "" {
		info.Mode = "assisted"
		fmt.Println("没侦测到命令行 AI（claude/codex/kimi）。")
		fmt.Println("→ 你将以「网页半自动」模式使用：在网页收发、用 ChatGPT 生成消息粘贴。")
		fmt.Println("  若想全自动：装一个命令行 agent（如 Codex CLI）后重跑 relais setup。")
	} else {
		info.Mode = "auto"
		var hp string
		var err error
		if info.OS == "windows" {
			hp, err = writeHookWindows(info)
		} else {
			hp, err = writeHook(info)
		}
		if err != nil {
			return err
		}
		info.HookPath = hp
		fmt.Printf("侦测到 %s（%s）→ 自动模式。\n已生成自动回复脚本：%s\n", info.Agent, info.AgentPath, hp)
	}
	if err := saveSetup(info); err != nil {
		return err
	}
	fmt.Println("下一步：relais doctor 自检；relais setup --service 装成开机常驻（让 bridge 一直在后台跑）。")
	fmt.Println("开启自主对话请到网页：进频道 → 顶部状态条设回合上限 → 点「开启自主对话」（开关只在网页，命令行的 agent 钥匙不能开）。")
	return nil
}
