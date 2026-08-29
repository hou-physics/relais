package main

import (
	"fmt"
	"os"
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
	default:
		return fmt.Errorf("未知子命令 %q（version 之外的命令在后续任务实现）", args[0])
	}
}
