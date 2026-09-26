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
	for _, want := range []string{"复制 ChatGPT 协议模板", "id=\"drafts\"", "id=\"settings-view\"", "id=\"lang-select\"", "id=\"user-menu\"", "id=\"admin-view\"", "id=\"menu-admin\"", "id=\"auto-bar\"", "id=\"auto-on\"", "id=\"auto-off\"", "id=\"auto-cap\""} {
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
	if !strings.Contains(string(body), "loadAutoState") {
		t.Fatal("/app.js 应含 loadAutoState")
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

// TestI18NKeysForAdminView 检查管理界面所需的 i18n 键存在。
func TestI18NKeysForAdminView(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/app.js")
	body, _ := io.ReadAll(resp.Body)
	appJS := string(body)

	// 检查三语键存在（zh/en/de 各一份）
	keys := []string{"channelAdmin", "createChannel", "newChannelPh", "members",
		"addMember", "addMemberPh", "genInvite", "remove"}

	for _, key := range keys {
		if !strings.Contains(appJS, key+":") {
			t.Fatalf("I18N 缺少键: %q", key)
		}
	}

	// 检查 localeFor 函数存在
	if !strings.Contains(appJS, "function localeFor") {
		t.Fatal("/app.js 缺少 localeFor 函数")
	}
}

// TestWebHasLocalModeControls 检查本地单人模式所需的控件、三语文案键与端点调用都已接入。
func TestWebHasLocalModeControls(t *testing.T) {
	html, _ := webFS.ReadFile("web/index.html")
	for _, id := range []string{`id="auto-mode"`, `id="auto-kickoff"`, `id="auto-reopen"`, `id="auto-answer"`, `id="auto-answer-send"`} {
		if !strings.Contains(string(html), id) {
			t.Fatalf("index.html 缺 %s", id)
		}
	}
	js, _ := webFS.ReadFile("web/app.js")
	for _, key := range []string{"autoResolved", "kickoff", "reopen", "modeSupervised", "modeAutopilot", "conclusionTag", "kickoffTag", "answerPh", "answerSend", "autoKickedOff", "autoClosed", "reopenReason"} {
		if strings.Count(string(js), key+":") < 3 {
			t.Fatalf("app.js 三语文案缺 %s（需 zh/en/de 各一）", key)
		}
	}
	for _, s := range []string{"/auto/kickoff", "/auto/reopen", "/auto/mode", `"conclusion"`, `"kickoff"`, "round_cap"} {
		if !strings.Contains(string(js), s) {
			t.Fatalf("app.js 缺 %s", s)
		}
	}
	// 人的操作（开关/模式/暂停继续/开工/回答等）失败时必须给可见提示并刷新状态，
	// 而不是让 api() 的 throw 变成静默的 unhandled rejection（按钮看起来像失灵）。
	if strings.Count(string(js), "humanAction(") < 8 {
		t.Fatal("app.js 里人操作的按钮监听缺少 humanAction 包裹（应至少出现 8 次：定义 + 各按钮调用）")
	}
	// 开工提示须带频道名：多模块项目里裸 relais conclusion 会打印错的结论
	if strings.Count(string(js), "relais conclusion {c}") < 3 || !strings.Contains(string(js), `.replace("{c}", channel)`) {
		t.Fatal("autoKickedOff 三语文案须为 relais conclusion {c} 并替换为当前频道名")
	}
	// needs-human 时"继续"须仍可见（联网频道的 M5 行为），只在 resolved 时隐藏
	if strings.Contains(string(js), `st.resolved || !!st.needs_human_q`) {
		t.Fatal("auto-resume 不应在 needs_human_q 时隐藏")
	}
	if strings.Contains(string(js), ".innerHTML = m.") || strings.Contains(string(js), ".innerHTML = st.") {
		t.Fatal("动态数据不得拼 innerHTML")
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

// TestWebAnswerResumesBeforePosting：needs-human 回答必须先 resume 再发消息；
// 反过来 bridge 可能在两次请求之间拉到回答，auto-turn 仍见暂停而跳过，循环卡死（冒烟发现）。
func TestWebAnswerResumesBeforePosting(t *testing.T) {
	js, _ := webFS.ReadFile("web/app.js")
	src := string(js)
	start := strings.Index(src, `$("auto-answer-send")`)
	if start < 0 {
		t.Fatal("app.js 缺 auto-answer-send 处理器")
	}
	handler := src[start:]
	if end := strings.Index(handler, "}));"); end >= 0 {
		handler = handler[:end]
	}
	r, m := strings.Index(handler, "/auto/resume"), strings.Index(handler, "/messages")
	if r < 0 || m < 0 || r > m {
		t.Fatalf("auto-answer-send 须先调 /auto/resume 再 POST /messages（resume@%d messages@%d）", r, m)
	}
}

// TestWebReopenPostsReason："继续讨论"须问理由，先 /auto/reopen 再以人的身份发消息，否则两侧都不醒（频道卡死）。
func TestWebReopenPostsReason(t *testing.T) {
	js, _ := webFS.ReadFile("web/app.js")
	src := string(js)
	start := strings.Index(src, `$("auto-reopen").addEventListener`)
	if start < 0 {
		t.Fatal("app.js 缺 auto-reopen 处理器")
	}
	handler := src[start:]
	if end := strings.Index(handler, "}));"); end >= 0 {
		handler = handler[:end]
	}
	if !strings.Contains(handler, `prompt(t("reopenReason"))`) {
		t.Fatal("auto-reopen 须先问理由")
	}
	r, m := strings.Index(handler, "/auto/reopen"), strings.Index(handler, "/messages")
	if r < 0 || m < 0 || r > m {
		t.Fatalf("auto-reopen 须先调 /auto/reopen 再 POST /messages（reopen@%d messages@%d）", r, m)
	}
}
