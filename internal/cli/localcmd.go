package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/hou-physics/relais/internal/local"
)

// agent 侧三条命令（relais post / wait / attach）：纯文件操作，不联网、不碰 HTTP Client（spec §6）。

func resolveSide(as string) (string, error) {
	if as != "" {
		if as != "claude" && as != "codex" {
			return "", fmt.Errorf("--as 只能是 claude 或 codex，得到 %q", as)
		}
		return as, nil
	}
	return local.DetectSide(os.Getenv)
}

// resolveModule：从当前目录向上找含该模块信箱的项目目录；找不到时原样返回 FindModuleDir 的错误（它会列出已有模块）。
func resolveModule(name string) (string, string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	proj, err := local.FindModuleDir(wd, name)
	if err != nil {
		return "", "", err
	}
	return proj, local.MailDir(proj, name), nil
}

// parseInterspersed：允许标志与位置参数任意穿插（relais post m r.md --ack 与 relais post --ack m r.md 等价）。
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func RunPost(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	as := fs.String("as", "", "claude|codex（默认按环境判断）")
	resolved := fs.Bool("resolved", false, "提议收敛")
	owner := fs.String("owner", "", "承接方 claude|codex|user（配 --resolved）")
	ack := fs.Bool("ack", false, "附和对方最近一封收敛提议")
	needsHuman := fs.Bool("needs-human", false, "需要雇主定夺")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("用法: relais post <模块> <文件> [--resolved --owner claude|codex|user | --ack | --needs-human] [--as claude|codex]")
	}
	side, err := resolveSide(*as)
	if err != nil {
		return err
	}
	_, md, err := resolveModule(pos[0])
	if err != nil {
		return err
	}
	if _, err := local.Post(md, side, pos[1], local.PostOpts{Resolved: *resolved, Owner: *owner, Ack: *ack, NeedsHuman: *needsHuman}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已交给 relais（模块 %s，来自 %s）。\n", pos[0], side)
	if side == "claude" {
		fmt.Fprintf(stdout, "现在在后台运行：relais wait %s\n", pos[0])
	}
	return nil
}

func RunWait(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	as := fs.String("as", "", "claude|codex（默认按环境判断）")
	timeout := fs.Duration("timeout", 0, "最长等待，如 30m（默认一直等）")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("用法: relais wait <模块> [--timeout 30m] [--as claude|codex]")
	}
	side, err := resolveSide(*as)
	if err != nil {
		return err
	}
	_, md, err := resolveModule(pos[0])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	letters, err := local.Wait(ctx, md, side, time.Second, *timeout, os.Getenv("CLAUDE_CODE_SESSION_ID"))
	switch {
	case errors.Is(err, local.ErrWaitTimeout):
		fmt.Fprintf(stdout, "没等到新信，继续等请再运行一次：relais wait %s\n", pos[0])
		return nil
	case errors.Is(err, context.Canceled):
		return nil
	case err != nil:
		return err
	}
	all, err := local.ListLetters(md)
	if err != nil {
		return err
	}
	for _, l := range letters {
		data, err := os.ReadFile(l.Path)
		if err != nil {
			return err
		}
		_, body, err := local.ParseLetter(data)
		if err != nil {
			return fmt.Errorf("%s: %w", l.Path, err)
		}
		var prior []local.Letter
		for _, p := range all {
			if p.Seq < l.Seq {
				prior = append(prior, p)
			}
		}
		fmt.Fprintln(stdout, local.Describe(l, body, side, prior))
	}
	fmt.Fprintf(stdout, "处理完后再在后台运行：relais wait %s（否则收不到下一封）\n", pos[0])
	return nil
}

func RunAttach(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	as := fs.String("as", "", "claude|codex（默认按环境判断）")
	thread := fs.String("thread", "", "Codex 对话 id 或对话名（默认取项目目录最近活动的对话）")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("用法: relais attach <模块> [--thread <id 或对话名>] [--as claude|codex]")
	}
	side, err := resolveSide(*as)
	if err != nil {
		return err
	}
	proj, md, err := resolveModule(pos[0])
	if err != nil {
		return err
	}
	if side == "claude" {
		fmt.Fprintf(stdout, "Claude 侧的接入就是在后台运行：relais wait %s\n", pos[0])
		return nil
	}
	var th local.Thread
	if *thread == "" {
		th, err = local.FindCodexThread(codexHome(), proj)
	} else {
		th, err = local.CodexThreadByIDOrName(codexHome(), *thread)
	}
	if err != nil {
		return err
	}
	name := th.Name
	if name == "" {
		name = th.Title
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := local.WriteAttach(md, local.Attach{ThreadID: th.ID, Name: name, Cwd: th.Cwd, At: at}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已接入 Codex 对话「%s」（模块 %s）。来信会直接出现在这个对话里。\n", name, pos[0])
	return nil
}
