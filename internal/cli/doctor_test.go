package cli

import (
	"strings"
	"testing"
)

func TestDoctorChecksReportFixes(t *testing.T) {
	// 未登录、无 setup → 至少两项红且带修复指引
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	checks := runChecks()
	if len(checks) < 3 {
		t.Fatalf("应有多项检查: %d", len(checks))
	}
	reds := 0
	for _, c := range checks {
		if !c.ok {
			reds++
			if c.fix == "" {
				t.Fatalf("红灯项必须带修复指引: %+v", c)
			}
		}
	}
	if reds == 0 {
		t.Fatal("空配置下应有红灯项")
	}
	// 登录项应红且提示 relais login
	found := false
	for _, c := range checks {
		if strings.Contains(c.name, "登录") && !c.ok && strings.Contains(c.fix, "relais login") {
			found = true
		}
	}
	if !found {
		t.Fatal("未登录应报红并提示 relais login")
	}
}
