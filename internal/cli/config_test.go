package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalConfigRoundTrip(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())
	if _, err := loadGlobal(); err == nil || !strings.Contains(err.Error(), "relais login") {
		t.Fatalf("未登录应提示 relais login, got %v", err)
	}
	want := &GlobalConfig{Server: "http://s", Token: "tk", Username: "hou"}
	if err := saveGlobal(want); err != nil {
		t.Fatal(err)
	}
	got, err := loadGlobal()
	if err != nil || *got != *want {
		t.Fatalf("配置往返失真: %+v %v", got, err)
	}
}

func TestFindProjectWalksUp(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b")
	os.MkdirAll(filepath.Join(root, "relais"), 0o755)
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(root, "relais", "config.toml"),
		[]byte("server = \"http://s\"\nchannel = \"deutschapp\"\n"), 0o644)
	t.Chdir(sub)
	dir, cfg, err := findProject()
	if err != nil || cfg.Channel != "deutschapp" {
		t.Fatalf("应向上找到项目: %v %+v", err, cfg)
	}
	if dir != root {
		t.Fatalf("项目根应为 %s, got %s", root, dir)
	}
	t.Chdir(t.TempDir())
	if _, _, err := findProject(); err == nil || !strings.Contains(err.Error(), "relais init") {
		t.Fatalf("找不到项目应提示 relais init, got %v", err)
	}
}
