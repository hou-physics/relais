package cli

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestServiceArtifactShape(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	// 只验证生成物内容/命令构造，不真正加载服务（避免污染系统）：用 dryRunInstallService
	spec, err := serviceSpec()
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(spec, "launchd") && !strings.Contains(spec, "plist") {
			t.Fatalf("mac 应产出 launchd plist: %q", spec)
		}
	case "windows":
		if !strings.Contains(spec, "schtasks") {
			t.Fatalf("windows 应用 schtasks: %q", spec)
		}
	}
	if !strings.Contains(spec, "bridge") {
		t.Fatal("服务应运行 relais bridge")
	}
}

func TestWindowsHookHasGuardrails(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	info := SetupInfo{OS: "windows", Agent: "codex", AgentPath: `C:\Users\x\codex.exe`, Mode: "auto"}
	hp, err := writeHookWindows(info)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hp)
	h := string(data)
	for _, want := range []string{"auto-turn", "guidance-pull", "needs-human", "send", "NEEDS_HUMAN"} {
		if !strings.Contains(h, want) {
			t.Fatalf("windows hook 缺少 %q", want)
		}
	}
	// 检查格式校验：必须在 send 前检查 --- 前缀
	if !strings.Contains(h, "findstr /b /c:\"---\"") {
		t.Fatal("windows hook 缺少格式校验（--- 前缀）")
	}
	// 闸门锚点：auto-turn 被拒时必须真正 exit（& 分隔命令，非转义的 ^& 字面量），
	// 否则会越过闸门继续跑到 send。防止修复轮 1 的 ^& 回归复发。
	if !strings.Contains(h, "& exit /b 0)") {
		t.Fatal("windows hook auto-turn 拒绝分支缺少真正的 exit（闸门失效）")
	}
	if strings.Contains(h, "^& exit /b 0") {
		t.Fatal("windows hook auto-turn 分支用了转义的 ^&，exit 不会执行（闸门失效）")
	}
	// 转义锚点：GUIDANCE（雇主自由文本）与 Q（agent 输出）拼进命令行前必须剥离双引号，
	// 否则一个 " 会截断 -p "%PROMPT%" / needs-human "!Q!"。
	if !strings.Contains(h, `set GUIDANCE=!GUIDANCE:"=!`) {
		t.Fatal("windows hook 未剥离 GUIDANCE 里的双引号（命令行注入/截断风险）")
	}
	if !strings.Contains(h, `set Q=!Q:"=!`) {
		t.Fatal("windows hook 未剥离 Q 里的双引号（needs-human 参数截断风险）")
	}
	// Q 去引号必须在第一个 if !errorlevel!（NEEDS_HUMAN 块）之前，即顶层行。
	// 放进括号块里的单个未配对 " 会让 cmd 算错块的引号开合、找不到匹配的 )，破坏整块解析。
	qi := strings.Index(h, `set Q=!Q:"=!`)
	ifi := strings.Index(h, "if !errorlevel!==0")
	if qi < 0 || ifi < 0 || qi > ifi {
		t.Fatal("Q 去引号必须在 NEEDS_HUMAN if 块之前（顶层行），否则块内未配对引号破坏 cmd 括号解析")
	}
	// 临时文件用 %RANDOM% 避免并发碰撞
	if !strings.Contains(h, "relais-reply-%RANDOM%.md") {
		t.Fatal("windows hook 临时输出文件未加 %RANDOM%（并发碰撞风险）")
	}
}
