package cli

import (
	"testing"
)

func TestAutoOnOffStatus(t *testing.T) {
	st, _, _ := setupCLITest(t, "hou", "duo")

	// 直接设置自主状态（人的操作在网页上完成，这里用 store 模拟）
	ch, _ := st.ChannelByName("duo")
	if err := st.SetAutoEnabled(ch.ID, true, 2); err != nil {
		t.Fatal(err)
	}

	// RunAuto("status") 应能读取状态
	if err := RunAuto([]string{"status"}); err != nil {
		t.Fatalf("auto status 失败: %v", err)
	}

	// RunAuto("on") 应返回 nil（打印网页重定向提示）
	if err := RunAuto([]string{"on", "--cap", "2"}); err != nil {
		t.Fatalf("auto on 应返回 nil: %v", err)
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

	// 关闭自主模式
	if err := st.SetAutoEnabled(ch.ID, false, 0); err != nil {
		t.Fatal(err)
	}

	// RunAuto("off") 应返回 nil（打印网页重定向提示）
	if err := RunAuto([]string{"off"}); err != nil {
		t.Fatalf("auto off 应返回 nil: %v", err)
	}

	// 关闭后 turn 应被拒
	if err := RunAutoTurn(nil); err == nil {
		t.Fatal("关闭后 turn 应被拒")
	}
}

func TestNeedsHumanAndGuidance(t *testing.T) {
	st, _, _ := setupCLITest(t, "hou", "duo")

	// 直接设置自主状态
	ch, _ := st.ChannelByName("duo")
	if err := st.SetAutoEnabled(ch.ID, true, 6); err != nil {
		t.Fatal(err)
	}

	// agent 标记需要人处理
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
}
