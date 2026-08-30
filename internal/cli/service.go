package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// serviceSpec 返回"将要安装的服务"的描述串（供测试与 dry-run），内容与 installService 一致。
func serviceSpec() (string, error) {
	relais, _ := os.Executable()
	if relais == "" {
		relais = "relais"
	}
	switch runtime.GOOS {
	case "darwin":
		return "launchd plist: " + relais + " bridge（开机自启常驻）", nil
	case "windows":
		return "schtasks /Create ... /TR \"" + relais + " bridge\"（登录时启动）", nil
	default:
		return "systemd user service: " + relais + " bridge", nil
	}
}

func installService() (string, error) {
	relais, err := os.Executable()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", "com.relais.bridge.plist")
		content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.relais.bridge</string>
  <key>ProgramArguments</key><array><string>` + relais + `</string><string>bridge</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>`
		if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
			return "", err
		}
		_ = exec.Command("launchctl", "unload", plist).Run()
		if err := exec.Command("launchctl", "load", plist).Run(); err != nil {
			return "", fmt.Errorf("加载 launchd 失败: %w", err)
		}
		return "已装成 macOS 后台服务（开机自启）：" + plist, nil
	case "windows":
		cmd := exec.Command("schtasks", "/Create", "/F", "/SC", "ONLOGON", "/TN", "RelaisBridge", "/TR", "\""+relais+"\" bridge")
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("注册 Windows 计划任务失败: %w", err)
		}
		return "已装成 Windows 登录启动任务：RelaisBridge", nil
	default:
		return "Linux 请用 systemd --user（见 ops.md）", nil
	}
}
