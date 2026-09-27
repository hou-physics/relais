package local

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func fakeCodexHome(t *testing.T, proj string) string {
	t.Helper()
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, title TEXT NOT NULL, name TEXT, archived INTEGER NOT NULL DEFAULT 0, updated_at_ms INTEGER)`); err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		id, cwd, title, name string
		archived             int
		ms                   int64
	}{
		{"t-old", proj, "旧对话", "", 0, 1000},
		{"t-new", proj, "新对话很长的标题" + string(make([]rune, 70)), "巡天主对话", 0, 3000},
		{"t-arch", proj, "已归档", "", 1, 5000},
		{"t-other", "/elsewhere", "别的项目", "", 0, 9000},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO threads VALUES (?,?,?,?,?,?)`, r.id, r.cwd, r.title, r.name, r.archived, r.ms); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(home, "state_4.sqlite"), []byte("stale"), 0o644)
	return home
}

func TestFindCodexThreadPicksLatestInDir(t *testing.T) {
	proj := t.TempDir()
	home := fakeCodexHome(t, proj)
	th, err := FindCodexThread(home, proj)
	if err != nil || th.ID != "t-new" || th.Name != "巡天主对话" {
		t.Fatalf("应选 t-new: %+v %v", th, err)
	}
	ls, _ := ListCodexThreads(home, proj)
	if len(ls) != 2 || ls[0].ID != "t-new" || ls[1].ID != "t-old" {
		t.Fatalf("列表应按时间降序且排除归档/别目录: %+v", ls)
	}
	if _, err := FindCodexThread(home, "/nowhere"); err == nil {
		t.Fatal("没有匹配应报错")
	}
	// /private 前缀等价
	if ls, _ := ListCodexThreads(home, "/private"+proj); len(ls) != 2 {
		t.Fatalf("/private 前缀应等价: %d", len(ls))
	}
}

func TestCodexStateDBAndSchemaError(t *testing.T) {
	home := t.TempDir()
	if _, err := CodexStateDB(home); err == nil {
		t.Fatal("没有 state_*.sqlite 应报错")
	}
	os.WriteFile(filepath.Join(home, "state_3.sqlite"), nil, 0o644)
	os.WriteFile(filepath.Join(home, "state_12.sqlite"), nil, 0o644)
	p, _ := CodexStateDB(home)
	if filepath.Base(p) != "state_12.sqlite" {
		t.Fatalf("应取数字最大的: %s", p)
	}
	db, _ := sql.Open("sqlite", filepath.Join(home, "state_12.sqlite"))
	db.Exec(`CREATE TABLE threads (id TEXT)`)
	db.Close()
	if _, err := ListCodexThreads(home, ""); err == nil {
		t.Fatal("表缺列应报明确错误")
	}
}

func TestListClaudeSessions(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	dir := filepath.Join(home, ".claude", "sessions")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "10.json"), []byte(`{"pid":10,"sessionId":"s1","cwd":"`+proj+`","kind":"interactive","name":"主对话","status":"idle","updatedAt":1790535000000}`), 0o644)
	os.WriteFile(filepath.Join(dir, "11.json"), []byte(`{"pid":11,"sessionId":"s2","cwd":"/elsewhere","kind":"interactive","name":"别的"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "12.json"), []byte(`{"pid":12,"sessionId":"s3","cwd":"`+proj+`","kind":"background","name":"后台"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.json"), []byte(`{`), 0o644)
	ss, err := ListClaudeSessions(home, proj)
	if err != nil || len(ss) != 1 || ss[0].ID != "s1" || ss[0].Name != "主对话" || ss[0].Status != "idle" || ss[0].PID != 10 {
		t.Fatalf("ListClaudeSessions: %+v %v", ss, err)
	}
	if ss[0].UpdatedAt.Year() != 2026 {
		t.Fatalf("updatedAt 毫秒应转时间: %v", ss[0].UpdatedAt)
	}
}

func TestAttachFileRoundTrip(t *testing.T) {
	_, md := setupMail(t)
	if _, ok := ReadAttach(md); ok {
		t.Fatal("初始无接入")
	}
	a := Attach{ThreadID: "t1", Name: "名", Cwd: "/p", At: time.Now().UTC().Truncate(time.Second)}
	if err := WriteAttach(md, a); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadAttach(md)
	if !ok || got.ThreadID != "t1" || got.Name != "名" || !got.At.Equal(a.At) {
		t.Fatalf("round trip: %+v", got)
	}
}

func TestCodexThreadByIDOrName(t *testing.T) {
	proj := t.TempDir()
	home := fakeCodexHome(t, proj)
	th, err := CodexThreadByIDOrName(home, "t-old")
	if err != nil || th.ID != "t-old" {
		t.Fatalf("应按 id 找到 t-old: %+v %v", th, err)
	}
	th, err = CodexThreadByIDOrName(home, "巡天主对话")
	if err != nil || th.ID != "t-new" {
		t.Fatalf("应按对话名找到 t-new: %+v %v", th, err)
	}
	if _, err := CodexThreadByIDOrName(home, "nope"); err == nil {
		t.Fatal("找不到应报明确错误")
	}
	if _, err := CodexThreadByIDOrName(home, "t-arch"); err == nil {
		t.Fatal("已归档的不应被找到")
	}
}
