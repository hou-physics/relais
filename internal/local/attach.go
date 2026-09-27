package local

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Thread struct {
	ID, Name, Title, Cwd string
	UpdatedAt            time.Time
}

type Session struct {
	ID, Name, Cwd, Status string
	PID                   int
	UpdatedAt             time.Time
}

type Attach struct {
	ThreadID string    `json:"thread_id"`
	Name     string    `json:"name"`
	Cwd      string    `json:"cwd"`
	At       time.Time `json:"at"`
}

func SamePath(a, b string) bool {
	norm := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		return strings.TrimPrefix(p, "/private")
	}
	return norm(a) == norm(b)
}

var stateDBRe = regexp.MustCompile(`^state_(\d+)\.sqlite$`)

func CodexStateDB(codexHome string) (string, error) {
	entries, err := os.ReadDir(codexHome)
	if err != nil {
		return "", fmt.Errorf("读不到 Codex 目录 %s: %w", codexHome, err)
	}
	best, bestN := "", -1
	for _, e := range entries {
		if m := stateDBRe.FindStringSubmatch(e.Name()); m != nil {
			if n, _ := strconv.Atoi(m[1]); n > bestN {
				best, bestN = e.Name(), n
			}
		}
	}
	if best == "" {
		return "", fmt.Errorf("%s 下没有 state_*.sqlite（Codex 桌面版还没建过对话？）", codexHome)
	}
	return filepath.Join(codexHome, best), nil
}

func openCodexStateDB(codexHome string) (*sql.DB, error) {
	p, err := CodexStateDB(codexHome)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return nil, err
	}
	return db, nil
}

func ListCodexThreads(codexHome, dir string) ([]Thread, error) {
	db, err := openCodexStateDB(codexHome)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, COALESCE(name,''), title, cwd, COALESCE(updated_at_ms,0) FROM threads WHERE archived=0 ORDER BY updated_at_ms DESC`)
	if err != nil {
		return nil, fmt.Errorf("Codex 线程表结构不是预期的（%v）；请在控制台里手动选对话或用 --thread", err)
	}
	defer rows.Close()
	var out []Thread
	for rows.Next() {
		var t Thread
		var ms int64
		if err := rows.Scan(&t.ID, &t.Name, &t.Title, &t.Cwd, &ms); err != nil {
			return nil, err
		}
		if dir != "" && !SamePath(t.Cwd, dir) {
			continue
		}
		t.UpdatedAt = time.UnixMilli(ms)
		if r := []rune(t.Title); len(r) > 60 {
			t.Title = string(r[:60])
		}
		out = append(out, t)
		if len(out) == 20 {
			break
		}
	}
	return out, rows.Err()
}

func FindCodexThread(codexHome, dir string) (Thread, error) {
	ls, err := ListCodexThreads(codexHome, dir)
	if err != nil {
		return Thread{}, err
	}
	if len(ls) == 0 {
		return Thread{}, fmt.Errorf("Codex 里没有工作目录为 %s 的对话；请在控制台里手动选，或用 --thread <id 或对话名>", dir)
	}
	return ls[0], nil
}

// CodexThreadByIDOrName 按 id 或精确对话名取一条（不限 20 条、不限目录）；找不到返回明确错误。
func CodexThreadByIDOrName(codexHome, key string) (Thread, error) {
	db, err := openCodexStateDB(codexHome)
	if err != nil {
		return Thread{}, err
	}
	defer db.Close()
	row := db.QueryRow(`SELECT id, COALESCE(name,''), title, cwd, COALESCE(updated_at_ms,0) FROM threads WHERE archived=0 AND (id=? OR name=?) ORDER BY updated_at_ms DESC LIMIT 1`, key, key)
	var t Thread
	var ms int64
	if err := row.Scan(&t.ID, &t.Name, &t.Title, &t.Cwd, &ms); err != nil {
		return Thread{}, fmt.Errorf("Codex 里没有 id 或对话名为 %q 的未归档对话: %w", key, err)
	}
	t.UpdatedAt = time.UnixMilli(ms)
	if r := []rune(t.Title); len(r) > 60 {
		t.Title = string(r[:60])
	}
	return t, nil
}

func ListClaudeSessions(home, dir string) ([]Session, error) {
	entries, err := os.ReadDir(filepath.Join(home, ".claude", "sessions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(home, ".claude", "sessions", e.Name()))
		if err != nil {
			continue
		}
		var raw struct {
			PID       int    `json:"pid"`
			SessionID string `json:"sessionId"`
			Cwd       string `json:"cwd"`
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Status    string `json:"status"`
			UpdatedAt int64  `json:"updatedAt"`
		}
		if json.Unmarshal(data, &raw) != nil || raw.Kind != "interactive" {
			continue
		}
		if dir != "" && !SamePath(raw.Cwd, dir) {
			continue
		}
		out = append(out, Session{ID: raw.SessionID, Name: raw.Name, Cwd: raw.Cwd, Status: raw.Status, PID: raw.PID, UpdatedAt: time.UnixMilli(raw.UpdatedAt)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func attachPath(mailDir string) string { return filepath.Join(mailDir, ".attach-codex") }

func WriteAttach(mailDir string, a Attach) error {
	data, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(attachPath(mailDir), data, 0o644)
}

func ReadAttach(mailDir string) (Attach, bool) {
	var a Attach
	data, err := os.ReadFile(attachPath(mailDir))
	if err != nil || json.Unmarshal(data, &a) != nil || a.ThreadID == "" {
		return a, false
	}
	return a, true
}
