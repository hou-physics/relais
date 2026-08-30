package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStaticPages(t *testing.T) {
	ts, st, users := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Relais") {
		t.Fatalf("首页应含 Relais: %d", resp.StatusCode)
	}
	for _, want := range []string{"复制 AI 格式模板", "id=\"drafts\"", "id=\"settings-view\"", "id=\"lang-select\"", "id=\"user-menu\""} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("首页缺少 %q", want)
		}
	}
	if !strings.Contains(string(body), "data-i18n") {
		t.Fatal("首页应含 data-i18n")
	}
	resp, _ = http.Get(ts.URL + "/app.js")
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "const I18N") {
		t.Fatalf("/app.js 应含 const I18N: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Deutsch") {
		t.Fatal("/app.js 应含 Deutsch")
	}
	ch, _ := st.ChannelByName("deutschapp")
	code, _ := st.CreateInvite(ch.ID, users["hou"].ID, time.Hour)
	resp, _ = http.Get(ts.URL + "/join/" + code)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "邀请") {
		t.Fatalf("join 页应渲染向导: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "不用命令行") {
		t.Fatal("join 页缺少无 CLI 分支")
	}
	resp, _ = http.Get(ts.URL + "/vendor/marked.min.js")
	if resp.StatusCode != 200 {
		t.Fatalf("vendor 资源应可达: %d", resp.StatusCode)
	}
}
