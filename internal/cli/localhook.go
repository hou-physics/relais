package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// codexIsolation 是 codex 讨论脑每次调用都带的隔离参数（spec §4.3 只读）。
// --ignore-user-config 仍用 CODEX_HOME 的登录态，只是不读 config.toml。
const codexIsolation = `--ignore-user-config -c 'sandbox_mode="read-only"' -c 'mcp_servers={}' ` +
	`--disable plugins --disable apps --disable browser_use --disable computer_use`

// writeLocalHook 生成本地模式的五分支 hook（D36 续会话 + D40 只读 + §5 五分支）。
// 与联网 hook（setup.go writeHook）分文件、互不影响。
func writeLocalHook(dir string, info SetupInfo) (string, error) {
	// agentAfterFirst 紧跟在首次运行与 RC=$? 之后执行（codex 从事件流取 thread_id）；
	// 必须排在 RC=$? 之后，否则管道的 $? 会覆盖 agent 的退出码（冒烟发现）。
	var agentFirst, agentResume, agentAfterFirst string
	switch info.Agent {
	case "claude":
		// 只读靠三件事（--allowedTools 只是"预批准"，挡不住用户 settings 里 defaultMode=auto 自动放行写工具）：
		//   --tools 只暴露 Read/Grep/Glob（其余内置工具根本不存在）；
		//   --strict-mcp-config 不加载任何用户/项目 MCP；
		//   --permission-mode default 覆盖用户 settings 的 defaultMode。
		// --tools 是变长参数（吃掉后面所有非 flag 词），所以 "$PROMPT" 必须紧跟在 -p 后面。
		agentFirst = `"$AGENT" -p "$PROMPT" --session-id "$SID" --tools "Read,Grep,Glob" --strict-mcp-config --permission-mode default > "$OUT" 2>"$ERR"`
		agentResume = `"$AGENT" -p "$PROMPT" --resume "$SID" --tools "Read,Grep,Glob" --strict-mcp-config --permission-mode default > "$OUT" 2>"$ERR"`
	case "codex":
		// 首次：--json 事件流进 $EV（含 thread_id），最终回复经 -o 写入 $OUT。
		// --skip-git-repo-check：codex 在非 git/未信任目录会直接拒跑（沙箱已是只读，放行无害）；
		// < /dev/null：stdin 非终端时 codex 会读 stdin 拼进提示词，bridge 下须显式断开。
		// codexIsolation：sandbox_mode 管不到 MCP 工具进程，exec 又从不询问，所以
		// 不加载用户 config.toml（其中的 mcp_servers / plugins），再清空 MCP、关掉插件与浏览器/电脑操控。
		agentFirst = `"$AGENT" exec --skip-git-repo-check --json -o "$OUT" ` + codexIsolation + ` -C "$RELAIS_MSG_DIR" "$PROMPT" < /dev/null > "$EV" 2>"$ERR"`
		agentAfterFirst = `SID="$(grep -o '"thread_id":"[^"]*"' "$EV" | head -1 | sed 's/.*:"//;s/"$//')"`
		agentResume = `"$AGENT" exec resume --skip-git-repo-check -o "$OUT" ` + codexIsolation + ` "$SID" "$PROMPT" < /dev/null > "$EV" 2>"$ERR"`
	default:
		return "", fmt.Errorf("本地模式只支持 claude 或 codex，得到 %q", info.Agent)
	}
	relais, _ := os.Executable()
	if relais == "" {
		relais = "relais"
	}
	hd := filepath.Join(dir, "hooks")
	if err := os.MkdirAll(hd, 0o755); err != nil {
		return "", err
	}
	newSID := `SID="$(uuidgen | tr 'A-Z' 'a-z')"`
	if info.Agent == "codex" {
		newSID = `SID=""` // codex 的 id 由首次运行产出
	}
	after := func(indent string) string {
		if agentAfterFirst == "" {
			return ""
		}
		return indent + agentAfterFirst + "\n"
	}
	script := "#!/bin/sh\n" +
		"# Relais 本地模式 hook（" + info.Agent + " 侧）：续会话 + 只读 + 五分支。由 relais local init 生成。\n" +
		"AGENT=\"" + info.AgentPath + "\"\n" +
		"RELAIS=\"${RELAIS_BIN:-" + relais + "}\"\n" +
		"export PATH=\"" + filepath.Dir(info.AgentPath) + ":" + filepath.Dir(relais) + ":$PATH\"\n" +
		"cd \"$RELAIS_MSG_DIR\" || exit 1\n" +
		"# 1) 服务器闸门\n" +
		"# 被拒时打印收到的原因（如「人的这条消息由 codex 侧接话，本侧不回」），原因为空才用通用文案\n" +
		"TURN_ERR=\"$(\"$RELAIS\" auto-turn 2>&1 >/dev/null)\" || { R=\"$(printf '%s' \"$TURN_ERR\" | sed 's/^relais 错误: *//' | head -1)\"; echo \"auto: ${R:-已暂停/到上限/需人处理，本条不自动回复}\"; exit 0; }\n" +
		"# 2) 会话：有则续，无则新建（首次要补读历史）\n" +
		"SID=\"$(\"$RELAIS\" session get)\"\n" +
		"FIRST=\"\"\n" +
		"if [ -z \"$SID\" ]; then FIRST=\"--first\"; " + newSID + "; fi\n" +
		"PROMPT=\"$(\"$RELAIS\" local-prompt $FIRST)\" || { echo \"auto: 生成提示词失败，本条跳过\"; exit 0; }\n" +
		"OUT=\"$(mktemp)\"; ERR=\"$(mktemp)\"; EV=\"$(mktemp)\"\n" +
		"# 3) 起讨论脑（只读）\n" +
		"if [ -n \"$FIRST\" ]; then\n" +
		"  " + agentFirst + "\n" +
		"  RC=$?\n" +
		after("  ") +
		"else\n" +
		"  " + agentResume + "\n" +
		"  RC=$?\n" +
		"  if [ $RC -ne 0 ]; then\n" +
		"    echo \"auto: 续会话失败（$SID），改为新建会话重来一次\"\n" +
		"    \"$RELAIS\" session clear\n" +
		"    " + newSID + "\n" +
		"    PROMPT=\"$(\"$RELAIS\" local-prompt --first)\" || { rm -f \"$OUT\" \"$ERR\" \"$EV\"; echo \"auto: 生成提示词失败，本条跳过\"; exit 0; }\n" +
		"    " + agentFirst + "\n" +
		"    RC=$?\n" +
		after("    ") +
		"  fi\n" +
		"fi\n" +
		"if [ $RC -ne 0 ]; then echo \"auto: agent 退出码 $RC，本条跳过\"; cat \"$ERR\"; rm -f \"$OUT\" \"$ERR\" \"$EV\"; exit 0; fi\n" +
		"[ -n \"$SID\" ] && \"$RELAIS\" session set \"$SID\"\n" +
		"# 4) 幂等键：源消息 id + 输出内容\n" +
		"KEY=\"$( { printf '%s' \"$RELAIS_MSG_ID\"; cat \"$OUT\"; } | shasum -a 256 | cut -c1-16)\"\n" +
		"# 5) 五分支（优先级：RESOLVED > NEEDS_HUMAN > --- > 跳过）\n" +
		"if head -1 \"$OUT\" | grep -q '^RESOLVED:'; then\n" +
		"  S=\"$(head -1 \"$OUT\" | sed 's/^RESOLVED: *//')\"\n" +
		"  BODY=\"$(mktemp)\"; tail -n +2 \"$OUT\" > \"$BODY\"\n" +
		"  \"$RELAIS\" send --kind resolved --summary \"$S\" --idempotency-key \"$KEY\" \"$BODY\"\n" +
		"  rm -f \"$BODY\"\n" +
		"elif head -1 \"$OUT\" | grep -q '^NEEDS_HUMAN:'; then\n" +
		"  Q=\"$(head -1 \"$OUT\" | sed 's/^NEEDS_HUMAN: *//')\"\n" +
		"  \"$RELAIS\" needs-human \"$Q\"\n" +
		"elif head -1 \"$OUT\" | grep -q '^---'; then\n" +
		"  \"$RELAIS\" send --idempotency-key \"$KEY\" \"$OUT\"\n" +
		"else\n" +
		"  echo \"auto: agent 输出格式不符，跳过本条\"\n" +
		"fi\n" +
		"rm -f \"$OUT\" \"$ERR\" \"$EV\"\n"
	hp := filepath.Join(hd, "auto-reply.sh")
	if err := os.WriteFile(hp, []byte(script), 0o755); err != nil {
		return "", err
	}
	return hp, nil
}
