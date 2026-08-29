// 服务器本机管理命令（spec §7：直连数据库，远程管理走网页）。
package cli

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

type ServerConfig struct {
	Listen  string `toml:"listen"`
	DataDir string `toml:"data_dir"`
	BaseURL string `toml:"base_url"`
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

func RunServe(args []string) error {
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
	fmt.Printf("relais 服务启动: %s (base_url=%s)\n", cfg.Listen, cfg.BaseURL)
	return http.ListenAndServe(cfg.Listen, server.New(st, cfg.BaseURL, cfg.DataDir).Handler())
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
