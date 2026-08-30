package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryUpsert(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if ps, _ := loadProjects(); len(ps) != 0 {
		t.Fatalf("空注册表应为空: %+v", ps)
	}
	if err := registerProject("phineuro", "/tmp/a"); err != nil {
		t.Fatal(err)
	}
	if err := registerProject("general", "/tmp/b"); err != nil {
		t.Fatal(err)
	}
	if err := registerProject("phineuro", "/tmp/neu"); err != nil { // 覆盖
		t.Fatal(err)
	}
	ps, err := loadProjects()
	if err != nil || len(ps) != 2 {
		t.Fatalf("应 2 条: %+v %v", ps, err)
	}
	m := map[string]string{}
	for _, p := range ps {
		m[p.Channel] = p.Dir
	}
	if m["phineuro"] != "/tmp/neu" || m["general"] != "/tmp/b" {
		t.Fatalf("upsert 语义不对: %+v", m)
	}
}

func TestInitRegistersProject(t *testing.T) {
	_, _, proj := setupCLITest(t, "hou", "duo") // setupCLITest 内部已跑 RunInit
	ps, err := loadProjects()
	if err != nil || len(ps) != 1 || ps[0].Channel != "duo" || ps[0].Dir != proj {
		t.Fatalf("init 应注册项目: %+v %v", ps, err)
	}
}

func TestPollOnceLandsNotifiesAndHooks(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	seedIncoming(t, st, users, "duo", 2)
	c, _, err := newClient()
	if err != nil {
		t.Fatal(err)
	}
	var notified []string
	marker := filepath.Join(t.TempDir(), "hook-ran")
	// hook 把消息路径追加进 marker 文件，验证环境变量与执行
	hook := "echo \"$RELAIS_MSG_ID $RELAIS_MSG_DIR\" >> " + marker
	landed, err := pollOnce(c, []bridgeTarget{{Channel: "duo", Dir: proj}}, hook,
		func(from, summary string) { notified = append(notified, from+":"+summary) })
	if err != nil || landed != 2 {
		t.Fatalf("应落 2 条: %d %v", landed, err)
	}
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "inbox"))
	if len(entries) != 2 {
		t.Fatalf("inbox 应 2 个文件: %v", entries)
	}
	if len(notified) != 2 || !strings.HasPrefix(notified[0], "wu:") {
		t.Fatalf("应通知 2 次: %v", notified)
	}
	data, err := os.ReadFile(marker)
	if err != nil || strings.Count(string(data), "\n") != 2 || !strings.Contains(string(data), proj) {
		t.Fatalf("hook 应执行 2 次并带 RELAIS_MSG_DIR: %q %v", data, err)
	}
	// 第二轮：无未读 → 0 落盘，不重复通知
	landed, err = pollOnce(c, []bridgeTarget{{Channel: "duo", Dir: proj}}, "", nil)
	if err != nil || landed != 0 {
		t.Fatalf("第二轮应 0: %d %v", landed, err)
	}
}
