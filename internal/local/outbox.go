package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/oklog/ulid/v2"
)

type PostOpts struct {
	Resolved   bool
	Owner      string
	Ack        bool
	NeedsHuman bool
}

// Post：agent 侧发信（spec §6.1）。只写文件，不联网。
func Post(mailDir, side, file string, o PostOpts) (string, error) {
	if side != "claude" && side != "codex" {
		return "", fmt.Errorf("侧只能是 claude 或 codex，得到 %q", side)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", fmt.Errorf("%s 是空的，信要有正文", file)
	}
	l := Letter{From: side, Kind: "letter", Summary: Summarize(string(data))}
	switch {
	case o.Ack && (o.Resolved || o.Owner != ""):
		return "", fmt.Errorf("--ack 不能与 --resolved/--owner 同用（附和时承接方取对方那封的）")
	case o.Ack && o.NeedsHuman, o.Resolved && o.NeedsHuman:
		return "", fmt.Errorf("--needs-human 不能与 --ack/--resolved 同用")
	case o.Resolved:
		if o.Owner != "claude" && o.Owner != "codex" && o.Owner != "user" {
			return "", fmt.Errorf("--resolved 必须带 --owner claude|codex|user")
		}
		l.Kind, l.Owner = "resolved", o.Owner
	case o.Ack:
		other := "codex"
		if side == "codex" {
			other = "claude"
		}
		letters, err := ListLetters(mailDir)
		if err != nil {
			return "", err
		}
		found := false
		for i := len(letters) - 1; i >= 0; i-- {
			if letters[i].From == other && letters[i].Kind == "resolved" {
				l.Kind, l.Owner, l.AckOf, found = "resolved", letters[i].Owner, letters[i].Seq, true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("%s 侧还没有 kind: resolved 的信可以附和（--ack）；要提议收敛请用 --resolved --owner", other)
		}
	case o.NeedsHuman:
		l.Kind = "needs-human"
	}
	name := fmt.Sprintf("%s-%s.md", side, ulid.Make().String())
	out := filepath.Join(mailDir, "outbox", name)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, RenderLetter(l, string(data)), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

type OutboxItem struct {
	Path, Key, From, Kind, Owner, Summary string
	AckOf                                 int
	Body                                  string
}

// OutboxError：ScanOutbox 读不了/解析不了的文件，携带文件名以便调用方精确改名（不必再从
// Error() 文本里 split 出文件名，见 M9 Task 7 修复轮 1 F3）。
type OutboxError struct {
	Name string
	Err  error
}

func (e OutboxError) Error() string { return e.Name + ": " + e.Err.Error() }

// ScanOutbox：守卫每轮调用；只看 *.md（.tmp 是 Post 写到一半的）。
func ScanOutbox(mailDir string) ([]OutboxItem, []OutboxError) {
	entries, err := os.ReadDir(filepath.Join(mailDir, "outbox"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []OutboxError{{Name: "outbox", Err: err}}
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var items []OutboxItem
	var errs []OutboxError
	for _, n := range names {
		p := filepath.Join(mailDir, "outbox", n)
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, OutboxError{Name: n, Err: err})
			continue
		}
		l, body, err := ParseLetter(data)
		if err != nil || (l.From != "claude" && l.From != "codex") {
			if err == nil {
				err = fmt.Errorf("发件人必须是 claude 或 codex")
			}
			errs = append(errs, OutboxError{Name: n, Err: err})
			continue
		}
		if l.Kind == "" {
			l.Kind = "letter"
		}
		items = append(items, OutboxItem{Path: p, Key: strings.TrimSuffix(n, ".md"), From: l.From, Kind: l.Kind, Owner: l.Owner, AckOf: l.AckOf, Summary: l.Summary, Body: body})
	}
	return items, errs
}
