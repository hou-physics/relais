package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/hou-physics/relais/internal/api"
	"golang.org/x/term"
	"syscall"
)

type AdminConfig struct {
	Server  string `toml:"server"`
	Session string `toml:"session"`
}

func adminConfigPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "admin.toml"), nil
}

func loadAdminConfig() (*AdminConfig, error) {
	path, err := adminConfigPath()
	if err != nil {
		return nil, err
	}
	var cfg AdminConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("尚未以管理员登录：请先运行 relais admin login <服务器地址>")
	}
	return &cfg, nil
}

func saveAdminConfig(cfg *AdminConfig) error {
	path, err := adminConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// saveAdminConfigForTest 供测试注入；生产代码不得调用。
func saveAdminConfigForTest(server, session string) error {
	return saveAdminConfig(&AdminConfig{Server: server, Session: session})
}

func newAdminClient() (*AdminClient, error) {
	cfg, err := loadAdminConfig()
	if err != nil {
		return nil, err
	}
	return &AdminClient{Server: strings.TrimRight(cfg.Server, "/"), Session: cfg.Session,
		hc: &http.Client{Timeout: 30 * time.Second}}, nil
}

func RunAdmin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais admin <login|channel|member|invite|grant> ...")
	}
	switch args[0] {
	case "grant":
		return RunAdminGrant(args[1:])
	case "login":
		return runAdminLogin(args[1:])
	case "channel":
		return runAdminChannel(args[1:])
	case "member":
		return runAdminMember(args[1:])
	case "invite":
		return runAdminInvite(args[1:])
	default:
		return fmt.Errorf("未知 admin 子命令 %q", args[0])
	}
}

func runAdminLogin(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: relais admin login <服务器地址>")
	}
	server := strings.TrimRight(args[0], "/")
	fmt.Print("用户名: ")
	reader := bufio.NewReader(os.Stdin)
	username, _ := reader.ReadString('\n')
	username = strings.TrimSpace(username)
	fmt.Print("密码: ")
	pwBytes, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	if err != nil {
		return err
	}
	// POST /api/login，抓 relais_session cookie
	body, _ := json.Marshal(api.LoginRequest{Username: username, Password: string(pwBytes)})
	resp, err := http.Post(server+"/api/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("无法连接服务器: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("登录失败：用户名或密码错误")
	}
	var me api.Me
	json.NewDecoder(resp.Body).Decode(&me)
	if !me.IsAdmin {
		return fmt.Errorf("登录成功，但 %s 不是管理员，无法使用管理命令", me.Username)
	}
	var session string
	for _, c := range resp.Cookies() {
		if c.Name == "relais_session" {
			session = c.Value
		}
	}
	if session == "" {
		return fmt.Errorf("服务器未返回会话")
	}
	if err := saveAdminConfig(&AdminConfig{Server: server, Session: session}); err != nil {
		return err
	}
	fmt.Printf("管理员登录成功：%s @ %s\n", me.Username, server)
	return nil
}

func runAdminChannel(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais admin channel <create|list> ...")
	}
	c, err := newAdminClient()
	if err != nil {
		return err
	}
	switch args[0] {
	case "create":
		if len(args) != 2 {
			return fmt.Errorf("用法: relais admin channel create <频道名>")
		}
		var st api.ChannelStat
		if err := c.do("POST", "/api/admin/channels", api.AdminChannelRequest{Name: args[1]}, &st); err != nil {
			return err
		}
		fmt.Printf("频道 %s 已创建\n", st.Name)
		return nil
	case "list":
		var stats []api.ChannelStat
		if err := c.do("GET", "/api/admin/channels", nil, &stats); err != nil {
			return err
		}
		fmt.Printf("频道（共 %d 个）：\n", len(stats))
		for _, s := range stats {
			fmt.Printf("  %s（%d 人）\n", s.Name, s.Members)
		}
		return nil
	default:
		return fmt.Errorf("未知 channel 子命令 %q", args[0])
	}
}

func runAdminMember(args []string) error {
	if len(args) != 3 || (args[0] != "add" && args[0] != "remove") {
		return fmt.Errorf("用法: relais admin member <add|remove> <频道> <用户名>")
	}
	c, err := newAdminClient()
	if err != nil {
		return err
	}
	channel, username := args[1], args[2]
	if args[0] == "add" {
		if err := c.do("POST", "/api/admin/channels/"+channel+"/members", api.AdminMemberRequest{Username: username}, nil); err != nil {
			return err
		}
		fmt.Printf("%s 已加入频道 %s\n", username, channel)
	} else {
		if err := c.do("DELETE", "/api/admin/channels/"+channel+"/members/"+username, nil, nil); err != nil {
			return err
		}
		fmt.Printf("%s 已移出频道 %s\n", username, channel)
	}
	return nil
}

func runAdminInvite(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: relais admin invite <频道>")
	}
	c, err := newAdminClient()
	if err != nil {
		return err
	}
	var out map[string]string
	if err := c.do("POST", "/api/admin/channels/"+args[0]+"/invites", nil, &out); err != nil {
		return err
	}
	fmt.Printf("邀请链接（7 天内一次性有效）：\n  %s\n", out["url"])
	return nil
}
