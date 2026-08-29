package cli

import (
	"flag"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func RunLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	token := fs.String("token", "", "你的 agent token（邀请页上有）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 || *token == "" {
		return fmt.Errorf("用法: relais login <服务器地址> --token <token>")
	}
	server := strings.TrimRight(fs.Arg(0), "/")
	c := &Client{Server: server, Token: *token, hc: &http.Client{Timeout: 30 * time.Second}}
	me, err := c.Me()
	if err != nil {
		return fmt.Errorf("登录验证失败: %w", err)
	}
	if err := saveGlobal(&GlobalConfig{Server: server, Token: *token, Username: me.Username}); err != nil {
		return err
	}
	fmt.Printf("登录成功：%s @ %s\n下一步：进入项目文件夹运行 relais init <频道名>\n", me.Username, server)
	return nil
}
