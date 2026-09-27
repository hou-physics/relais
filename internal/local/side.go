package local

import "fmt"

// DetectSide 按环境判断当前对话是哪一侧（spec §6.4）。--as 由调用方优先处理。
func DetectSide(getenv func(string) string) (string, error) {
	claude := getenv("CLAUDECODE") != ""
	codex := getenv("CODEX_HOME") != "" || getenv("CODEX_SANDBOX") != "" || getenv("CODEX_SANDBOX_NETWORK_DISABLED") != ""
	switch {
	case claude && codex:
		return "", fmt.Errorf("环境里同时有 Claude Code 与 Codex 的标记，请加 --as claude 或 --as codex")
	case claude:
		return "claude", nil
	case codex:
		return "codex", nil
	}
	return "", fmt.Errorf("判断不出当前是哪一侧（没有 CLAUDECODE 或 CODEX_HOME 环境变量），请加 --as claude 或 --as codex")
}
