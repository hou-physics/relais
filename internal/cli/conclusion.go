package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RunConclusion 打印本项目频道最新一份结论（relais/conclusions/<频道>-<id>.md）。
func RunConclusion(args []string) error {
	root, proj, err := findProject()
	if err != nil {
		return err
	}
	channel := proj.Channel
	if len(args) == 1 {
		channel = args[0]
	}
	dir := filepath.Join(root, "relais", "conclusions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("频道 %q 还没有结论（目录 %s 不存在）", channel, dir)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), channel+"-") && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return fmt.Errorf("频道 %q 还没有结论", channel)
	}
	sort.Strings(names) // ULID 时间有序，最后一个最新
	p := filepath.Join(dir, names[len(names)-1])
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fmt.Printf("结论文件: %s\n\n%s\n", p, data)
	return nil
}
