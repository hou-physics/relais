package cli

import (
	"strings"
	"testing"
)

func TestAutoOnOffStatus(t *testing.T) {
	_, _, _ = setupCLITest(t, "hou", "duo")
	if err := RunAuto([]string{"on", "--cap", "2"}); err != nil {
		t.Fatalf("auto on 失败: %v", err)
	}
	if err := RunAuto([]string{"status"}); err != nil {
		t.Fatalf("auto status 失败: %v", err)
	}
	// agent 请求 turn：前2次放行，第3次被拒(exit 非0)
	if err := RunAutoTurn(nil); err != nil {
		t.Fatalf("第1轮 turn 应放行: %v", err)
	}
	if err := RunAutoTurn(nil); err != nil {
		t.Fatalf("第2轮 turn 应放行: %v", err)
	}
	if err := RunAutoTurn(nil); err == nil {
		t.Fatal("第3轮 turn 应被拒(返回错误)")
	}
	if err := RunAuto([]string{"off"}); err != nil {
		t.Fatal(err)
	}
	if err := RunAutoTurn(nil); err == nil {
		t.Fatal("关闭后 turn 应被拒")
	}
}

func TestNeedsHumanAndGuidance(t *testing.T) {
	_, _, _ = setupCLITest(t, "hou", "duo")
	RunAuto([]string{"on"})
	if err := RunNeedsHuman([]string{"预算多少？"}); err != nil {
		t.Fatalf("needs-human 失败: %v", err)
	}
	// needs-human 后 turn 被拒
	if err := RunAutoTurn(nil); err == nil {
		t.Fatal("needs-human 后 turn 应被拒")
	}
	// guidance-pull：先没有
	// （写引导要人的钥匙，这里用 API 直接构造略；仅验证命令能跑通不报错）
	if err := RunGuidancePull(nil); err != nil {
		t.Fatalf("guidance-pull 失败: %v", err)
	}
	_ = strings.TrimSpace
}
