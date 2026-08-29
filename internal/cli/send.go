package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/msg"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func RunSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	var to multiFlag
	fs.Var(&to, "to", "收件人用户名（可多次）")
	all := fs.Bool("all", false, "发给频道内除自己外的全部成员")
	summary := fs.String("summary", "", "给人看的摘要（必填）")
	reply := fs.String("reply", "", "回复的消息 id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *summary == "" {
		return fmt.Errorf("--summary 必填：给人看的一两句话")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("用法: relais send [--to 用户名]... [--all] --summary \"...\" <正文文件|->")
	}
	root, proj, err := findProject()
	if err != nil {
		return err
	}
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	// 读正文（文件或 stdin）
	src := fs.Arg(0)
	var body []byte
	fromStdin := src == "-"
	if fromStdin {
		if body, err = io.ReadAll(os.Stdin); err != nil {
			return err
		}
	} else {
		if body, err = os.ReadFile(src); err != nil {
			return fmt.Errorf("读取正文文件失败: %w", err)
		}
	}
	// Draft protection closure: wraps any subsequent error to save stdin if needed
	failWithDraft := func(err error) error {
		if !fromStdin {
			return err
		}
		draft := filepath.Join(root, "relais", "drafts",
			time.Now().UTC().Format("20060102-150405")+".md")
		if werr := os.WriteFile(draft, body, 0o644); werr == nil {
			return fmt.Errorf("%w（正文已保存到 %s）", err, draft)
		}
		return err
	}
	// 解析收件人（spec §7 默认收件人规则）
	members, err := c.Members(proj.Channel)
	if err != nil {
		return failWithDraft(err)
	}
	var others []string
	for _, m := range members {
		if m.Username != cfg.Username {
			others = append(others, m.Username)
		}
	}
	recipients := []string(to)
	if *all {
		recipients = others
	}
	if len(recipients) == 0 {
		if len(others) == 1 {
			recipients = others // 双人频道：默认对方
		} else {
			return fmt.Errorf("频道 %q 有 %d 名成员，请用 --to 指定收件人（成员：%s）或 --all 发全体",
				proj.Channel, len(members), strings.Join(others, ", "))
		}
	}
	sent, err := c.Send(proj.Channel, api.SendRequest{
		To: recipients, Summary: *summary, Body: string(body), InReplyTo: *reply,
	})
	if err != nil {
		return failWithDraft(fmt.Errorf("发送失败: %w", err))
	}
	// sent/ 副本（本地快照；事实源在服务器）
	copyPath := filepath.Join(root, "relais", "sent", localName(sent.ID, sent.CreatedAt, cfg.Username))
	env := msg.Envelope{ID: sent.ID, Channel: sent.Channel, From: sent.From, To: sent.To,
		InReplyTo: sent.InReplyTo, Sent: sent.CreatedAt, Summary: sent.Summary}
	if werr := os.WriteFile(copyPath, msg.Render(env, string(body)), 0o644); werr != nil {
		fmt.Printf("已发送 → %s（频道 %s，id %s）\n", strings.Join(sent.To, ", "), sent.Channel, sent.ID)
		fmt.Printf("警告：本地副本写入失败: %v\n", werr)
	} else {
		fmt.Printf("已发送 → %s（频道 %s，id %s）\n本地副本: %s\n",
			strings.Join(sent.To, ", "), sent.Channel, sent.ID, copyPath)
	}
	return nil
}

// localName: 短且可排序的落盘文件名（ULID 前 10 位含时间戳排序性）。
func localName(id string, at time.Time, who string) string {
	short := id
	if len(short) > 10 {
		short = short[:10]
	}
	return fmt.Sprintf("%s-%s-%s.md", at.UTC().Format("20060102"), who, short)
}
