package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/msg"
	"github.com/hou-physics/relais/internal/store"
)

func seedIncoming(t *testing.T, st *store.Store, users map[string]*store.User, channel string, n int) {
	t.Helper()
	ch, _ := st.ChannelByName(channel)
	for i := 0; i < n; i++ {
		if _, err := st.SaveMessage(ch.ID, users["wu"].ID, []int64{users["hou"].ID},
			"摘要-"+string(rune('A'+i)), "# 正文\n内容-"+string(rune('A'+i)), ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInboxAndPullAll(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	seedIncoming(t, st, users, "duo", 2)
	if err := RunInbox(nil); err != nil {
		t.Fatal(err)
	}
	if err := RunPull(nil); err != nil { // 无参数 = 拉全部未读
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(proj, "relais", "inbox"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("inbox/ 应落 2 个文件: %v %v", entries, err)
	}
	// 文件可被 msg.Parse 解析且含正文
	data, _ := os.ReadFile(filepath.Join(proj, "relais", "inbox", entries[0].Name()))
	env, body, err := msg.Parse(data)
	if err != nil || env.From != "wu" || !strings.Contains(body, "内容-") {
		t.Fatalf("落盘文件解析失败: %v %+v", err, env)
	}
	// 已读：再 pull 应无新文件
	if err := RunPull(nil); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(filepath.Join(proj, "relais", "inbox"))
	if len(entries) != 2 {
		t.Fatalf("重复 pull 不应重复落盘: %d", len(entries))
	}
}

func TestPullByIndex(t *testing.T) {
	st, users, proj := setupCLITest(t, "hou", "duo")
	seedIncoming(t, st, users, "duo", 2)
	if err := RunPull([]string{"2"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(proj, "relais", "inbox"))
	if len(entries) != 1 {
		t.Fatalf("按编号 pull 应只落 1 个文件: %v", entries)
	}
	if err := RunPull([]string{"99"}); err == nil {
		t.Fatal("越界编号应报错")
	}
}

func TestMembers(t *testing.T) {
	_, _, _ = setupCLITest(t, "hou", "trio")
	if err := RunMembers(nil); err != nil {
		t.Fatal(err)
	}
}
