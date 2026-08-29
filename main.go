package main

import (
	"fmt"
	"os"

	"github.com/hou-physics/relais/internal/cli"
)

const version = "0.1.0-m1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "relais 错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: relais <serve|login|init|send|inbox|pull|members|agent-guide|user|channel|invite|version>")
	}
	switch args[0] {
	case "version":
		fmt.Println("relais", version)
		return nil
	case "serve":
		return cli.RunServe(args[1:])
	case "login":
		return cli.RunLogin(args[1:])
	case "init":
		return cli.RunInit(args[1:])
	case "send":
		return cli.RunSend(args[1:])
	case "inbox":
		return cli.RunInbox(args[1:])
	case "pull":
		return cli.RunPull(args[1:])
	case "members":
		return cli.RunMembers(args[1:])
	case "agent-guide":
		return cli.RunGuide(args[1:])
	case "user":
		return cli.RunUser(args[1:])
	case "channel":
		return cli.RunChannel(args[1:])
	case "invite":
		return cli.RunInvite(args[1:])
	default:
		return fmt.Errorf("未知子命令 %q（version 之外的命令在后续任务实现）", args[0])
	}
}
