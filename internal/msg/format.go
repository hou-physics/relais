// Package msg 实现 Relais 消息的落盘格式：YAML frontmatter 信封 + Markdown 正文（spec §6）。
package msg

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Envelope struct {
	ID          string    `yaml:"id"`
	Channel     string    `yaml:"channel"`
	From        string    `yaml:"from"`
	To          []string  `yaml:"to"`
	InReplyTo   string    `yaml:"in_reply_to,omitempty"`
	Sent        time.Time `yaml:"sent"`
	Summary     string    `yaml:"summary"`
	Seq         int       `yaml:"seq,omitempty"`
	Round       int       `yaml:"round,omitempty"`
	Kind        string    `yaml:"kind,omitempty"`
	Owner       string    `yaml:"owner,omitempty"`
	OwnerReason string    `yaml:"owner_reason,omitempty"`
	AckOf       string    `yaml:"ack_of,omitempty"`
}

func Render(env Envelope, body string) []byte {
	var buf bytes.Buffer
	buf.WriteString("---\n")
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(env); err != nil {
		panic(err) // 结构体固定，编码不可能失败
	}
	enc.Close()
	buf.WriteString("---\n\n")
	buf.WriteString(body)
	return buf.Bytes()
}

func Parse(data []byte) (Envelope, string, error) {
	var env Envelope
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return env, "", fmt.Errorf("消息文件缺少 frontmatter 起始线")
	}
	rest := s[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return env, "", fmt.Errorf("消息文件 frontmatter 未闭合")
	}
	if err := yaml.Unmarshal([]byte(rest[:idx]), &env); err != nil {
		return env, "", fmt.Errorf("frontmatter 解析失败: %w", err)
	}
	body := rest[idx+len("\n---\n"):]
	body = strings.TrimPrefix(body, "\n")
	return env, body, nil
}

// ExtractSummary 尝试从带 frontmatter 的 Markdown 中取出摘要与纯正文。
// 解析失败或无 summary 时按纯文本对待（ok=false，body=原文），永不报错。
func ExtractSummary(data []byte) (string, string, bool) {
	env, body, err := Parse(data)
	if err != nil || strings.TrimSpace(env.Summary) == "" {
		return "", string(data), false
	}
	return env.Summary, body, true
}

// Header 是 agent 手写 frontmatter 时用到的字段子集（relais send 从中取值）。
type Header struct {
	Summary     string `yaml:"summary"`
	Kind        string `yaml:"kind"`
	Owner       string `yaml:"owner"`
	OwnerReason string `yaml:"owner_reason"`
	AckOf       string `yaml:"ack_of"`
}

// ExtractHeader 宽松解析 agent 写的 frontmatter（只认这几个键，其余忽略）；无 frontmatter 时 ok=false 且 body=原文。
func ExtractHeader(data []byte) (Header, string, bool) {
	var h Header
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return h, s, false
	}
	rest := s[len("---\n"):]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return h, s, false
	}
	if err := yaml.Unmarshal([]byte(rest[:idx]), &h); err != nil {
		return h, s, false
	}
	body := strings.TrimPrefix(rest[idx+len("\n---\n"):], "\n")
	return h, body, true
}
