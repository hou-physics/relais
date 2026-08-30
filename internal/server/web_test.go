package server

import (
	"io"
	"net/http"
	"os"
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
	for _, want := range []string{"复制 AI 格式模板", "id=\"drafts\"", "id=\"settings-view\"", "id=\"lang-select\"", "id=\"user-menu\"", "id=\"admin-view\"", "id=\"menu-admin\""} {
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
	// 静态断言：双向协议标志句和 Windows PATH 标志
	resp, _ = http.Get(ts.URL + "/app.js")
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "你必须输出成下面这个格式") {
		t.Fatal("/app.js 缺少双向协议标志句（「你必须输出成下面这个格式」）")
	}
	resp, _ = http.Get(ts.URL + "/join/" + code)
	body, _ = io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "relais-bin") {
		t.Fatal("join 页缺少 Windows PATH 标志（「relais-bin」）")
	}
}

// TestAITemplateStaysInSyncAcrossFiles 钉住 app.js 和 join.html 里各自内嵌的
// "AI 生成消息" 格式模板，防止两份重复拷贝悄悄跑偏（发消息主页 vs. 邀请加入页
// 各自维护一份同样的模板文本）。
func TestAITemplateStaysInSyncAcrossFiles(t *testing.T) {
	const marker = "summary: <一两句话摘要，给人快速浏览>"
	appJS, err := os.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	joinHTML, err := os.ReadFile("web/join.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(appJS), marker) {
		t.Fatalf("web/app.js 缺少 AI 模板标记行: %q", marker)
	}
	if !strings.Contains(string(joinHTML), marker) {
		t.Fatalf("web/join.html 缺少 AI 模板标记行: %q", marker)
	}
}
