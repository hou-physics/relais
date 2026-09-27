// 服务器本机管理命令（spec §7：直连数据库，远程管理走网页）。
package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

type ServerConfig struct {
	Listen  string `toml:"listen"`
	DataDir string `toml:"data_dir"`
	BaseURL string `toml:"base_url"`
	// LocalDir：本地模式的配置根目录（M8；联网部署留空）。
	LocalDir string `toml:"local_dir"`
}

func loadServerConfig(path string) (*ServerConfig, error) {
	cfg := &ServerConfig{Listen: "127.0.0.1:8080"}
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("读取服务器配置 %s 失败: %w", path, err)
	}
	if cfg.DataDir == "" || cfg.BaseURL == "" {
		return nil, fmt.Errorf("服务器配置需含 data_dir 与 base_url")
	}
	return cfg, nil
}

func openServerStore(configPath string) (*store.Store, *ServerConfig, error) {
	cfg, err := loadServerConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "relais.db"))
	return st, cfg, err
}

func RunServe(args []string) error { return runServe(context.Background(), args) }

// runServe：ctx 结束（或收到 SIGINT/SIGTERM）时关掉 HTTP 服务、等守卫退出后返回；测试用 ctx 停服。
func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "/etc/relais/server.toml", "服务器配置文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, cfg, err := openServerStore(*configPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if cfg.LocalDir != "" && !isLoopbackListen(cfg.Listen) {
		// 本地管理接口能写任意目录、起工作脑，绝不能暴露在非回环地址上（spec §11）
		return fmt.Errorf("server.toml 设了 local_dir（本地模式），但 listen = %q 不是回环地址；本地模式只允许 127.0.0.1 或 localhost，拒绝启动", cfg.Listen)
	}
	fmt.Printf("relais 服务启动: %s (base_url=%s)\n", cfg.Listen, cfg.BaseURL)
	srv := server.New(st, cfg.BaseURL, cfg.DataDir)
	hs := &http.Server{Addr: cfg.Listen} // Handler 须在 SetLocal 之后取，否则本地路由不注册
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	daemonDone := make(chan struct{})
	close(daemonDone) // 非本地模式没有守卫，视为已结束
	if cfg.LocalDir != "" {
		// 同一个 mgr 既给 SetLocal 又开守卫：Redeliver 经 mgr.daemon 调用，store 句柄也共用
		mgr := newLocalManager(cfg.LocalDir)
		srv.SetLocal(mgr, localHuman)
		d, err := mgr.newDaemon(srv.PublishMessage)
		if err != nil {
			return err
		}
		defer func() {
			if mgr.shared != nil {
				mgr.shared.Close()
			}
		}()
		daemonDone = make(chan struct{})
		go func() {
			defer close(daemonDone)
			d.Run(ctx)
		}()
		fmt.Printf("本地模式管理接口已启用（%s）\n守卫已启动（每 2 秒扫一次 outbox）\n", cfg.LocalDir)
	}
	// NotifyContext 接管了 SIGINT/SIGTERM 的默认退出：ctx 结束时关掉 HTTP 服务；随即 stop()，再按一次 Ctrl-C 走默认处理
	go func() {
		<-ctx.Done()
		stop()
		hs.Close()
	}()
	hs.Handler = srv.Handler()
	err = hs.ListenAndServe()
	stop() // 监听失败时也让守卫退出
	<-daemonDone
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func RunUser(args []string) error {
	if len(args) < 2 || args[0] != "add" {
		return fmt.Errorf("用法: relais user add <用户名> [--display <显示名>] --config <server.toml>")
	}
	username := args[1]
	fs := flag.NewFlagSet("user add", flag.ContinueOnError)
	display := fs.String("display", username, "显示名")
	configPath := fs.String("config", "/etc/relais/server.toml", "服务器配置文件")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	st, _, err := openServerStore(*configPath)
	if err != nil {
		return err
	}
	defer st.Close()
	password := newPassword()
	u, err := st.CreateUser(username, *display, password)
	if err != nil {
		return err
	}
	fmt.Printf("用户 %s 已创建\n  初始密码: %s （请让本人登录网页后使用；本密码只显示这一次）\n  agent token: %s\n",
		u.Username, password, u.AgentToken)
	return nil
}

func RunChannel(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("用法: relais channel create <频道名> | relais channel add <频道名> <用户名>  （均需 --config）")
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("channel create", flag.ContinueOnError)
		configPath := fs.String("config", "/etc/relais/server.toml", "服务器配置文件")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		st, _, err := openServerStore(*configPath)
		if err != nil {
			return err
		}
		defer st.Close()
		ch, err := st.CreateChannel(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("频道 %s 已创建\n", ch.Name)
		return nil
	case "add":
		if len(args) < 3 {
			return fmt.Errorf("用法: relais channel add <频道名> <用户名> --config <server.toml>")
		}
		fs := flag.NewFlagSet("channel add", flag.ContinueOnError)
		configPath := fs.String("config", "/etc/relais/server.toml", "服务器配置文件")
		if err := fs.Parse(args[3:]); err != nil {
			return err
		}
		st, _, err := openServerStore(*configPath)
		if err != nil {
			return err
		}
		defer st.Close()
		ch, err := st.ChannelByName(args[1])
		if err != nil {
			return fmt.Errorf("频道 %q 不存在", args[1])
		}
		u, err := st.UserByName(args[2])
		if err != nil {
			return fmt.Errorf("用户 %q 不存在", args[2])
		}
		if err := st.AddMember(ch.ID, u.ID); err != nil {
			return err
		}
		fmt.Printf("%s 已加入频道 %s\n", u.Username, ch.Name)
		return nil
	default:
		return fmt.Errorf("未知 channel 子命令 %q", args[0])
	}
}

func RunInvite(args []string) error {
	fs := flag.NewFlagSet("invite", flag.ContinueOnError)
	channel := fs.String("channel", "", "邀请加入的频道（可空）")
	configPath := fs.String("config", "/etc/relais/server.toml", "服务器配置文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, cfg, err := openServerStore(*configPath)
	if err != nil {
		return err
	}
	defer st.Close()
	var chID int64
	if *channel != "" {
		ch, err := st.ChannelByName(*channel)
		if err != nil {
			return fmt.Errorf("频道 %q 不存在", *channel)
		}
		chID = ch.ID
	}
	admin, err := st.FirstUser()
	if err != nil {
		return fmt.Errorf("请先用 relais user add 创建至少一个用户")
	}
	code, err := st.CreateInvite(chID, admin.ID, 7*24*time.Hour)
	if err != nil {
		return err
	}
	fmt.Printf("邀请链接（7 天内一次性有效）：\n  %s/join/%s\n", cfg.BaseURL, code)
	return nil
}

func RunAdminGrant(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: relais admin grant <用户名> --config <server.toml>")
	}
	username := args[0]
	fs := flag.NewFlagSet("admin grant", flag.ContinueOnError)
	configPath := fs.String("config", "/etc/relais/server.toml", "服务器配置文件")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	st, _, err := openServerStore(*configPath)
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := st.UserByName(username)
	if err != nil {
		return fmt.Errorf("用户 %q 不存在", username)
	}
	if err := st.SetAdmin(u.ID, true); err != nil {
		return err
	}
	fmt.Printf("%s 已设为管理员\n", username)
	return nil
}

func newPassword() string {
	const chars = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 12)
	for i := range b {
		b[i] = chars[randInt(len(chars))]
	}
	return string(b)
}

func randInt(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}
