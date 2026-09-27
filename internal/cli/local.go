package cli

import (
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/store"
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

const rulesTemplate = `# 本项目铁律（两侧讨论脑每轮都读）

写具体、可判定的规矩，一行一条。例如：
- 不部署、不推送，除非负责人明确说。
- 不碰 data/app.sqlite 与本机 8000 端口。
- 术语以 CONTEXT.md 为准。
`

func RunLocal(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais local <init|status|close> ...")
	}
	switch args[0] {
	case "init":
		return runLocalInit(args[1:])
	case "status":
		return runLocalStatus()
	case "close":
		if len(args) != 2 {
			return fmt.Errorf("用法: relais local close <频道>")
		}
		return runLocalClose(args[1])
	default:
		return fmt.Errorf("未知 local 子命令 %q", args[0])
	}
}

func localServerConfigPath(ld string) string { return filepath.Join(ld, "server.toml") }

func openLocalStore() (*store.Store, *ServerConfig, string, error) {
	ld, err := localDir()
	if err != nil {
		return nil, nil, "", err
	}
	st, cfg, err := openServerStore(localServerConfigPath(ld))
	return st, cfg, ld, err
}

func runLocalInit(args []string) error {
	fs := flag.NewFlagSet("local init", flag.ContinueOnError)
	claudePath := fs.String("claude", "", "claude 可执行文件路径（默认 PATH 侦测）")
	codexPath := fs.String("codex", "", "codex 可执行文件路径（默认 PATH 侦测；本机常不在 PATH）")
	project := fs.String("project", "", "项目目录（默认当前目录）")
	listen := fs.String("listen", "127.0.0.1:8080", "本地服务器监听地址")
	noService := fs.Bool("no-service", false, "不安装 launchd 常驻（测试/手动运行用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	modules := fs.Args()
	if len(modules) == 0 {
		return fmt.Errorf("用法: relais local init [--claude <path>] [--codex <path>] [--project <dir>] [--no-service] <模块名>...")
	}
	for _, m := range modules {
		if strings.ContainsAny(m, " /\\") {
			return fmt.Errorf("模块名 %q 不能含空格或斜杠", m)
		}
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
	claudeP, codexP := *claudePath, *codexPath
	if claudeP == "" {
		claudeP, _ = exec.LookPath("claude")
	}
	if codexP == "" {
		codexP, _ = exec.LookPath("codex")
	}
	res, err := mgr.bootstrap(*listen, claudeP, codexP)
	if err != nil {
		return err
	}
	if res.PasswordShown {
		fmt.Printf("网页 %s 登录账号: %s\n初始密码: %s（已存到 %s）\n", res.BaseURL, res.HumanUser, res.HumanPassword, shq(filepath.Join(ld, "human.txt")))
	}
	for _, m := range modules {
		if _, err := mgr.CreateModule(m, root); err != nil {
			return err
		}
	}
	baseURL := res.BaseURL
	scPath := localServerConfigPath(ld)
	// 常驻：serve + 两个 bridge（spec §3.1：已在跑则跳过——plist 已存在就不重装、不重载）
	if !*noService {
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
			if !shouldInstallPlist(plistPathFor(v.label)) {
				fmt.Printf("常驻 %s 已存在，跳过\n", v.label)
				continue
			}
			if _, err := installPlist(v.label, v.args, v.env); err != nil {
				return err
			}
		}
		waitListen(*listen, 5*time.Second)
	}
	// 路径含空格（~/Library/Application Support），打印时一律单引号包起来，复制即可用
	fmt.Printf(`本地模式已就绪（%s）
  网页监督台: %s   （账号 %s，密码见 %s）
  模块频道: %s
  项目目录: %s   （规矩写在 relais/RULES.md）
  两侧配置: %s
下一步：在工作脑里把议题写成第一封信 →
  RELAIS_CONFIG_DIR=%s RELAIS_CHANNEL=<模块> relais send <文件>
  （或在网页里以本人身份发第一封）
`, shq(ld), baseURL, localHuman, shq(filepath.Join(ld, "human.txt")), strings.Join(modules, ", "), shq(root),
		shq(filepath.Join(ld, "sides"))+"/{claude,codex}", shq(filepath.Join(ld, "sides"))+"/<你这侧>")
	if *noService {
		fmt.Printf("未安装常驻（--no-service）。手动运行：\n  relais serve --config %s\n  RELAIS_CONFIG_DIR=%s relais bridge --interval 5 --hook %s\n  RELAIS_CONFIG_DIR=%s relais bridge --interval 5 --hook %s\n",
			shq(scPath),
			shq(filepath.Join(ld, "sides", "claude")), shq(filepath.Join(ld, "sides", "claude", "hooks", "auto-reply.sh")),
			shq(filepath.Join(ld, "sides", "codex")), shq(filepath.Join(ld, "sides", "codex", "hooks", "auto-reply.sh")))
	}
	return nil
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

// shouldInstallPlist：plist 已存在（服务已装、多半在跑）则不重装，避免重跑 init 时 bootout/重载打断正在跑的循环。
func shouldInstallPlist(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
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
	fmt.Printf("提示：%s 还没响应，launchd 可能仍在启动；稍后用 relais local status 确认。\n", addr)
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
		state := map[string]string{"running": "运行", "closed": "已关闭", "kicked_off": "已开工",
			"resolved": "已握手待确认", "needs_human": "等你：" + md.NeedsHumanQ, "paused": "已暂停"}[md.State]
		sids := ""
		for _, side := range []string{"claude", "codex"} {
			id, _ := sessionGet(filepath.Join(ld, "sides", side), md.Name)
			if id != "" {
				sids += side + "✓ "
			} else {
				sids += side + "– "
			}
		}
		fmt.Printf("  %-16s %-10s 第 %d/%d 回合  %s  会话 %s\n", md.Name, md.Mode, md.Round, md.RoundCap, state, sids)
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
	fmt.Printf("频道 %q 已归档：循环停止、两侧讨论会话作废；消息与文件保留。\n", channel)
	return nil
}
