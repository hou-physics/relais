package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

func MailDir(projectDir, module string) string {
	return filepath.Join(projectDir, "relais", "mail", module)
}

func EnsureMailDir(projectDir, module string) error {
	if !ValidModuleName(module) {
		return fmt.Errorf("模块名 %q 不合法", module)
	}
	for _, sub := range []string{"outbox", "drafts"} {
		if err := os.MkdirAll(filepath.Join(MailDir(projectDir, module), sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// ValidModuleName：模块名要当目录名用：非空、无首尾空白、不含斜杠与 ..、不以 . 开头
// （含 "."，也免得变成隐藏目录）、不含控制字符（终审修复 minor）。
func ValidModuleName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name || strings.HasPrefix(name, ".") || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func ListModulesIn(projectDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(projectDir, "relais", "mail"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidModuleName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// FindModuleDir 从 startDir 向上找含 relais/mail/<module>/ 的目录。
func FindModuleDir(startDir, module string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	var seen []string
	for {
		if st, err := os.Stat(MailDir(dir, module)); err == nil && st.IsDir() {
			return dir, nil
		}
		if mods, _ := ListModulesIn(dir); len(mods) > 0 && seen == nil {
			seen = mods
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if len(seen) > 0 {
		return "", fmt.Errorf("从 %s 向上没找到模块 %q；这里有的模块：%s", startDir, module, strings.Join(seen, "、"))
	}
	return "", fmt.Errorf("从 %s 向上没找到任何 relais/mail/ 目录；先在控制台新建模块", startDir)
}

// ListLetters：归档信（NNN-<from>.md）与 kickoff-NNN.md，按 seq 升序，同 seq 时 kickoff 在后。跳过 outbox/drafts/conclusion-*.md/标记文件。
func ListLetters(mailDir string) ([]Letter, error) {
	entries, err := os.ReadDir(mailDir)
	if err != nil {
		return nil, err
	}
	var out []Letter
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		_, _, isLetter := ParseLetterName(name)
		isKickoff := strings.HasPrefix(name, "kickoff-") && strings.HasSuffix(name, ".md")
		if !isLetter && !isKickoff {
			continue
		}
		data, err := os.ReadFile(filepath.Join(mailDir, name))
		if err != nil {
			return nil, err
		}
		l, _, err := ParseLetter(data)
		if err != nil {
			continue // 坏文件不挡路，控制台会另行提示
		}
		l.Path = filepath.Join(mailDir, name)
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Kind != "kickoff" && out[j].Kind == "kickoff"
	})
	return out, nil
}
