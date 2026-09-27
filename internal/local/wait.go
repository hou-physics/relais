package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ErrWaitTimeout = errors.New("没等到新信")

type WaitMarker struct {
	PID       int       `json:"pid"`
	Since     time.Time `json:"since"`
	SessionID string    `json:"session_id,omitempty"`
}

type Cursor struct {
	Seq     int    `json:"seq"`
	Kickoff string `json:"kickoff,omitempty"`
}

func cursorPath(mailDir, side string) string { return filepath.Join(mailDir, ".cursor-"+side) }
func waitPath(mailDir, side string) string   { return filepath.Join(mailDir, ".wait-"+side) }

func ReadCursor(mailDir, side string) Cursor {
	var c Cursor
	if data, err := os.ReadFile(cursorPath(mailDir, side)); err == nil {
		_ = json.Unmarshal(data, &c)
	}
	return c
}

func WriteCursor(mailDir, side string, c Cursor) error {
	data, _ := json.Marshal(c)
	return os.WriteFile(cursorPath(mailDir, side), data, 0o644)
}

func ReadWaitMarker(mailDir, side string) (WaitMarker, bool) {
	var m WaitMarker
	data, err := os.ReadFile(waitPath(mailDir, side))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return m, false
	}
	return m, true
}

var kickoffNameRe = regexp.MustCompile(`^kickoff-(\d+)\.md$`)

// kickoffSeq 解析 kickoff-<digits>.md 里的序号；解析不出（含空字符串）按 0 处理。
func kickoffSeq(name string) (int, bool) {
	m := kickoffNameRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return n, true
}

// NewLetters：side 没见过的信（seq > 游标，或游标之后出现的 kickoff 文件），不含 side 自己写的。
func NewLetters(mailDir, side string, c Cursor) ([]Letter, Cursor, error) {
	all, err := ListLetters(mailDir)
	if err != nil {
		return nil, c, err
	}
	next := c
	var out []Letter
	for _, l := range all {
		name := filepath.Base(l.Path)
		if l.Kind == "kickoff" {
			seen, _ := kickoffSeq(next.Kickoff)
			cur, _ := kickoffSeq(name)
			if cur > seen {
				out = append(out, l)
				next.Kickoff = name
			}
			continue
		}
		if l.Seq <= c.Seq {
			continue
		}
		if l.Seq > next.Seq {
			next.Seq = l.Seq
		}
		if l.From != side {
			out = append(out, l)
		}
	}
	return out, next, nil
}

func Wait(ctx context.Context, mailDir, side string, poll, timeout time.Duration, sessionID string) ([]Letter, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	marker, _ := json.Marshal(WaitMarker{PID: os.Getpid(), Since: time.Now(), SessionID: sessionID})
	if err := os.WriteFile(waitPath(mailDir, side), marker, 0o644); err != nil {
		return nil, err
	}
	defer os.Remove(waitPath(mailDir, side))
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	for {
		ls, next, err := NewLetters(mailDir, side, ReadCursor(mailDir, side))
		if err != nil {
			return nil, err
		}
		if len(ls) > 0 {
			return ls, WriteCursor(mailDir, side, next)
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil, ErrWaitTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

var firstResponderRe = regexp.MustCompile(`^@(claude|codex)\b`)

func FirstResponder(human Letter, humanBody string, letters []Letter) string {
	first := strings.TrimSpace(strings.SplitN(strings.TrimLeft(humanBody, "\n"), "\n", 2)[0])
	if m := firstResponderRe.FindStringSubmatch(first); m != nil {
		return m[1]
	}
	for i := len(letters) - 1; i >= 0; i-- {
		if letters[i].Seq >= human.Seq && human.Seq > 0 {
			continue
		}
		switch letters[i].From {
		case "claude":
			return "codex"
		case "codex":
			return "claude"
		}
	}
	return "claude"
}

// Describe：wait 输出与 codex 投递通知共用的一句话（不含正文）。
func Describe(l Letter, body, side string, prior []Letter) string {
	switch l.Kind {
	case "kickoff":
		conc := filepath.Join(filepath.Dir(l.Path), ConclusionName(l.Seq))
		switch l.Owner {
		case side:
			return fmt.Sprintf("已握手，你是承接方：读 %s 开工", conc)
		case "user":
			return fmt.Sprintf("已握手，承接方是雇主，本侧不用开工（结论在 %s）", conc)
		default:
			return fmt.Sprintf("已握手，承接方是 %s，本侧不用开工（结论在 %s）", l.Owner, conc)
		}
	}
	line := fmt.Sprintf("第 %d 封 来自 %s：%s", l.Seq, l.From, l.Path)
	switch {
	case l.Kind == "conclusion":
		return line + "\n这是对方的附和，已握手；不用回信，等开工通知"
	case l.Kind == "needs-human" && l.From != "hou":
		return line + "\n对方在等雇主定夺；不用回信"
	}
	if l.From == "hou" {
		if FirstResponder(l, body, prior) == side {
			return line + "\n雇主的信，由你先回"
		}
		return line + fmt.Sprintf("\n雇主的信，由 %s 先回，本侧不用回", FirstResponder(l, body, prior))
	}
	return line
}
