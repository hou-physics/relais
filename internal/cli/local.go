package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

const localHuman = "hou"

func localDir() (string, error) {
	if d := os.Getenv("RELAIS_LOCAL_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "relais-local"), nil
}

func RunLocal(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais local <init|bootstrap|status|close> ...")
	}
	switch args[0] {
	case "init":
		return runLocalInit(args[1:])
	case "bootstrap":
		return runLocalBootstrap(args[1:])
	case "status":
		return runLocalStatus()
	case "close":
		if len(args) != 2 {
			return fmt.Errorf("用法: relais local close <模块>")
		}
		return runLocalClose(args[1])
	default:
		return fmt.Errorf("未知 local 子命令 %q", args[0])
	}
}

func localServerConfigPath(ld string) string { return filepath.Join(ld, "server.toml") }

func runLocalInit(args []string) error {
	fs := flag.NewFlagSet("local init", flag.ContinueOnError)
	project := fs.String("project", "", "项目目录（默认当前目录）")
	listen := fs.String("listen", "127.0.0.1:8080", "本地服务器监听地址")
	noService := fs.Bool("no-service", false, "不安装 launchd 常驻（测试/手动运行用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	modules := fs.Args()
	if len(modules) == 0 {
		return fmt.Errorf("用法: relais local init [--project <dir>] [--listen <addr>] [--no-service] <模块名>...")
	}
	root := *project
	if root == "" {
		var err error
		if root, err = os.Getwd(); err != nil {
			return err
		}
	}
	root, _ = filepath.Abs(root)
	ld, err := localDir()
	if err != nil {
		return err
	}
	mgr := newLocalManager(ld)
	res, err := mgr.bootstrap(*listen)
	if err != nil {
		return err
	}
	for _, m := range modules {
		if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: m, Dir: root}); err != nil {
			return err
		}
	}
	scPath := localServerConfigPath(ld)
	if !*noService {
		if err := installLocalServices(ld, scPath); err != nil {
			return err
		}
		waitListen(*listen, 5*time.Second)
	}
	// 路径含空格（~/Library/Application Support），打印时一律单引号包起来，复制即可用
	fmt.Printf(`本地模式已就绪（%s）
  控制台: %s   （本机打开即可，无需登录）
  模块: %s
  项目目录: %s   （协议在 relais/PROTOCOL.md）
下一步：在 Claude Code / Codex 对话里说「接入 relais 模块 <模块名>」。
`, shq(ld), res.BaseURL, strings.Join(modules, ", "), shq(root))
	if *noService {
		fmt.Printf("未安装常驻（--no-service）。手动运行：\n  relais serve --config %s\n", shq(scPath))
	}
	return nil
}

// installLocalServices：serve + 两个 bridge 的 launchd 常驻；已存在的 plist 跳过（spec §3.1）。
// 提示信息走 stderr，保证 bootstrap --json 的 stdout 只有一行 JSON。
func installLocalServices(ld, scPath string) error {
	relais, _ := os.Executable()
	type svc struct {
		label string
		args  []string
		env   map[string]string
	}
	svcs := []svc{{"com.relais.local.serve", []string{relais, "serve", "--config", scPath}, nil}}
	for _, side := range []string{"claude", "codex"} {
		d := filepath.Join(ld, "sides", side)
		svcs = append(svcs, svc{"com.relais.local.bridge." + side,
			[]string{relais, "bridge", "--interval", "5", "--hook", filepath.Join(d, "hooks", "auto-reply.sh")},
			map[string]string{"RELAIS_CONFIG_DIR": d, "HOME": os.Getenv("HOME"), "PATH": os.Getenv("PATH")}})
	}
	for _, v := range svcs {
		p := plistPathFor(v.label)
		if !plistNeedsInstall(p, relais) {
			fmt.Fprintf(os.Stderr, "常驻 %s 已存在，跳过\n", v.label)
			continue
		}
		if _, err := os.Stat(p); err == nil {
			fmt.Fprintf(os.Stderr, "常驻 %s 指向旧二进制，重装\n", v.label)
		}
		if _, err := installPlist(v.label, v.args, v.env); err != nil {
			return err
		}
	}
	return nil
}

// runLocalBootstrap：只搭环境（服务器配置、三账号、设置、常驻），不建模块——
// 模块由控制台网页新建。双击安装脚本调用它（--json）。
func runLocalBootstrap(args []string) error {
	fs := flag.NewFlagSet("local bootstrap", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:8080", "本地服务器监听地址")
	noService := fs.Bool("no-service", false, "不安装 launchd 常驻")
	asJSON := fs.Bool("json", false, "只输出一行 JSON（安装脚本用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ld, err := localDir()
	if err != nil {
		return err
	}
	mgr := newLocalManager(ld)
	res, err := mgr.bootstrap(*listen)
	if err != nil {
		return err
	}
	if !*noService {
		if err := installLocalServices(ld, localServerConfigPath(ld)); err != nil {
			return err
		}
		waitListen(*listen, 5*time.Second)
	}
	if *asJSON {
		return printBootstrapJSON(os.Stdout, res)
	}
	fmt.Printf("本地模式环境已就绪：%s\n控制台: %s（本机打开即可，无需登录）\n", shq(ld), res.BaseURL)
	return nil
}

// printBootstrapJSON：安装脚本读的契约——恰好一行 JSON，两个键（M9 起不再有密码）。
// bootstrap --json 路径上的其它提示（常驻跳过、waitListen 超时）一律走 stderr。
func printBootstrapJSON(w io.Writer, res bootstrapResult) error {
	return json.NewEncoder(w).Encode(map[string]any{"base_url": res.BaseURL, "human_user": res.HumanUser})
}

// isLoopbackListen：本地模式只允许监听回环地址（spec §11）。
func isLoopbackListen(addr string) bool {
	return strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:")
}

// shq 把路径包成 shell 单引号字面量（路径含空格时复制粘贴仍可用）。
func shq(p string) string {
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

func plistPathFor(label string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

// plistNeedsInstall：plist 不存在，或它的程序路径不是当前二进制（升级后安装位置变了，如 Intel Mac 的
// /usr/local/bin）时需要（重）装；已指向当前二进制则不动，避免重跑时重载打断正在跑的循环。
func plistNeedsInstall(path, exe string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	return !strings.Contains(string(data), "<string>"+xmlEscape(exe)+"</string>")
}

func waitListen(addr string, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "提示：%s 还没响应，launchd 可能仍在启动；稍后用 relais local status 确认。\n", addr)
}

func runLocalStatus() error {
	ld, err := localDir()
	if err != nil {
		return err
	}
	cfg, err := loadServerConfig(localServerConfigPath(ld))
	if err != nil {
		return fmt.Errorf("本地模式未初始化？%w", err)
	}
	mods, err := newLocalManager(ld).ListModules()
	if err != nil {
		return err
	}
	up := "未响应"
	if c, err := net.DialTimeout("tcp", cfg.Listen, 300*time.Millisecond); err == nil {
		c.Close()
		up = "在跑"
	}
	fmt.Printf("本地服务器 %s：%s（%s）\n", cfg.Listen, up, ld)
	for _, md := range mods {
		claude := "claude:没在等"
		if md.Claude.Waiting {
			claude = "claude:在等"
		}
		codex := "codex:未接入"
		if md.Codex.Attached {
			codex = "codex:已接入「" + md.Codex.ThreadName + "」"
		}
		state := md.State
		if md.NeedsHumanQ != "" {
			state += "：" + md.NeedsHumanQ
		}
		fmt.Printf("  %-16s %-10s 第 %d/%d 回合  %s  %s  %s\n", md.Name, md.Mode, md.Round, md.RoundCap, state, claude, codex)
	}
	return nil
}

func runLocalClose(channel string) error {
	ld, err := localDir()
	if err != nil {
		return err
	}
	if err := newLocalManager(ld).CloseModule(channel); err != nil {
		return err
	}
	fmt.Printf("模块 %q 已关闭：守卫不再收发；消息与信箱文件保留。\n", channel)
	return nil
}
