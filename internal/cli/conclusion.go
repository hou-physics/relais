package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ulidLen 是 github.com/oklog/ulid 生成的 Crockford Base32 编码长度（无连字符）。
const ulidLen = 26

// latestConclusion 在 <root>/relais/conclusions/ 里找频道 channel 最新一份结论文件，返回其路径。
//
// 文件名必须严格匹配 `<channel>-<26 位 ULID>.md`：多模块共用一个项目目录时（D37），
// 子频道名形如 `duo-auth`，若只用前缀匹配，`relais conclusion`（频道 duo）会把
// `duo-auth-<id>.md` 也当成 duo 的结论，且因为 "duo-auth-..." 按字符串排序可能排在
// "duo-<id>.md" 之后而被误当作"最新"。用 ULID 定长校验排除这种跨频道串号。
func latestConclusion(root, channel string) (string, error) {
	dir := filepath.Join(root, "relais", "conclusions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("频道 %q 还没有结论（目录 %s 不存在）", channel, dir)
	}
	prefix := channel + "-"
	var names []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".md") {
			continue
		}
		rest := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".md")
		if len(rest) != ulidLen || strings.Contains(rest, "-") {
			continue // 属于其他子频道（如 duo-auth-<id>.md 之于 duo），跳过
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", fmt.Errorf("频道 %q 还没有结论", channel)
	}
	sort.Strings(names) // ULID 时间有序，最后一个最新
	return filepath.Join(dir, names[len(names)-1]), nil
}

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
	p, err := latestConclusion(root, channel)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	fmt.Printf("结论文件: %s\n\n%s\n", p, data)
	return nil
}
