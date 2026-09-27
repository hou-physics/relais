// Package local 实现 Relais 本地模式「接入现有对话」（M9）：信箱布局、信封、agent 侧纯文件命令、守卫循环。
package local

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Letter struct {
	ID      string    `yaml:"id,omitempty"`
	Module  string    `yaml:"module"`
	Seq     int       `yaml:"seq"`
	From    string    `yaml:"from"`
	Date    time.Time `yaml:"date"`
	Kind    string    `yaml:"kind"`
	ReplyTo int       `yaml:"reply_to,omitempty"`
	Owner   string    `yaml:"owner,omitempty"`
	AckOf   int       `yaml:"ack_of,omitempty"`
	Summary string    `yaml:"summary"`
	Path    string    `yaml:"-"`
}

func RenderLetter(l Letter, body string) []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(l); err != nil {
		panic(err)
	}
	enc.Close()
	buf.WriteString("---\n\n")
	buf.WriteString(body)
	return buf.Bytes()
}

func ParseLetter(data []byte) (Letter, string, error) {
	var l Letter
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return l, "", fmt.Errorf("信件缺少 frontmatter 起始线")
	}
	rest := s[4:]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return l, "", fmt.Errorf("信件 frontmatter 未闭合")
	}
	if err := yaml.Unmarshal([]byte(rest[:idx]), &l); err != nil {
		return l, "", fmt.Errorf("信封解析失败: %w", err)
	}
	body := strings.TrimPrefix(rest[idx+5:], "\n")
	return l, body, nil
}

var letterNameRe = regexp.MustCompile(`^(\d{3,})-([a-z]+)\.md$`)

func LetterName(seq int, from string) string { return fmt.Sprintf("%03d-%s.md", seq, from) }
func ConclusionName(seq int) string          { return fmt.Sprintf("conclusion-%03d.md", seq) }
func KickoffName(seq int) string             { return fmt.Sprintf("kickoff-%03d.md", seq) }

func ParseLetterName(name string) (int, string, bool) {
	m := letterNameRe.FindStringSubmatch(name)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, "", false
	}
	return n, m[2], true
}

// Summarize：正文第一行非空文字，去掉 Markdown 标题井号与首尾空白，最多 80 字。
func Summarize(body string) string {
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if t == "" {
			continue
		}
		r := []rune(t)
		if len(r) > 80 {
			r = r[:80]
		}
		return string(r)
	}
	return ""
}
