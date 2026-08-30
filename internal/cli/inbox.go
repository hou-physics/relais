package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/msg"
)

func RunInbox(_ []string) error {
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	c, _, err := newClient()
	if err != nil {
		return err
	}
	unread, err := c.Envelopes(proj.Channel, true)
	if err != nil {
		return err
	}
	if len(unread) == 0 {
		fmt.Println("收件箱没有未读消息。")
		return nil
	}
	fmt.Printf("未读 %d 条（relais pull 拉全部，relais pull <编号> 拉单条）：\n", len(unread))
	for i, m := range unread {
		fmt.Printf("[%d] %s · %s · %s (id %s)\n",
			i+1, m.From, m.CreatedAt.Local().Format("2006-01-02 15:04"), m.Summary, m.ID)
	}
	return nil
}

func pullOne(c *Client, root string, envMsg api.Message) (string, error) {
	full, err := c.Message(envMsg.ID)
	if err != nil {
		return "", fmt.Errorf("拉取 %s 失败: %w", envMsg.ID, err)
	}
	env := msg.Envelope{ID: full.ID, Channel: full.Channel, From: full.From, To: full.To,
		InReplyTo: full.InReplyTo, Sent: full.CreatedAt, Summary: full.Summary}
	// 使用完整 ULID 而非前 10 字符以避免碰撞
	filename := fmt.Sprintf("%s-%s-%s.md", full.CreatedAt.UTC().Format("20060102"), full.From, full.ID)
	inboxDir := filepath.Join(root, "relais", "inbox")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(inboxDir, filename)
	if err := os.WriteFile(path, msg.Render(env, full.Body), 0o644); err != nil {
		return "", err
	}
	if err := c.MarkRead(full.ID); err != nil {
		return "", err
	}
	return path, nil
}

func RunPull(args []string) error {
	root, proj, err := findProject()
	if err != nil {
		return err
	}
	c, _, err := newClient()
	if err != nil {
		return err
	}
	unread, err := c.Envelopes(proj.Channel, true)
	if err != nil {
		return err
	}
	if len(unread) == 0 {
		fmt.Println("没有未读消息可拉取。")
		return nil
	}
	targets := unread
	if len(args) == 1 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > len(unread) {
			return fmt.Errorf("编号 %q 无效：当前未读为 1..%d（先跑 relais inbox 查看）", args[0], len(unread))
		}
		targets = unread[n-1 : n]
	} else if len(args) > 1 {
		return fmt.Errorf("用法: relais pull [编号]")
	}
	for _, envMsg := range targets {
		path, err := pullOne(c, root, envMsg)
		if err != nil {
			return err
		}
		fmt.Printf("已拉取: %s\n  来自 %s · %s\n", path, envMsg.From, envMsg.Summary)
	}
	return nil
}

func RunMembers(_ []string) error {
	_, proj, err := findProject()
	if err != nil {
		return err
	}
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	members, err := c.Members(proj.Channel)
	if err != nil {
		return err
	}
	fmt.Printf("频道 %q 成员 %d 人：\n", proj.Channel, len(members))
	for _, m := range members {
		self := ""
		if m.Username == cfg.Username {
			self = "（你）"
		}
		fmt.Printf("  %s · %s%s\n", m.Username, m.DisplayName, self)
	}
	return nil
}
