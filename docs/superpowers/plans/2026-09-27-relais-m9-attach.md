# Relais M9「接入现有对话」实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 Relais 本地模式改成"守卫 + 门铃 + 传输协议"：讨论发生在 Hou 开着的 Claude Code / Codex 对话里，`relais serve` 独自搬信，agent 侧只用三条纯文件命令，控制台独立重做、无登录。

**Architecture:** 新包 `internal/local`（信箱布局、信封、post/wait/attach、协议文本、守卫循环）只依赖 `store`/`msg`/`api`；`internal/cli/localmgr.go` 继续实现 `server.LocalManager`，但模块登记改存 SQLite `local_modules`，两侧配置目录、hook、无头讨论脑整套删除；服务器在 `local_dir` 非空时回环免钥匙、挂本地路由、把 `/` 换成 `web/local/` 页面，并由 `RunServe` 启动守卫 goroutine。

**Tech Stack:** Go 1.27（modernc sqlite、yaml.v3、`log/slog`）、vanilla JS（复用 `web/vendor/marked.min.js` + `purify.min.js`）、bash 安装脚本、launchd。

**Spec:** `docs/superpowers/specs/2026-09-27-relais-m9-attach-design.md`（术语见 `CONTEXT.md`「接入现有对话（M9 起）」；决策 D45）

## Global Constraints

- 版本：`main.go` 的 `version` 与 `internal/server/static.go` 的 `Version` 都改为 `0.7.0-m9`（Task 13）。
- 地板：每个任务结束 `./scripts/check.sh` 全绿（gofmt、vet、build、test、`bash -n` 安装脚本、node --check）。
- agent 侧命令（`relais post/wait/attach`）**不得 import `internal/cli` 的 Client，不发 HTTP**；只读写项目目录与 `$CODEX_HOME`/`~/.claude/sessions`（只读）。
- 守卫只在登记目录下写 `relais/mail/<模块>/`、`relais/PROTOCOL.md`、`CLAUDE.md`/`AGENTS.md` 的标记块；外部命令一律 `exec.Command` 参数数组，不经 shell。
- 线上（`server.toml` 无 `local_dir`）行为不变：没有回环免钥匙，`/api/local/*` 404，`/` 仍是原联网页面（永久锚点）。
- 本地页面只有中文，不请求任何外网资源（`index.html` 里不得出现 `http://` / `https://`）。
- 协议文件 `PROTOCOL.md` 不含内容层小标题："背景"、"观点"、"问题"、"工程附录" 四个词不得出现（单测锚点）。
- 单测用 `t.TempDir()`、假 `HOME`/`CODEX_HOME`、假 `codex` 脚本；不得碰真实 `~/Library`、`~/.codex`、`~/.claude`，不得装 launchd。
- 提交信息中文，尾行 `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`。
- 与 spec 的偏离一律记到 `docs/decisions.md` D46（Task 13），不得静默。

## 已知偏离 spec（执行前定案，写进 D46）

1. **kickoff 消息不占 seq**（store.Kickoff 沿用 M7 `seq=0`），归档文件名为 `kickoff-<结论seq>.md` 而非 `NNN-relais.md`；`wait` 的游标文件同时记 `seq` 与最近见过的 kickoff 文件名。
2. **结论信保留真实发件人**：握手第二封（DB 里 `kind=conclusion`）归档为 `NNN-<side>.md`、`kind: conclusion`，`from:` 是写它的 agent；`conclusion-<seq>.md` 是它的副本（`from: relais`）。只有 kickoff 文件 `from: relais`。
3. **回合计数**：守卫每入库一封 agent 信（`kind` 为 `letter`/`resolved`）调用新方法 `store.CountLocalTurn`，`round_count>=cap` 时置 needs-human（沿用 `capHitQuestion` 文案），不再有 `RequestTurn` 的拒绝。
4. **`relais conclusion` 子命令删除**（读旧目录 `relais/conclusions/`），新布局里结论文件可直接读。
5. **模块登记表**从两侧 `projects.toml` 改为 SQLite `local_modules`；`sides/` 目录不再生成也不再读（升级时导入一次）。

## 文件结构

**新建**
- `internal/local/letter.go` — 归档信信封 `Letter`、`RenderLetter`/`ParseLetter`、文件名 `LetterName`/`ParseLetterName`/`ConclusionName`/`KickoffName`、`Summarize`。
- `internal/local/mailbox.go` — 目录布局 `MailDir`、`EnsureMailDir`、`FindModuleDir`（向上找）、`ListLetters`、`ListModulesIn`。
- `internal/local/side.go` — `DetectSide`。
- `internal/local/outbox.go` — `Post`、`ScanOutbox`、`OutboxItem`。
- `internal/local/wait.go` — `Wait`、游标与 `.wait-<侧>` 标记、`FirstResponder`。
- `internal/local/attach.go` — `FindCodexThread`、`ListCodexThreads`、`ListClaudeSessions`、`WriteAttach`/`ReadAttach`。
- `internal/local/protocol.go` — `ProtocolText`、`WriteProtocol`、`EnsurePointer`。
- `internal/local/daemon.go` — `Daemon`（收件/归档/投递）、`DeliveryText`。
- `internal/local/status.go` — `SideStatus`（claude 在等/没在等、codex 已接入）。
- `internal/store/local.go` — `local_modules`/`local_deliveries` 表与方法、`ListAfterSeq`、`ListKickoffs`、`CountLocalTurn`、`RenameChannel`、`DeleteChannel`、`ReopenChannel`。
- `internal/cli/localcmd.go` — `RunPost`/`RunWait`/`RunAttach`。
- `internal/server/web/local/{index.html,app.js,style.css}` — 本地控制台。
- 各自的 `_test.go`。

**修改**
- `internal/api/api.go` — `LocalModule` 重定义、`LocalSettings` 改字段、新增 `LocalConversation`、`LocalAttachRequest`、`LocalModulePatch`、`LocalState`。
- `internal/server/local.go` — `LocalManager` 接口重定义、路由重写、去心跳。
- `internal/server/auth.go` — 回环免钥匙。
- `internal/server/server.go` — 本地页面路由、`PublishMessage`。
- `internal/server/static.go` — 版本。
- `internal/cli/localmgr.go` — 重写模块层；`internal/cli/local.go` — bootstrap/服务/状态；`internal/cli/admin.go` `RunServe` — 起守卫；`internal/cli/bridge.go`+`client.go` — 去心跳；`main.go` — 子命令表。
- `安装 Relais 本地模式.command`、`scripts/check.sh`、`README.md`、`docs/decisions.md`。

**删除**
- `internal/cli/localhook.go(+_test)`、`localprompt.go(+_test)`、`localturn_test.go`、`session.go(+_test)`、`conclusion.go(+_test)`；`internal/guide/guide.go` 的 `LocalText`（+相关测试）；`internal/cli/localmgr.go` 里 `writeLocalAgentGuide`、`rulesTemplate`、`writeSide`、`checkAgentPath`。

---

### Task 1: `internal/local` 信封、文件名与目录布局

**Files:**
- Create: `internal/local/letter.go`, `internal/local/mailbox.go`, `internal/local/side.go`
- Test: `internal/local/letter_test.go`, `internal/local/mailbox_test.go`, `internal/local/side_test.go`

**Interfaces:**
- Produces:
  ```go
  package local
  type Letter struct {
      ID string `yaml:"id,omitempty"`; Module string `yaml:"module"`; Seq int `yaml:"seq"`
      From string `yaml:"from"`; Date time.Time `yaml:"date"`; Kind string `yaml:"kind"` // letter|resolved|needs-human|conclusion|kickoff
      ReplyTo int `yaml:"reply_to,omitempty"`; Owner string `yaml:"owner,omitempty"`; AckOf int `yaml:"ack_of,omitempty"`
      Summary string `yaml:"summary"`
      Path string `yaml:"-"` // ListLetters 填
  }
  func RenderLetter(l Letter, body string) []byte
  func ParseLetter(data []byte) (Letter, string, error)
  func LetterName(seq int, from string) string        // "007-codex.md"；seq>=1000 时 "1000-codex.md"
  func ParseLetterName(name string) (seq int, from string, ok bool)
  func ConclusionName(seq int) string                 // "conclusion-006.md"
  func KickoffName(seq int) string                    // "kickoff-006.md"
  func Summarize(body string) string                  // 首行去 # 与空白，最多 80 字（rune）
  func MailDir(projectDir, module string) string      // <projectDir>/relais/mail/<module>
  func EnsureMailDir(projectDir, module string) error // 建 mail/<module>/{outbox,drafts}
  func FindModuleDir(startDir, module string) (projectDir string, err error) // 向上找含 relais/mail/<module> 的目录；找不到列出最近一层 relais/mail/ 下有哪些模块
  func ListLetters(mailDir string) ([]Letter, error)  // 归档信 + kickoff 文件，按 seq 升序；kickoff 排在同 seq 结论之后
  func ListModulesIn(projectDir string) ([]string, error)
  func ValidModuleName(name string) bool              // 非空、无 "/"、无 ".."、首尾无空白
  func DetectSide(getenv func(string) string) (string, error)
  ```

- [ ] **Step 1: 写失败测试 `letter_test.go`**

```go
package local

import (
	"strings"
	"testing"
	"time"
)

func TestRenderParseLetterRoundTrip(t *testing.T) {
	l := Letter{ID: "01X", Module: "黑客松", Seq: 7, From: "codex", Date: time.Date(2026, 9, 27, 21, 10, 3, 0, time.UTC),
		Kind: "resolved", ReplyTo: 6, Owner: "codex", AckOf: 5, Summary: "同意收敛"}
	data := RenderLetter(l, "# 同意收敛\n\n正文 `不动`\n")
	got, body, err := ParseLetter(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seq != 7 || got.From != "codex" || got.Kind != "resolved" || got.AckOf != 5 || got.Owner != "codex" || got.ReplyTo != 6 || got.Module != "黑客松" {
		t.Fatalf("信封往返不一致: %+v", got)
	}
	if body != "# 同意收敛\n\n正文 `不动`\n" {
		t.Fatalf("正文被改动: %q", body)
	}
	if !strings.HasPrefix(string(data), "---\n") || !strings.Contains(string(data), "\n---\n\n# 同意收敛") {
		t.Fatalf("frontmatter 形状不对: %s", data)
	}
}

func TestLetterNames(t *testing.T) {
	if LetterName(7, "codex") != "007-codex.md" || LetterName(1000, "hou") != "1000-hou.md" {
		t.Fatal("LetterName 格式错")
	}
	if ConclusionName(6) != "conclusion-006.md" || KickoffName(6) != "kickoff-006.md" {
		t.Fatal("结论/开工文件名错")
	}
	seq, from, ok := ParseLetterName("012-claude.md")
	if !ok || seq != 12 || from != "claude" {
		t.Fatalf("ParseLetterName: %d %s %v", seq, from, ok)
	}
	for _, bad := range []string{"conclusion-006.md", "kickoff-006.md", "draft.md", "007-codex.txt", "outbox"} {
		if _, _, ok := ParseLetterName(bad); ok {
			t.Fatalf("%q 不该被当成归档信", bad)
		}
	}
}

func TestSummarize(t *testing.T) {
	if Summarize("## 标题 \n\n正文") != "标题" {
		t.Fatal("应去掉 # 与空白")
	}
	long := strings.Repeat("字", 100)
	if got := Summarize(long); len([]rune(got)) != 80 {
		t.Fatalf("应截到 80 字, got %d", len([]rune(got)))
	}
	if Summarize("\n\n  第三行才有字\n") != "第三行才有字" {
		t.Fatal("应跳过空行")
	}
}
```

- [ ] **Step 2: 写失败测试 `mailbox_test.go`**

```go
package local

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureMailDirAndFind(t *testing.T) {
	proj := t.TempDir()
	if err := EnsureMailDir(proj, "黑客松"); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"outbox", "drafts"} {
		if st, err := os.Stat(filepath.Join(proj, "relais", "mail", "黑客松", sub)); err != nil || !st.IsDir() {
			t.Fatalf("缺 %s", sub)
		}
	}
	deep := filepath.Join(proj, "src", "pkg")
	os.MkdirAll(deep, 0o755)
	got, err := FindModuleDir(deep, "黑客松")
	if err != nil || got != proj {
		t.Fatalf("向上找模块目录失败: %q %v", got, err)
	}
	_, err = FindModuleDir(deep, "不存在")
	if err == nil || !contains(err.Error(), "黑客松") {
		t.Fatalf("找不到时应列出已有模块: %v", err)
	}
	mods, _ := ListModulesIn(proj)
	if len(mods) != 1 || mods[0] != "黑客松" {
		t.Fatalf("ListModulesIn: %v", mods)
	}
}

func TestListLettersOrdersAndSkipsOthers(t *testing.T) {
	proj := t.TempDir()
	EnsureMailDir(proj, "m")
	md := MailDir(proj, "m")
	write := func(name string, l Letter) {
		os.WriteFile(filepath.Join(md, name), RenderLetter(l, "x"), 0o644)
	}
	write("002-codex.md", Letter{Seq: 2, From: "codex", Kind: "letter"})
	write("001-hou.md", Letter{Seq: 1, From: "hou", Kind: "letter"})
	write("003-claude.md", Letter{Seq: 3, From: "claude", Kind: "conclusion", Owner: "claude"})
	write("kickoff-003.md", Letter{Seq: 3, From: "relais", Kind: "kickoff", Owner: "claude"})
	write("conclusion-003.md", Letter{Seq: 3, From: "relais", Kind: "conclusion"})
	os.WriteFile(filepath.Join(md, "drafts", "x.md"), []byte("草稿"), 0o644)
	os.WriteFile(filepath.Join(md, "outbox", "claude-01.md"), []byte("---\nfrom: claude\n---\n\n待发"), 0o644)
	ls, err := ListLetters(md)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range ls {
		names = append(names, filepath.Base(l.Path))
	}
	want := []string{"001-hou.md", "002-codex.md", "003-claude.md", "kickoff-003.md"}
	if len(names) != len(want) {
		t.Fatalf("got %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v want %v", names, want)
		}
	}
}

func TestValidModuleName(t *testing.T) {
	for _, ok := range []string{"黑客松", "m1", "a b"} {
		if !ValidModuleName(ok) {
			t.Fatalf("%q 应合法", ok)
		}
	}
	for _, bad := range []string{"", " x", "x ", "a/b", "..", "a..b"} {
		if ValidModuleName(bad) {
			t.Fatalf("%q 应非法", bad)
		}
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

注意：原 M7 `validModuleName` 拒绝含空格的名字；M9 放宽为只拒绝斜杠、`..`、首尾空白（spec §4）。`a b` 合法。

- [ ] **Step 3: 写失败测试 `side_test.go`**

```go
package local

import "testing"

func TestDetectSide(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		want string
		err  bool
	}{
		{"claude", map[string]string{"CLAUDECODE": "1"}, "claude", false},
		{"codex home", map[string]string{"CODEX_HOME": "/x"}, "codex", false},
		{"codex sandbox", map[string]string{"CODEX_SANDBOX_NETWORK_DISABLED": "1"}, "codex", false},
		{"none", map[string]string{}, "", true},
		{"both", map[string]string{"CLAUDECODE": "1", "CODEX_HOME": "/x"}, "", true},
	}
	for _, c := range cases {
		got, err := DetectSide(env(c.env))
		if (err != nil) != c.err || got != c.want {
			t.Fatalf("%s: got %q err=%v", c.name, got, err)
		}
	}
}
```

- [ ] **Step 4: 跑测试确认失败**

Run: `go test ./internal/local/ 2>&1 | head -5`
Expected: 编译失败（包不存在 / 未定义符号）。

- [ ] **Step 5: 实现 `letter.go`**

```go
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

func LetterName(seq int, from string) string   { return fmt.Sprintf("%03d-%s.md", seq, from) }
func ConclusionName(seq int) string            { return fmt.Sprintf("conclusion-%03d.md", seq) }
func KickoffName(seq int) string               { return fmt.Sprintf("kickoff-%03d.md", seq) }

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
```

- [ ] **Step 6: 实现 `mailbox.go` 与 `side.go`**

```go
package local

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func MailDir(projectDir, module string) string {
	return filepath.Join(projectDir, "relais", "mail", module)
}

func EnsureMailDir(projectDir, module string) error {
	if !ValidModuleName(module) {
		return fmt.Errorf("模块名 %q 不合法", module)
	}
	for _, sub := range []string{"outbox", "drafts"} {
		if err := os.MkdirAll(filepath.Join(MailDir(projectDir, module), sub), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func ValidModuleName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && !strings.Contains(name, "/") && !strings.Contains(name, "..")
}

func ListModulesIn(projectDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(projectDir, "relais", "mail"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && ValidModuleName(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// FindModuleDir 从 startDir 向上找含 relais/mail/<module>/ 的目录。
func FindModuleDir(startDir, module string) (string, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", err
	}
	var seen []string
	for {
		if st, err := os.Stat(MailDir(dir, module)); err == nil && st.IsDir() {
			return dir, nil
		}
		if mods, _ := ListModulesIn(dir); len(mods) > 0 && seen == nil {
			seen = mods
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if len(seen) > 0 {
		return "", fmt.Errorf("从 %s 向上没找到模块 %q；这里有的模块：%s", startDir, module, strings.Join(seen, "、"))
	}
	return "", fmt.Errorf("从 %s 向上没找到任何 relais/mail/ 目录；先在控制台新建模块", startDir)
}

// ListLetters：归档信（NNN-<from>.md）与 kickoff-NNN.md，按 seq 升序，同 seq 时 kickoff 在后。跳过 outbox/drafts/conclusion-*.md/标记文件。
func ListLetters(mailDir string) ([]Letter, error) {
	entries, err := os.ReadDir(mailDir)
	if err != nil {
		return nil, err
	}
	var out []Letter
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		_, _, isLetter := ParseLetterName(name)
		isKickoff := strings.HasPrefix(name, "kickoff-") && strings.HasSuffix(name, ".md")
		if !isLetter && !isKickoff {
			continue
		}
		data, err := os.ReadFile(filepath.Join(mailDir, name))
		if err != nil {
			return nil, err
		}
		l, _, err := ParseLetter(data)
		if err != nil {
			continue // 坏文件不挡路，控制台会另行提示
		}
		l.Path = filepath.Join(mailDir, name)
		out = append(out, l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Kind != "kickoff" && out[j].Kind == "kickoff"
	})
	return out, nil
}
```

```go
package local

import "fmt"

// DetectSide 按环境判断当前对话是哪一侧（spec §6.4）。--as 由调用方优先处理。
func DetectSide(getenv func(string) string) (string, error) {
	claude := getenv("CLAUDECODE") != ""
	codex := getenv("CODEX_HOME") != "" || getenv("CODEX_SANDBOX") != "" || getenv("CODEX_SANDBOX_NETWORK_DISABLED") != ""
	switch {
	case claude && codex:
		return "", fmt.Errorf("环境里同时有 Claude Code 与 Codex 的标记，请加 --as claude 或 --as codex")
	case claude:
		return "claude", nil
	case codex:
		return "codex", nil
	}
	return "", fmt.Errorf("判断不出当前是哪一侧（没有 CLAUDECODE 或 CODEX_HOME 环境变量），请加 --as claude 或 --as codex")
}
```

- [ ] **Step 7: 跑测试**

Run: `go test ./internal/local/ -run 'TestRenderParse|TestLetterNames|TestSummarize|TestEnsureMailDir|TestListLetters|TestValidModuleName|TestDetectSide' -v`
Expected: 全部 PASS。

- [ ] **Step 8: 提交**

```bash
git add internal/local
git commit -m "feat(local): 信箱布局、归档信信封与侧的判断（M9 Task 1）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: store 新增本地登记表与频道改名/删除/重开

**Files:**
- Create: `internal/store/local.go`
- Modify: `internal/store/store.go`（`Open` 的迁移循环追加两条 `CREATE TABLE IF NOT EXISTS`）
- Test: `internal/store/local_test.go`

**Interfaces:**
- Produces:
  ```go
  type LocalModule struct {
      ChannelID int64; Name string; Dir string; ArchivedSeq int
      CodexThreadID, CodexThreadName, CodexAttachedAt, CodexDeliveryError string
      CreatedAt, ClosedAt string
  }
  func (s *Store) UpsertLocalModule(channelID int64, dir string) error       // 不存在则插入（created_at=now），存在只更新 dir
  func (s *Store) LocalModules() ([]LocalModule, error)                      // JOIN channels 取 Name，按 Name 排序，含已关闭
  func (s *Store) LocalModuleByName(name string) (LocalModule, error)        // sql.ErrNoRows 原样返回
  func (s *Store) SetArchivedSeq(channelID int64, seq int) error
  func (s *Store) SetCodexAttach(channelID int64, threadID, threadName, at string) error
  func (s *Store) SetCodexDeliveryError(channelID int64, msg string) error
  func (s *Store) SetLocalClosed(channelID int64, closedAt string) error     // "" = 重开
  func (s *Store) RecordDelivery(messageID, side, status, errText string) error   // upsert
  func (s *Store) Delivery(messageID, side string) (status, errText, at string, err error)
  func (s *Store) LastDelivery(channelID int64, side string) (messageID, status, errText, at string, err error) // 按 messages.created_at 最新
  func (s *Store) ListAfterSeq(channelID int64, seq int) ([]Message, error)  // seq>参数，升序，含 Sender/To/Body
  func (s *Store) ListKickoffs(channelID int64) ([]Message, error)           // kind=kickoff（seq=0），按 created_at 升序，含 AckOf
  func (s *Store) SeqOf(messageID string) (int, error)
  func (s *Store) MessageIDBySeq(channelID int64, seq int) (string, error)
  func (s *Store) CountLocalTurn(channelID int64) (capHit bool, err error)   // round_count+1；>=cap 时 SetNeedsHuman(capHitQuestion) 并返回 true；无 channel_auto 行时 upsert 一行 cap=16
  func (s *Store) RenameChannel(channelID int64, newName string) error       // UNIQUE 冲突 → ErrChannelExists
  func (s *Store) DeleteChannel(channelID int64) error                       // messages/recipients/sent_keys/drafts/channel_auto/members/local_modules/local_deliveries/channels 一个事务
  func (s *Store) ReopenChannel(channelID int64) error                       // closed=0, paused=0
  var ErrChannelExists = errors.New("频道名已存在")
  ```

- [ ] **Step 1: 写失败测试 `internal/store/local_test.go`**

```go
package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openLocalTest(t *testing.T) (*Store, *Channel, map[string]*User) {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	us := map[string]*User{}
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.CreateUser(n, n, "pw-"+n)
		us[n] = u
	}
	ch, _ := st.CreateChannel("m")
	for _, u := range us {
		st.AddMember(ch.ID, u.ID)
	}
	return st, ch, us
}

func TestLocalModuleLifecycle(t *testing.T) {
	st, ch, _ := openLocalTest(t)
	if err := st.UpsertLocalModule(ch.ID, "/p"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLocalModule(ch.ID, "/p2"); err != nil {
		t.Fatal(err)
	}
	m, err := st.LocalModuleByName("m")
	if err != nil || m.Dir != "/p2" || m.Name != "m" || m.ArchivedSeq != 0 || m.CreatedAt == "" {
		t.Fatalf("upsert 后: %+v %v", m, err)
	}
	st.SetArchivedSeq(ch.ID, 3)
	st.SetCodexAttach(ch.ID, "tid", "名", "2026-09-27T00:00:00Z")
	st.SetCodexDeliveryError(ch.ID, "boom")
	st.SetLocalClosed(ch.ID, "2026-09-27T01:00:00Z")
	ms, _ := st.LocalModules()
	if len(ms) != 1 || ms[0].ArchivedSeq != 3 || ms[0].CodexThreadID != "tid" || ms[0].CodexThreadName != "名" || ms[0].CodexDeliveryError != "boom" || ms[0].ClosedAt == "" {
		t.Fatalf("字段没存住: %+v", ms)
	}
	if err := st.RenameChannel(ch.ID, "m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LocalModuleByName("m"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("旧名应查不到: %v", err)
	}
	if m, err := st.LocalModuleByName("m2"); err != nil || m.ChannelID != ch.ID {
		t.Fatalf("新名应查到: %v", err)
	}
	st.CreateChannel("taken")
	if err := st.RenameChannel(ch.ID, "taken"); !errors.Is(err, ErrChannelExists) {
		t.Fatalf("重名应 ErrChannelExists, got %v", err)
	}
	st.ReopenChannel(ch.ID)
	if a, _ := st.GetAuto(ch.ID); a.Closed || a.Paused {
		t.Fatal("重开后不应 closed/paused")
	}
	if err := st.DeleteChannel(ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ChannelByName("m2"); err == nil {
		t.Fatal("删除后频道应不存在")
	}
	if ms, _ := st.LocalModules(); len(ms) != 0 {
		t.Fatal("删除后登记表应空")
	}
}

func TestListAfterSeqAndKickoffs(t *testing.T) {
	st, ch, us := openLocalTest(t)
	to := []int64{us["claude"].ID, us["hou"].ID}
	m1, _ := st.SaveMessage(ch.ID, us["codex"].ID, to, "一", "正文一", "")
	m2, _ := st.SaveMessageOpts(ch.ID, us["codex"].ID, to, "二", "正文二", "", SaveOpts{Kind: "resolved", Owner: "codex"})
	ls, err := st.ListAfterSeq(ch.ID, 1)
	if err != nil || len(ls) != 1 || ls[0].ID != m2.ID || ls[0].Sender != "codex" || ls[0].Body != "正文二" || len(ls[0].To) != 2 {
		t.Fatalf("ListAfterSeq: %+v %v", ls, err)
	}
	if seq, _ := st.SeqOf(m1.ID); seq != 1 {
		t.Fatalf("SeqOf: %d", seq)
	}
	if id, _ := st.MessageIDBySeq(ch.ID, 2); id != m2.ID {
		t.Fatal("MessageIDBySeq")
	}
	// 握手 → kickoff（seq=0）
	m3, _ := st.SaveMessageOpts(ch.ID, us["claude"].ID, []int64{us["codex"].ID, us["hou"].ID}, "三", "附和", "", SaveOpts{Kind: "resolved", Owner: "codex", AckOf: m2.ID})
	if r, _ := st.EvaluateHandshake(ch.ID, m3.ID); r != HandshakeDone {
		t.Fatalf("应握手, got %v", r)
	}
	k, err := st.Kickoff(ch.ID, us["hou"].ID)
	if err != nil {
		t.Fatal(err)
	}
	ks, _ := st.ListKickoffs(ch.ID)
	if len(ks) != 1 || ks[0].ID != k.ID || ks[0].AckOf != m3.ID || ks[0].Owner != "codex" {
		t.Fatalf("ListKickoffs: %+v", ks)
	}
	if ls, _ := st.ListAfterSeq(ch.ID, 0); len(ls) != 3 {
		t.Fatalf("kickoff 不占 seq，ListAfterSeq(0) 应 3 封, got %d", len(ls))
	}
}

func TestDeliveriesAndLocalTurn(t *testing.T) {
	st, ch, us := openLocalTest(t)
	m, _ := st.SaveMessage(ch.ID, us["codex"].ID, []int64{us["claude"].ID}, "一", "x", "")
	st.RecordDelivery(m.ID, "codex", "error", "no attach")
	st.RecordDelivery(m.ID, "codex", "ok", "")
	if status, e, at, err := st.Delivery(m.ID, "codex"); err != nil || status != "ok" || e != "" || at == "" {
		t.Fatalf("Delivery upsert: %s %s %s %v", status, e, at, err)
	}
	if id, status, _, _, _ := st.LastDelivery(ch.ID, "codex"); id != m.ID || status != "ok" {
		t.Fatalf("LastDelivery: %s %s", id, status)
	}
	st.SetAutoEnabled(ch.ID, true, 2) // 2 条 = 1 回合
	if hit, _ := st.CountLocalTurn(ch.ID); hit {
		t.Fatal("第 1 条不应到顶")
	}
	if hit, _ := st.CountLocalTurn(ch.ID); !hit {
		t.Fatal("第 2 条应到顶")
	}
	if a, _ := st.GetAuto(ch.ID); a.NeedsHumanQ == "" || !a.Paused || a.RoundCount != 2 {
		t.Fatalf("到顶应 needs-human: %+v", a)
	}
	ch2, _ := st.CreateChannel("noauto")
	if hit, err := st.CountLocalTurn(ch2.ID); err != nil || hit {
		t.Fatalf("无 channel_auto 行也要能计数: %v %v", hit, err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/store/ -run 'TestLocalModuleLifecycle|TestListAfterSeq|TestDeliveriesAndLocalTurn' 2>&1 | head -5`
Expected: 未定义符号编译失败。

- [ ] **Step 3: 在 `store.go` 的迁移循环末尾追加两条 DDL**

在 `Open` 里 `for _, ddl := range []string{ ... }` 的切片末尾加：

```go
		`CREATE TABLE IF NOT EXISTS local_modules (
			channel_id INTEGER PRIMARY KEY REFERENCES channels(id),
			dir TEXT NOT NULL,
			archived_seq INTEGER NOT NULL DEFAULT 0,
			codex_thread_id TEXT NOT NULL DEFAULT '',
			codex_thread_name TEXT NOT NULL DEFAULT '',
			codex_attached_at TEXT NOT NULL DEFAULT '',
			codex_delivery_error TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			closed_at TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS local_deliveries (
			message_id TEXT NOT NULL, side TEXT NOT NULL, status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '', at TEXT NOT NULL,
			PRIMARY KEY (message_id, side))`,
```

（循环里对非 "duplicate column" 错误会返回；`CREATE TABLE IF NOT EXISTS` 幂等，无冲突。）

- [ ] **Step 4: 实现 `internal/store/local.go`**

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrChannelExists = errors.New("频道名已存在")

type LocalModule struct {
	ChannelID                                                             int64
	Name, Dir                                                             string
	ArchivedSeq                                                           int
	CodexThreadID, CodexThreadName, CodexAttachedAt, CodexDeliveryError string
	CreatedAt, ClosedAt                                                   string
}

func (s *Store) UpsertLocalModule(channelID int64, dir string) error {
	_, err := s.db.Exec(`INSERT INTO local_modules (channel_id, dir, created_at) VALUES (?,?,?)
		ON CONFLICT(channel_id) DO UPDATE SET dir=excluded.dir`, channelID, dir, now())
	return err
}

const localModuleCols = `m.channel_id, c.name, m.dir, m.archived_seq, m.codex_thread_id, m.codex_thread_name, m.codex_attached_at, m.codex_delivery_error, m.created_at, m.closed_at`

func scanLocalModule(row interface{ Scan(...any) error }) (LocalModule, error) {
	var m LocalModule
	err := row.Scan(&m.ChannelID, &m.Name, &m.Dir, &m.ArchivedSeq, &m.CodexThreadID, &m.CodexThreadName, &m.CodexAttachedAt, &m.CodexDeliveryError, &m.CreatedAt, &m.ClosedAt)
	return m, err
}

func (s *Store) LocalModules() ([]LocalModule, error) {
	rows, err := s.db.Query(`SELECT ` + localModuleCols + ` FROM local_modules m JOIN channels c ON c.id=m.channel_id ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalModule
	for rows.Next() {
		m, err := scanLocalModule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) LocalModuleByName(name string) (LocalModule, error) {
	return scanLocalModule(s.db.QueryRow(`SELECT `+localModuleCols+` FROM local_modules m JOIN channels c ON c.id=m.channel_id WHERE c.name=?`, name))
}

func (s *Store) SetArchivedSeq(channelID int64, seq int) error {
	_, err := s.db.Exec(`UPDATE local_modules SET archived_seq=? WHERE channel_id=?`, seq, channelID)
	return err
}

func (s *Store) SetCodexAttach(channelID int64, threadID, threadName, at string) error {
	_, err := s.db.Exec(`UPDATE local_modules SET codex_thread_id=?, codex_thread_name=?, codex_attached_at=?, codex_delivery_error='' WHERE channel_id=?`, threadID, threadName, at, channelID)
	return err
}

func (s *Store) SetCodexDeliveryError(channelID int64, msg string) error {
	_, err := s.db.Exec(`UPDATE local_modules SET codex_delivery_error=? WHERE channel_id=?`, msg, channelID)
	return err
}

func (s *Store) SetLocalClosed(channelID int64, closedAt string) error {
	_, err := s.db.Exec(`UPDATE local_modules SET closed_at=? WHERE channel_id=?`, closedAt, channelID)
	return err
}

func (s *Store) RecordDelivery(messageID, side, status, errText string) error {
	_, err := s.db.Exec(`INSERT INTO local_deliveries (message_id, side, status, error, at) VALUES (?,?,?,?,?)
		ON CONFLICT(message_id, side) DO UPDATE SET status=excluded.status, error=excluded.error, at=excluded.at`, messageID, side, status, errText, now())
	return err
}

func (s *Store) Delivery(messageID, side string) (status, errText, at string, err error) {
	err = s.db.QueryRow(`SELECT status, error, at FROM local_deliveries WHERE message_id=? AND side=?`, messageID, side).Scan(&status, &errText, &at)
	return
}

func (s *Store) LastDelivery(channelID int64, side string) (messageID, status, errText, at string, err error) {
	err = s.db.QueryRow(`SELECT d.message_id, d.status, d.error, d.at FROM local_deliveries d JOIN messages m ON m.id=d.message_id
		WHERE m.channel_id=? AND d.side=? ORDER BY m.created_at DESC, m.seq DESC LIMIT 1`, channelID, side).Scan(&messageID, &status, &errText, &at)
	return
}

func (s *Store) listMessagesWhere(where string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(`SELECT m.id, m.channel_id, m.sender_id, u.username, u.display_name, u.avatar, m.summary, m.body_md, COALESCE(m.in_reply_to,''), m.created_at,
		m.seq, m.kind, m.owner, m.owner_reason, m.ack_of FROM messages m JOIN users u ON u.id=m.sender_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Sender, &m.SenderDisplay, &m.SenderAvatar, &m.Summary, &m.Body, &m.InReplyTo, &created,
			&m.Seq, &m.Kind, &m.Owner, &m.OwnerReason, &m.AckOf); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(created)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		to, err := s.recipientNames(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].To = to
	}
	return out, nil
}

func (s *Store) ListAfterSeq(channelID int64, seq int) ([]Message, error) {
	return s.listMessagesWhere(`m.channel_id=? AND m.seq>? ORDER BY m.seq`, channelID, seq)
}

func (s *Store) ListKickoffs(channelID int64) ([]Message, error) {
	return s.listMessagesWhere(`m.channel_id=? AND m.kind='kickoff' ORDER BY m.created_at, m.id`, channelID)
}

func (s *Store) SeqOf(messageID string) (int, error) {
	var seq int
	err := s.db.QueryRow(`SELECT seq FROM messages WHERE id=?`, messageID).Scan(&seq)
	return seq, err
}

func (s *Store) MessageIDBySeq(channelID int64, seq int) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM messages WHERE channel_id=? AND seq=?`, channelID, seq).Scan(&id)
	return id, err
}

// CountLocalTurn：本地频道每入库一封 agent 信记一条；到上限置 needs-human（沿用 capHitQuestion 文案）。不拒收。
func (s *Store) CountLocalTurn(channelID int64) (bool, error) {
	if _, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, enabled, round_count, cap, paused, needs_human_q) VALUES (?,1,0,16,0,'')
		ON CONFLICT(channel_id) DO NOTHING`, channelID); err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`UPDATE channel_auto SET round_count=round_count+1 WHERE channel_id=?`, channelID); err != nil {
		return false, err
	}
	var round, cap int
	var q string
	if err := s.db.QueryRow(`SELECT round_count, cap, needs_human_q FROM channel_auto WHERE channel_id=?`, channelID).Scan(&round, &cap, &q); err != nil {
		return false, err
	}
	if round < cap {
		return false, nil
	}
	if q == "" {
		if err := s.SetNeedsHuman(channelID, s.capHitQuestion(channelID, cap)); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (s *Store) RenameChannel(channelID int64, newName string) error {
	_, err := s.db.Exec(`UPDATE channels SET name=? WHERE id=?`, newName, channelID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrChannelExists
	}
	return err
}

func (s *Store) DeleteChannel(channelID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM local_deliveries WHERE message_id IN (SELECT id FROM messages WHERE channel_id=?)`,
		`DELETE FROM recipients WHERE message_id IN (SELECT id FROM messages WHERE channel_id=?)`,
		`DELETE FROM sent_keys WHERE channel_id=?`,
		`DELETE FROM drafts WHERE channel_id=?`,
		`DELETE FROM messages WHERE channel_id=?`,
		`DELETE FROM channel_auto WHERE channel_id=?`,
		`DELETE FROM guidance WHERE channel_id=?`,
		`DELETE FROM invites WHERE channel_id=?`,
		`DELETE FROM channel_members WHERE channel_id=?`,
		`DELETE FROM local_modules WHERE channel_id=?`,
		`DELETE FROM channels WHERE id=?`,
	} {
		if _, err := tx.Exec(q, channelID); err != nil {
			return fmt.Errorf("删除频道: %s: %w", q, err)
		}
	}
	return tx.Commit()
}

func (s *Store) ReopenChannel(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET closed=0, paused=0 WHERE channel_id=?`, channelID)
	return err
}
```

实现前用 `grep -n "CREATE TABLE" internal/store/store.go` 核对表名（`channel_members`、`guidance`、`invites`、`drafts`、`sent_keys` 的确切名字）与 `parseTime` 是否已有（`GetMessage` 里怎么把 `created_at` 转 `time.Time`，照抄同一做法；若没有现成函数就在 `local.go` 里写 `func parseTime(s string) time.Time { t, _ := time.Parse(time.RFC3339, s); return t }`）。

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/store/ -v -run 'TestLocalModuleLifecycle|TestListAfterSeq|TestDeliveriesAndLocalTurn'`
Expected: PASS；再跑 `go test ./internal/store/` 确认旧测试不受影响。

- [ ] **Step 6: 提交**

```bash
git add internal/store
git commit -m "feat(store): 本地模块登记表、投递记录、频道改名/删除/重开、本地回合计数（M9 Task 2）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: `relais post` 的文件层：写 outbox 与扫 outbox

**Files:**
- Create: `internal/local/outbox.go`
- Test: `internal/local/outbox_test.go`

**Interfaces:**
- Consumes: Task 1 的 `Letter`、`RenderLetter`、`ParseLetter`、`MailDir`、`ListLetters`、`Summarize`。
- Produces:
  ```go
  type PostOpts struct{ Resolved bool; Owner string; Ack bool; NeedsHuman bool }
  // Post 校验并把 file 复制成 <mailDir>/outbox/<side>-<ULID>.md（头：from/kind/owner/ack_of/summary）；返回 outbox 路径。
  func Post(mailDir, side, file string, o PostOpts) (string, error)
  type OutboxItem struct{ Path, Key, From, Kind, Owner, Summary string; AckOf int; Body string }
  // ScanOutbox 按文件名排序读 outbox/*.md；解析失败的项 Err 非空（调用方改名 .rejected）。
  func ScanOutbox(mailDir string) ([]OutboxItem, []error)
  ```
  outbox 文件的 `Key` = 文件名去掉 `.md`（如 `claude-01M3…`），守卫用它当幂等键。

- [ ] **Step 1: 写失败测试 `outbox_test.go`**

```go
package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupMail(t *testing.T) (string, string) {
	t.Helper()
	proj := t.TempDir()
	if err := EnsureMailDir(proj, "m"); err != nil {
		t.Fatal(err)
	}
	return proj, MailDir(proj, "m")
}

func TestPostWritesOutboxWithHeader(t *testing.T) {
	_, md := setupMail(t)
	draft := filepath.Join(md, "drafts", "a.md")
	os.WriteFile(draft, []byte("# 我的看法\n\n正文\n"), 0o644)
	out, err := Post(md, "claude", draft, PostOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != filepath.Join(md, "outbox") || !strings.HasPrefix(filepath.Base(out), "claude-") {
		t.Fatalf("outbox 路径不对: %s", out)
	}
	data, _ := os.ReadFile(out)
	l, body, err := ParseLetter(data)
	if err != nil || l.From != "claude" || l.Kind != "letter" || l.Summary != "我的看法" || body != "# 我的看法\n\n正文\n" {
		t.Fatalf("outbox 头/正文: %+v %q %v", l, body, err)
	}
	if _, err := os.Stat(draft); err != nil {
		t.Fatal("原文件不能动")
	}
	items, errs := ScanOutbox(md)
	if len(errs) != 0 || len(items) != 1 || items[0].Key != strings.TrimSuffix(filepath.Base(out), ".md") || items[0].Body != body || items[0].From != "claude" {
		t.Fatalf("ScanOutbox: %+v %v", items, errs)
	}
}

func TestPostFlags(t *testing.T) {
	_, md := setupMail(t)
	f := filepath.Join(md, "drafts", "a.md")
	os.WriteFile(f, []byte("提议收敛\n"), 0o644)
	if _, err := Post(md, "claude", f, PostOpts{Resolved: true}); err == nil {
		t.Fatal("--resolved 无 --owner 应报错")
	}
	if _, err := Post(md, "claude", f, PostOpts{Resolved: true, Owner: "kimi"}); err == nil {
		t.Fatal("owner 只能 claude|codex|user")
	}
	if _, err := Post(md, "claude", f, PostOpts{Ack: true, Resolved: true, Owner: "claude"}); err == nil {
		t.Fatal("--ack 与 --resolved 互斥")
	}
	if _, err := Post(md, "claude", f, PostOpts{Ack: true}); err == nil || !strings.Contains(err.Error(), "resolved") {
		t.Fatalf("对方没有 resolved 信时 --ack 应报错: %v", err)
	}
	// 对方的 resolved 信到了
	os.WriteFile(filepath.Join(md, "005-codex.md"), RenderLetter(Letter{Seq: 5, From: "codex", Kind: "resolved", Owner: "codex"}, "x"), 0o644)
	os.WriteFile(filepath.Join(md, "006-codex.md"), RenderLetter(Letter{Seq: 6, From: "codex", Kind: "letter"}, "x"), 0o644)
	out, err := Post(md, "claude", f, PostOpts{Ack: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	l, _, _ := ParseLetter(data)
	if l.Kind != "resolved" || l.AckOf != 5 || l.Owner != "codex" {
		t.Fatalf("--ack 应 kind=resolved ack_of=5 owner=对方提名: %+v", l)
	}
	out, _ = Post(md, "claude", f, PostOpts{NeedsHuman: true})
	data, _ = os.ReadFile(out)
	l, _, _ = ParseLetter(data)
	if l.Kind != "needs-human" || l.Summary != "提议收敛" {
		t.Fatalf("--needs-human: %+v", l)
	}
	out, _ = Post(md, "codex", f, PostOpts{Resolved: true, Owner: "user"})
	data, _ = os.ReadFile(out)
	l, _, _ = ParseLetter(data)
	if l.Kind != "resolved" || l.Owner != "user" || l.From != "codex" {
		t.Fatalf("--resolved --owner user: %+v", l)
	}
	empty := filepath.Join(md, "drafts", "empty.md")
	os.WriteFile(empty, []byte("  \n"), 0o644)
	if _, err := Post(md, "claude", empty, PostOpts{}); err == nil {
		t.Fatal("空文件应报错")
	}
	if _, err := Post(md, "kimi", f, PostOpts{}); err == nil {
		t.Fatal("侧只能 claude|codex")
	}
}

func TestScanOutboxReportsBadFiles(t *testing.T) {
	_, md := setupMail(t)
	os.WriteFile(filepath.Join(md, "outbox", "claude-bad.md"), []byte("没有 frontmatter"), 0o644)
	os.WriteFile(filepath.Join(md, "outbox", "codex-02.md"), []byte("---\nfrom: codex\nkind: letter\nsummary: s\n---\n\n正文"), 0o644)
	items, errs := ScanOutbox(md)
	if len(items) != 1 || items[0].From != "codex" || len(errs) != 1 || !strings.Contains(errs[0].Error(), "claude-bad.md") {
		t.Fatalf("items=%+v errs=%v", items, errs)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/local/ -run 'TestPost|TestScanOutbox' 2>&1 | head -3`
Expected: 未定义 `Post`/`PostOpts`/`ScanOutbox`。

- [ ] **Step 3: 实现 `outbox.go`**

```go
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
	AckOf                                  int
	Body                                   string
}

// ScanOutbox：守卫每轮调用；只看 *.md（.tmp 是 Post 写到一半的）。
func ScanOutbox(mailDir string) ([]OutboxItem, []error) {
	entries, err := os.ReadDir(filepath.Join(mailDir, "outbox"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []error{err}
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var items []OutboxItem
	var errs []error
	for _, n := range names {
		p := filepath.Join(mailDir, "outbox", n)
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n, err))
			continue
		}
		l, body, err := ParseLetter(data)
		if err != nil || (l.From != "claude" && l.From != "codex") {
			if err == nil {
				err = fmt.Errorf("from 必须是 claude 或 codex")
			}
			errs = append(errs, fmt.Errorf("%s: %w", n, err))
			continue
		}
		if l.Kind == "" {
			l.Kind = "letter"
		}
		items = append(items, OutboxItem{Path: p, Key: strings.TrimSuffix(n, ".md"), From: l.From, Kind: l.Kind, Owner: l.Owner, AckOf: l.AckOf, Summary: l.Summary, Body: body})
	}
	return items, errs
}
```

`ulid` 已是 go.mod 依赖（store 在用），import 路径照 `internal/store/store.go`。

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/local/ -v -run 'TestPost|TestScanOutbox'`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/local
git commit -m "feat(local): relais post 的 outbox 写入与扫描（M9 Task 3）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: `relais wait` 的文件层：门铃、游标、接话规则

**Files:**
- Create: `internal/local/wait.go`
- Test: `internal/local/wait_test.go`

**Interfaces:**
- Consumes: Task 1 的 `ListLetters`、`Letter`。
- Produces:
  ```go
  type WaitMarker struct{ PID int `json:"pid"`; Since time.Time `json:"since"`; SessionID string `json:"session_id,omitempty"` }
  type Cursor struct{ Seq int `json:"seq"`; Kickoff string `json:"kickoff,omitempty"` } // kickoff = 最近见过的 kickoff 文件名
  func ReadCursor(mailDir, side string) Cursor                 // 文件 .cursor-<side>，缺省零值
  func WriteCursor(mailDir, side string, c Cursor) error
  func ReadWaitMarker(mailDir, side string) (WaitMarker, bool) // 文件 .wait-<side>
  // NewLetters 返回 side 尚未见过、且不是 side 自己写的信（含 kickoff 文件），并给出新的游标。
  func NewLetters(mailDir, side string, c Cursor) ([]Letter, Cursor, error)
  // Wait 写 .wait-<side>，每 poll 轮询一次直到有新信或 ctx 结束；返回前写游标、删标记。timeout<=0 = 不超时。
  func Wait(ctx context.Context, mailDir, side string, poll, timeout time.Duration, sessionID string) ([]Letter, error)
  var ErrWaitTimeout = errors.New("没等到新信")
  // FirstResponder：雇主的信由谁先回（spec §6.2 / §7.3）。letters 为该信之前的全部信。
  func FirstResponder(human Letter, humanBody string, letters []Letter) string
  // Describe 给 wait 的输出行（spec §6.2 三种附加行）。
  func Describe(l Letter, body string, side string, prior []Letter) string
  ```

- [ ] **Step 1: 写失败测试 `wait_test.go`**

```go
package local

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func put(t *testing.T, md, name string, l Letter, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(md, name), RenderLetter(l, body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNewLettersSkipsOwnAndSeen(t *testing.T) {
	_, md := setupMail(t)
	put(t, md, "001-hou.md", Letter{Seq: 1, From: "hou", Kind: "letter"}, "@codex 先回\n\n题")
	put(t, md, "002-codex.md", Letter{Seq: 2, From: "codex", Kind: "letter"}, "x")
	put(t, md, "003-claude.md", Letter{Seq: 3, From: "claude", Kind: "letter"}, "x")
	ls, c, err := NewLetters(md, "claude", Cursor{})
	if err != nil || len(ls) != 2 || ls[0].Seq != 1 || ls[1].Seq != 2 || c.Seq != 3 {
		t.Fatalf("应看到 1、2，游标推到 3: %+v %+v %v", ls, c, err)
	}
	ls, c, _ = NewLetters(md, "claude", c)
	if len(ls) != 0 {
		t.Fatal("没有新信")
	}
	put(t, md, "kickoff-003.md", Letter{Seq: 3, From: "relais", Kind: "kickoff", Owner: "codex"}, "开工")
	ls, c, _ = NewLetters(md, "claude", c)
	if len(ls) != 1 || ls[0].Kind != "kickoff" || c.Kickoff != "kickoff-003.md" || c.Seq != 3 {
		t.Fatalf("kickoff 文件应被当新信: %+v %+v", ls, c)
	}
	if ls, _, _ := NewLetters(md, "claude", c); len(ls) != 0 {
		t.Fatal("同一 kickoff 不重复")
	}
}

func TestWaitReturnsOnNewLetterAndCleansMarker(t *testing.T) {
	_, md := setupMail(t)
	done := make(chan []Letter, 1)
	go func() {
		ls, _ := Wait(context.Background(), md, "claude", 20*time.Millisecond, 0, "sess-1")
		done <- ls
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if m, ok := ReadWaitMarker(md, "claude"); ok && m.SessionID == "sess-1" && m.PID == os.Getpid() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等待中应有 .wait-claude 标记")
		}
		time.Sleep(10 * time.Millisecond)
	}
	put(t, md, "001-codex.md", Letter{Seq: 1, From: "codex", Kind: "letter"}, "x")
	select {
	case ls := <-done:
		if len(ls) != 1 || ls[0].Seq != 1 {
			t.Fatalf("应返回第 1 封: %+v", ls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("wait 没有醒")
	}
	if _, ok := ReadWaitMarker(md, "claude"); ok {
		t.Fatal("返回后标记应删除")
	}
	if c := ReadCursor(md, "claude"); c.Seq != 1 {
		t.Fatalf("游标应写 1: %+v", c)
	}
}

func TestWaitImmediateAndTimeout(t *testing.T) {
	_, md := setupMail(t)
	put(t, md, "001-codex.md", Letter{Seq: 1, From: "codex", Kind: "letter"}, "x")
	ls, err := Wait(context.Background(), md, "claude", 10*time.Millisecond, 0, "")
	if err != nil || len(ls) != 1 {
		t.Fatalf("已有未读信应立即返回: %v %v", ls, err)
	}
	_, err = Wait(context.Background(), md, "claude", 10*time.Millisecond, 50*time.Millisecond, "")
	if err != ErrWaitTimeout {
		t.Fatalf("应超时: %v", err)
	}
	if _, ok := ReadWaitMarker(md, "claude"); ok {
		t.Fatal("超时后标记应删除")
	}
}

func TestFirstResponderAndDescribe(t *testing.T) {
	h := Letter{Seq: 3, From: "hou", Kind: "letter"}
	prior := []Letter{{Seq: 1, From: "claude", Kind: "letter"}, {Seq: 2, From: "codex", Kind: "letter"}}
	if FirstResponder(h, "@codex 先回\n\n题", prior) != "codex" {
		t.Fatal("首行 @codex 先回 优先")
	}
	if FirstResponder(h, "题", prior) != "claude" {
		t.Fatal("否则是最近 agent 信的另一侧")
	}
	if FirstResponder(h, "题", nil) != "claude" {
		t.Fatal("尚无 agent 信时 claude 先回")
	}
	d := Describe(Letter{Seq: 3, From: "hou", Kind: "letter", Path: "/p/003-hou.md"}, "题", "codex", prior)
	if !strings.Contains(d, "第 3 封") || !strings.Contains(d, "来自 hou") || !strings.Contains(d, "/p/003-hou.md") || !strings.Contains(d, "由 claude 先回，本侧不用回") {
		t.Fatalf("Describe 雇主信: %q", d)
	}
	d = Describe(Letter{Seq: 4, From: "relais", Kind: "kickoff", Owner: "codex", Path: "/p/kickoff-004.md"}, "", "codex", prior)
	if !strings.Contains(d, "你是承接方") || !strings.Contains(d, "conclusion-004.md") {
		t.Fatalf("Describe kickoff 承接方: %q", d)
	}
	d = Describe(Letter{Seq: 4, From: "relais", Kind: "kickoff", Owner: "codex", Path: "/p/kickoff-004.md"}, "", "claude", prior)
	if !strings.Contains(d, "承接方是 codex，本侧不用开工") {
		t.Fatalf("Describe kickoff 非承接方: %q", d)
	}
	d = Describe(Letter{Seq: 4, From: "relais", Kind: "kickoff", Owner: "user", Path: "/p/kickoff-004.md"}, "", "claude", prior)
	if !strings.Contains(d, "承接方是雇主") {
		t.Fatalf("Describe kickoff owner=user: %q", d)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/local/ -run 'TestNewLetters|TestWait|TestFirstResponder' 2>&1 | head -3`
Expected: 未定义符号。

- [ ] **Step 3: 实现 `wait.go`**

```go
package local

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
			if name > next.Kickoff {
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
	if l.From == "hou" {
		if FirstResponder(l, body, prior) == side {
			return line + "\n雇主的信，由你先回"
		}
		return line + fmt.Sprintf("\n雇主的信，由 %s 先回，本侧不用回", FirstResponder(l, body, prior))
	}
	return line
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/local/ -v -run 'TestNewLetters|TestWait|TestFirstResponder'`
Expected: PASS（`TestWaitReturnsOnNewLetterAndCleansMarker` 靠 20ms 轮询，2 秒内必回）。

- [ ] **Step 5: 提交**

```bash
git add internal/local
git commit -m "feat(local): relais wait 的门铃、游标与接话规则（M9 Task 4）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: `relais attach` 的文件层：找 Codex 线程、列两侧对话、接入登记

**Files:**
- Create: `internal/local/attach.go`
- Test: `internal/local/attach_test.go`

**Interfaces:**
- Produces:
  ```go
  type Thread struct{ ID, Name, Title, Cwd string; UpdatedAt time.Time }
  type Session struct{ ID, Name, Cwd, Status string; PID int; UpdatedAt time.Time }
  type Attach struct{ ThreadID string `json:"thread_id"`; Name string `json:"name"`; Cwd string `json:"cwd"`; At time.Time `json:"at"` }
  func CodexStateDB(codexHome string) (string, error)                       // 最新的 state_*.sqlite（按名字里的数字最大）
  func ListCodexThreads(codexHome, dir string) ([]Thread, error)             // archived=0 且 cwd 等价于 dir（真实路径，兼容 /private 前缀），updated_at_ms 降序，最多 20；dir=="" 不过滤
  func FindCodexThread(codexHome, dir string) (Thread, error)                // ListCodexThreads 第一条；空 → 明确错误
  func ListClaudeSessions(home, dir string) ([]Session, error)               // ~/.claude/sessions/*.json，kind=interactive 且 cwd 等价于 dir；坏文件跳过
  func WriteAttach(mailDir string, a Attach) error                           // .attach-codex，JSON
  func ReadAttach(mailDir string) (Attach, bool)
  func SamePath(a, b string) bool                                            // filepath.Clean + EvalSymlinks 相等，或去掉 /private 前缀后相等
  ```

- [ ] **Step 1: 写失败测试 `attach_test.go`**

```go
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
```

`modernc.org/sqlite` 的驱动名照 `internal/store/store.go` 里 `sql.Open` 的第一个参数。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/local/ -run 'TestFindCodex|TestCodexState|TestListClaude|TestAttachFile' 2>&1 | head -3`
Expected: 未定义符号。

- [ ] **Step 3: 实现 `attach.go`**

```go
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

func ListCodexThreads(codexHome, dir string) ([]Thread, error) {
	p, err := CodexStateDB(codexHome)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+p+"?mode=ro&_pragma=busy_timeout(2000)")
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
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/local/ -v -run 'TestFindCodex|TestCodexState|TestListClaude|TestAttachFile'`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/local
git commit -m "feat(local): 找 Codex 线程、列两侧对话、接入登记文件（M9 Task 5）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: 协议文件与指针块

**Files:**
- Create: `internal/local/protocol.go`
- Test: `internal/local/protocol_test.go`

**Interfaces:**
- Produces:
  ```go
  const ProtocolMarker = "<!-- relais-protocol v1 -->"
  const PointerMarker = "<!-- relais-local -->"
  func ProtocolText() string                    // 全文（以 ProtocolMarker 开头）
  // WriteProtocol：<dir>/relais/PROTOCOL.md 不存在或以 ProtocolMarker 开头 → 写入；否则不动并返回 ErrProtocolCustom。
  func WriteProtocol(projectDir string) error
  var ErrProtocolCustom = errors.New("PROTOCOL.md 已被手改，未覆盖")
  // EnsurePointer：在 <dir>/<file> 里保证有两行版指针块；旧版（含 PointerMarker 到 "<!-- /relais-local -->"）整块替换；不存在则追加；文件不存在则新建。
  func EnsurePointer(path string) error
  ```

- [ ] **Step 1: 写失败测试 `protocol_test.go`**

```go
package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolTextIsTransportOnly(t *testing.T) {
	s := ProtocolText()
	if !strings.HasPrefix(s, ProtocolMarker) {
		t.Fatal("应以标记开头")
	}
	for _, must := range []string{"relais wait", "relais attach", "relais post", "--resolved --owner", "--ack", "--needs-human", "relais/mail/", "conclusion-", "outbox", "先回", "CLAUDECODE"} {
		if !strings.Contains(s, must) {
			t.Fatalf("协议缺 %q", must)
		}
	}
	for _, forbidden := range []string{"背景", "观点", "问题清单", "工程附录", "http://", "https://"} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("协议不该含内容层/外链词 %q", forbidden)
		}
	}
	for i, h := range []string{"## 1", "## 2", "## 3", "## 4", "## 5", "## 6", "## 7", "## 8"} {
		if !strings.Contains(s, h) {
			t.Fatalf("应有第 %d 段", i+1)
		}
	}
}

func TestWriteProtocolRespectsCustom(t *testing.T) {
	proj := t.TempDir()
	if err := WriteProtocol(proj); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(proj, "relais", "PROTOCOL.md")
	data, _ := os.ReadFile(p)
	if string(data) != ProtocolText() {
		t.Fatal("首次写入应等于全文")
	}
	if err := WriteProtocol(proj); err != nil {
		t.Fatal("带标记的可重复覆盖")
	}
	os.WriteFile(p, []byte("# 我自己的协议\n"), 0o644)
	err := WriteProtocol(proj)
	if !errors.Is(err, ErrProtocolCustom) {
		t.Fatalf("手改过的不覆盖: %v", err)
	}
	data, _ = os.ReadFile(p)
	if string(data) != "# 我自己的协议\n" {
		t.Fatal("手改内容被动了")
	}
}

func TestEnsurePointerReplacesOldBlock(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "CLAUDE.md")
	old := "# 项目\n\n原有内容\n\n<!-- relais-local -->\n## Relais 本地模式\n旧的三行说明\n<!-- /relais-local -->\n"
	os.WriteFile(p, []byte(old), 0o644)
	if err := EnsurePointer(p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	s := string(data)
	if strings.Contains(s, "旧的三行说明") || strings.Count(s, PointerMarker) != 1 || !strings.Contains(s, "原有内容") || !strings.Contains(s, "relais/PROTOCOL.md") {
		t.Fatalf("旧块应被替换、原内容保留: %q", s)
	}
	before := s
	EnsurePointer(p)
	data, _ = os.ReadFile(p)
	if string(data) != before {
		t.Fatal("幂等")
	}
	p2 := filepath.Join(dir, "AGENTS.md")
	if err := EnsurePointer(p2); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(p2)
	if !strings.HasPrefix(string(data), PointerMarker) {
		t.Fatal("新文件应只含指针块")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/local/ -run 'TestProtocol|TestWriteProtocol|TestEnsurePointer' 2>&1 | head -3`
Expected: 未定义符号。

- [ ] **Step 3: 实现 `protocol.go`**

```go
package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	ProtocolMarker    = "<!-- relais-protocol v1 -->"
	PointerMarker     = "<!-- relais-local -->"
	pointerEndMarker  = "<!-- /relais-local -->"
	pointerBlock      = PointerMarker + "\n本项目接入了 Relais。雇主说「接入 relais 模块 X」时，读 relais/PROTOCOL.md 并照做。\n" + pointerEndMarker + "\n"
)

var ErrProtocolCustom = errors.New("PROTOCOL.md 已被手改，未覆盖")

// ProtocolText：两侧共读的传输协议（spec §5）。只讲信怎么走，不讲信里写什么。
func ProtocolText() string {
	return ProtocolMarker + `
# Relais 通信协议（只管信怎么走，不管信里写什么）

本项目里的模块目录在 ` + "`relais/mail/<模块>/`" + `。下面「X」代表模块名。

## 1. 你是谁
Claude Code 里的对话是 ` + "`claude`" + ` 侧，Codex 里的对话是 ` + "`codex`" + ` 侧。
relais 命令会按环境自动判断（Claude Code 有 CLAUDECODE，Codex 有 CODEX_HOME）；判断不了时命令会要求加 ` + "`--as claude`" + ` 或 ` + "`--as codex`" + `。

## 2. 接入
雇主说「接入 relais 模块 X」时：
- claude 侧：在后台运行 ` + "`relais wait X`" + `。它会一直等，信到了就退出并打印信的路径。
- codex 侧：运行 ` + "`relais attach X`" + `。它把当前这个对话登记为收信对话，之后来信会直接出现在对话里。
接入后不用再做别的，信到了会有通知。

## 3. 收信
通知里给的是文件路径。只读 ` + "`relais/mail/X/`" + ` 下的信，按文件名里的序号读（` + "`007-codex.md`" + ` 是第 7 封，codex 写的）。
别的模块的目录不读。` + "`outbox/`" + ` 与 ` + "`drafts/`" + ` 里的不是来信。

## 4. 回信
把正文写成一个 .md 文件（建议放 ` + "`relais/mail/X/drafts/`" + `），首行写一句摘要，然后运行：
` + "`relais post X <文件>`" + `
信封（序号、发件人、时间、回复哪封）由工具填，你不用写。三种标记：
- ` + "`relais post X <文件> --resolved --owner claude|codex|user`" + `：我认为可以收敛，并提名承接方。
- ` + "`relais post X <文件> --ack`" + `：我附和对方最近一封收敛提议（承接方自动取对方那封的）。
- ` + "`relais post X <文件> --needs-human`" + `：需要雇主定夺；文件首行就是问题。

## 5. 发完之后
- claude 侧：再在后台运行一次 ` + "`relais wait X`" + `。
- codex 侧：不用做。

## 6. 收敛怎么算
双方各一封收敛信、第二封带 --ack、承接方一致，就算握手。结论会落在 ` + "`relais/mail/X/conclusion-<序号>.md`" + `，承接方会收到开工通知（文件 ` + "`kickoff-<序号>.md`" + `，发件人 relais）。承接方是 user 时两侧都只等雇主。

## 7. 雇主的信
雇主写的信（发件人 hou）会送到两侧，但只由一侧先回：通知里会写明是不是你。不是你就不回，等对方的信。

## 8. 不要做的事
不改别人的信；不往 ` + "`outbox/`" + ` 以外的地方放待发的信；不自己写 ` + "`conclusion-*.md`" + ` 或 ` + "`kickoff-*.md`" + `；不给 Relais 发 HTTP 请求；不读别的模块。
`
}

func WriteProtocol(projectDir string) error {
	p := filepath.Join(projectDir, "relais", "PROTOCOL.md")
	if data, err := os.ReadFile(p); err == nil && !strings.HasPrefix(string(data), ProtocolMarker) {
		return ErrProtocolCustom
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(ProtocolText()), 0o644)
}

func EnsurePointer(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s := string(raw)
	if i := strings.Index(s, PointerMarker); i >= 0 {
		j := strings.Index(s[i:], pointerEndMarker)
		if j < 0 {
			return nil // 只有起始标记的异常文件不动
		}
		end := i + j + len(pointerEndMarker)
		if strings.HasPrefix(s[end:], "\n") {
			end++
		}
		if s[i:end] == pointerBlock {
			return nil
		}
		s = s[:i] + pointerBlock + s[end:]
		return os.WriteFile(path, []byte(s), 0o644)
	}
	if s != "" && !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if s != "" {
		s += "\n"
	}
	return os.WriteFile(path, []byte(s+pointerBlock), 0o644)
}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/local/ -v -run 'TestProtocol|TestWriteProtocol|TestEnsurePointer'`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/local
git commit -m "feat(local): 传输协议 PROTOCOL.md 与两行指针块（M9 Task 6）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: 守卫循环：收件 → 归档 → 投递，以及两侧状态

**Files:**
- Create: `internal/local/daemon.go`, `internal/local/status.go`
- Test: `internal/local/daemon_test.go`

**Interfaces:**
- Consumes: Task 2 的 store 方法；Task 1/3/4/5 的 `ListLetters`、`ScanOutbox`、`Describe`、`ReadAttach`、`ReadWaitMarker`、`RenderLetter`、文件名函数。
- Produces:
  ```go
  type ModuleRef struct{ ChannelID int64; Name, Dir string }
  type Daemon struct {
      Store     *store.Store
      Modules   func() ([]ModuleRef, error)          // 未关闭模块（cli 层从 store.LocalModules 过滤 ClosedAt==""）
      CodexPath func() string                         // 空 → 投递记 error "未设置 codex 路径"
      Publish   func(channelID int64, m *store.Message, channelName string) // SSE；可为 nil
      Notify    func(title, body string)              // 桌面通知；可为 nil
      Log       *slog.Logger                          // nil → slog.Default()
      Interval  time.Duration                         // Run 用；默认 2s
      Timeout   time.Duration                         // codex queue 超时；默认 20s
      Users     map[string]int64                      // 用户名 → id（claude/codex/hou），cli 层启动时填
  }
  func (d *Daemon) RunOnce() error                    // 三步，逐模块，错误记日志不中断；返回第一个错误（测试用）
  func (d *Daemon) Run(ctx context.Context)
  func (d *Daemon) Redeliver(channelID int64) error   // 对最后一封应投给 codex 的信重跑投递
  func DeliveryText(module string, l Letter, body string, prior []Letter) string // codex 通知文本 = "Relais 模块「X」" 前缀 + Describe(..., "codex", prior) + 尾句
  type SideStatus struct {
      ClaudeWaiting bool; ClaudeWaitSince time.Time; ClaudeSessionID string; ClaudeCursor int
      CodexAttached bool; CodexThreadName string; CodexAttachedAt time.Time
  }
  func ReadSideStatus(mailDir string, pidAlive func(int) bool) SideStatus
  var PIDAlive = func(pid int) bool { ... }           // syscall.Kill(pid, 0) == nil || EPERM
  ```

**守卫三步（`RunOnce` 对每个模块）：**
1. 收件：`ScanOutbox` → 每项 `SaveMessageOpts(chID, Users[from], to=另一侧+hou, summary, body, "", SaveOpts{Kind, Owner, AckOf: msgID(由 AckOf seq 经 MessageIDBySeq 换), IdemKey: "outbox:"+Key})`；`kind=needs-human` 存成 `Kind:""` 并 `SetNeedsHuman(chID, summary)`；`kind=letter/resolved` 后 `CountLocalTurn`（到顶 → Notify）；`kind=""`（letter）后 `ClearKickedOff`；`kind=resolved` 后 `EvaluateHandshake`，`HandshakeDone` 且 mode==autopilot → `Kickoff(chID, Users["hou"])`；`HandshakeOwnerConflict` → Notify。成功后 `os.Remove` outbox 文件；`Publish`。解析失败项：改名 `<原名>.rejected`，写同名 `.rejected.txt` 原因，Notify 一次。
2. 归档：`ListAfterSeq(chID, ArchivedSeq)` → 每条写 `LetterName(seq, sender)`（`Kind` 空 → `letter`；`AckOf` 从 id 换 seq；`ReplyTo` = 该信之前最后一封非本侧信的 seq）；`kind=conclusion` 额外写 `ConclusionName(seq)`（`From: relais`）；每条写完 `SetArchivedSeq`。再 `ListKickoffs` 里 `Delivery(id,"archive")` 不存在的 → 写 `KickoffName(结论seq)`（结论 seq = `SeqOf(k.AckOf)`；`From: relais`，正文 = k.Body）→ `RecordDelivery(id,"archive","ok","")`。文件写法：先写 `.tmp` 再 `rename`。
3. 投递：对步骤 2 新归档的每封（含 kickoff），收件人含 `codex`（kickoff 一律投）且发件人不是 codex → `deliverCodex`：无 `.attach-codex` → `RecordDelivery(id,"codex","error","codex 侧未接入")` + `SetCodexDeliveryError`；有 → `exec.CommandContext(ctx(Timeout), CodexPath(), "queue", "--thread", a.ThreadID, "--message", text)`，成功 `RecordDelivery ok` + 清 `SetCodexDeliveryError("")`，失败记 error（stderr 前 200 字）+ Notify。claude 侧不做事（`RecordDelivery(id,"claude","ok","")` 记一下即可）。
   `.attach-codex` 文件比 DB 新（`At` 晚于 `CodexAttachedAt`）时先 `SetCodexAttach`。

- [ ] **Step 1: 写失败测试 `daemon_test.go`**

```go
package local

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/store"
)

type fixture struct {
	st    *store.Store
	proj  string
	md    string
	chID  int64
	d     *Daemon
	codex string // 假 codex 脚本记录 argv 的日志文件
	notes []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	users := map[string]int64{}
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.CreateUser(n, n, "pw")
		users[n] = u.ID
	}
	ch, _ := st.CreateChannel("m")
	for _, id := range users {
		st.AddMember(ch.ID, id)
	}
	st.SetAutoEnabled(ch.ID, true, 16)
	proj := t.TempDir()
	EnsureMailDir(proj, "m")
	st.UpsertLocalModule(ch.ID, proj)
	log := filepath.Join(t.TempDir(), "codex.log")
	script := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \""+log+"\"\nif [ \"$FAKE_CODEX_FAIL\" = 1 ]; then echo boom >&2; exit 3; fi\n"), 0o755)
	f := &fixture{st: st, proj: proj, md: MailDir(proj, "m"), chID: ch.ID, codex: log}
	f.d = &Daemon{Store: st, Users: users,
		Modules:   func() ([]ModuleRef, error) { return []ModuleRef{{ChannelID: ch.ID, Name: "m", Dir: proj}}, nil },
		CodexPath: func() string { return script },
		Notify:    func(title, body string) { f.notes = append(f.notes, title+"|"+body) },
	}
	return f
}

func (f *fixture) post(t *testing.T, side, body string, o PostOpts) {
	t.Helper()
	draft := filepath.Join(f.md, "drafts", side+".md")
	os.WriteFile(draft, []byte(body), 0o644)
	if _, err := Post(f.md, side, draft, o); err != nil {
		t.Fatal(err)
	}
}

func TestDaemonIngestsArchivesDelivers(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", Name: "对话", At: time.Now()})
	f.post(t, "claude", "# 第一封\n\n你好\n", PostOpts{})
	if err := f.d.RunOnce(); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(f.md, "outbox")); len(entries) != 0 {
		t.Fatal("入库后 outbox 应清空")
	}
	data, err := os.ReadFile(filepath.Join(f.md, "001-claude.md"))
	if err != nil {
		t.Fatal("应归档为 001-claude.md")
	}
	l, body, _ := ParseLetter(data)
	if l.Seq != 1 || l.From != "claude" || l.Kind != "letter" || l.Module != "m" || l.ID == "" || l.Summary != "第一封" || body != "# 第一封\n\n你好\n" {
		t.Fatalf("归档信封: %+v %q", l, body)
	}
	argv, _ := os.ReadFile(f.codex)
	s := string(argv)
	if !strings.Contains(s, "queue\n--thread\nt-1\n--message\n") || !strings.Contains(s, "Relais 模块「m」") || !strings.Contains(s, "001-claude.md") || !strings.Contains(s, "PROTOCOL.md") {
		t.Fatalf("codex queue 参数: %q", s)
	}
	m, _ := f.st.LocalModuleByName("m")
	if m.ArchivedSeq != 1 || m.CodexThreadID != "t-1" || m.CodexDeliveryError != "" {
		t.Fatalf("登记表: %+v", m)
	}
	if id, status, _, _, _ := f.st.LastDelivery(f.chID, "codex"); id != l.ID || status != "ok" {
		t.Fatalf("投递记录: %s %s", id, status)
	}
	if a, _ := f.st.GetAuto(f.chID); a.RoundCount != 1 {
		t.Fatalf("agent 信应计回合: %d", a.RoundCount)
	}
	// 幂等：再跑一轮不重复归档、不重复投递
	f.d.RunOnce()
	if argv2, _ := os.ReadFile(f.codex); string(argv2) != s {
		t.Fatal("不该重复投递")
	}
}

func TestDaemonCodexNotAttachedAndFailure(t *testing.T) {
	f := newFixture(t)
	f.post(t, "claude", "x\n", PostOpts{})
	f.d.RunOnce()
	m, _ := f.st.LocalModuleByName("m")
	if !strings.Contains(m.CodexDeliveryError, "未接入") {
		t.Fatalf("未接入应记错误: %+v", m)
	}
	if len(f.notes) == 0 {
		t.Fatal("投递失败应通知")
	}
	WriteAttach(f.md, Attach{ThreadID: "t-1", Name: "对话", At: time.Now()})
	t.Setenv("FAKE_CODEX_FAIL", "1")
	if err := f.d.Redeliver(f.chID); err == nil {
		t.Fatal("codex 退出非零应报错")
	}
	m, _ = f.st.LocalModuleByName("m")
	if !strings.Contains(m.CodexDeliveryError, "boom") {
		t.Fatalf("应记 stderr: %q", m.CodexDeliveryError)
	}
	t.Setenv("FAKE_CODEX_FAIL", "0")
	if err := f.d.Redeliver(f.chID); err != nil {
		t.Fatal(err)
	}
	m, _ = f.st.LocalModuleByName("m")
	if m.CodexDeliveryError != "" {
		t.Fatal("重投成功应清错误")
	}
}

func TestDaemonHandshakeConclusionKickoffAndWait(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.st.SetMode(f.chID, "autopilot")
	f.post(t, "codex", "提议\n", PostOpts{Resolved: true, Owner: "codex"})
	f.d.RunOnce()
	f.post(t, "claude", "附和\n", PostOpts{Ack: true})
	f.d.RunOnce()
	for _, name := range []string{"001-codex.md", "002-claude.md", "conclusion-002.md", "kickoff-002.md"} {
		if _, err := os.Stat(filepath.Join(f.md, name)); err != nil {
			t.Fatalf("缺 %s", name)
		}
	}
	data, _ := os.ReadFile(filepath.Join(f.md, "002-claude.md"))
	l, _, _ := ParseLetter(data)
	if l.Kind != "conclusion" || l.AckOf != 1 || l.From != "claude" || l.Owner != "codex" {
		t.Fatalf("结论信: %+v", l)
	}
	data, _ = os.ReadFile(filepath.Join(f.md, "kickoff-002.md"))
	l, _, _ = ParseLetter(data)
	if l.Kind != "kickoff" || l.From != "relais" || l.Owner != "codex" || l.Seq != 2 {
		t.Fatalf("kickoff 文件: %+v", l)
	}
	argv, _ := os.ReadFile(f.codex)
	if !strings.Contains(string(argv), "你是承接方") || !strings.Contains(string(argv), "conclusion-002.md") {
		t.Fatalf("codex 应收到开工通知: %q", argv)
	}
	// claude 侧 wait 立即拿到附和之后的新东西：kickoff（001 是对方的、002 是自己的）
	ls, err := Wait(nil, f.md, "claude", 10*time.Millisecond, 0, "")
	if err != nil || len(ls) != 2 || ls[0].Seq != 1 || ls[1].Kind != "kickoff" {
		t.Fatalf("wait: %+v %v", ls, err)
	}
	f.d.RunOnce() // 幂等：kickoff 不重复归档
	if entries, _ := filepath.Glob(filepath.Join(f.md, "kickoff-*")); len(entries) != 1 {
		t.Fatal("kickoff 只归档一次")
	}
}

func TestDaemonNeedsHumanRejectedAndHumanLetter(t *testing.T) {
	f := newFixture(t)
	WriteAttach(f.md, Attach{ThreadID: "t-1", At: time.Now()})
	f.post(t, "codex", "要不要上 Redis？\n", PostOpts{NeedsHuman: true})
	f.d.RunOnce()
	if a, _ := f.st.GetAuto(f.chID); a.NeedsHumanQ != "要不要上 Redis？" || !a.Paused {
		t.Fatalf("needs-human 应置问题并暂停: %+v", a)
	}
	os.WriteFile(filepath.Join(f.md, "outbox", "claude-bad.md"), []byte("没头"), 0o644)
	f.d.RunOnce()
	if _, err := os.Stat(filepath.Join(f.md, "outbox", "claude-bad.md.rejected")); err != nil {
		t.Fatal("坏文件应改名 .rejected")
	}
	// 雇主在控制台写信（直接入库）→ 归档 003-hou.md 并投给 codex，通知里说明由谁先回
	users := f.d.Users
	f.st.SaveMessage(f.chID, users["hou"], []int64{users["claude"], users["codex"]}, "上", "@codex 先回\n\n上 Redis", "")
	f.d.RunOnce()
	if _, err := os.Stat(filepath.Join(f.md, "002-hou.md")); err != nil {
		t.Fatal("雇主信应归档 002-hou.md")
	}
	argv, _ := os.ReadFile(f.codex)
	if !strings.Contains(string(argv), "002-hou.md") || !strings.Contains(string(argv), "由你先回") {
		t.Fatalf("雇主信投递: %q", argv)
	}
}

func TestReadSideStatus(t *testing.T) {
	_, md := setupMail(t)
	s := ReadSideStatus(md, func(int) bool { return true })
	if s.ClaudeWaiting || s.CodexAttached {
		t.Fatal("初始都未接入")
	}
	os.WriteFile(filepath.Join(md, ".wait-claude"), []byte(`{"pid":42,"since":"2026-09-27T10:00:00Z","session_id":"s"}`), 0o644)
	WriteCursor(md, "claude", Cursor{Seq: 3})
	WriteAttach(md, Attach{ThreadID: "t", Name: "n", At: time.Now()})
	s = ReadSideStatus(md, func(pid int) bool { return pid == 42 })
	if !s.ClaudeWaiting || s.ClaudeSessionID != "s" || s.ClaudeCursor != 3 || !s.CodexAttached || s.CodexThreadName != "n" {
		t.Fatalf("status: %+v", s)
	}
	s = ReadSideStatus(md, func(int) bool { return false })
	if s.ClaudeWaiting {
		t.Fatal("pid 死了不算在等")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/local/ -run 'TestDaemon|TestReadSideStatus' 2>&1 | head -3`
Expected: 未定义 `Daemon` 等。

- [ ] **Step 3: 实现 `status.go`**

```go
package local

import (
	"errors"
	"syscall"
	"time"
)

type SideStatus struct {
	ClaudeWaiting   bool
	ClaudeWaitSince time.Time
	ClaudeSessionID string
	ClaudeCursor    int
	CodexAttached   bool
	CodexThreadName string
	CodexAttachedAt time.Time
}

var PIDAlive = func(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func ReadSideStatus(mailDir string, pidAlive func(int) bool) SideStatus {
	var s SideStatus
	if m, ok := ReadWaitMarker(mailDir, "claude"); ok && pidAlive(m.PID) {
		s.ClaudeWaiting, s.ClaudeWaitSince, s.ClaudeSessionID = true, m.Since, m.SessionID
	}
	s.ClaudeCursor = ReadCursor(mailDir, "claude").Seq
	if a, ok := ReadAttach(mailDir); ok {
		s.CodexAttached, s.CodexThreadName, s.CodexAttachedAt = true, a.Name, a.At
	}
	return s
}
```

- [ ] **Step 4: 实现 `daemon.go`**

```go
package local

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/store"
)

type ModuleRef struct {
	ChannelID int64
	Name, Dir string
}

type Daemon struct {
	Store     *store.Store
	Modules   func() ([]ModuleRef, error)
	CodexPath func() string
	Publish   func(channelID int64, m *store.Message, channelName string)
	Notify    func(title, body string)
	Log       *slog.Logger
	Interval  time.Duration
	Timeout   time.Duration
	Users     map[string]int64
}

func (d *Daemon) log() *slog.Logger {
	if d.Log == nil {
		return slog.Default()
	}
	return d.Log
}

func (d *Daemon) notify(title, body string) {
	if d.Notify != nil {
		d.Notify(title, body)
	}
}

func (d *Daemon) Run(ctx context.Context) {
	iv := d.Interval
	if iv <= 0 {
		iv = 2 * time.Second
	}
	for {
		if err := d.RunOnce(); err != nil {
			d.log().Warn("守卫一轮出错", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(iv):
		}
	}
}

func (d *Daemon) RunOnce() error {
	mods, err := d.Modules()
	if err != nil {
		return err
	}
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, m := range mods {
		md := MailDir(m.Dir, m.Name)
		if err := os.MkdirAll(filepath.Join(md, "outbox"), 0o755); err != nil {
			keep(err)
			continue
		}
		d.syncAttach(m, md)
		keep(d.ingest(m, md))
		fresh, err := d.archive(m, md)
		keep(err)
		for _, l := range fresh {
			keep(d.deliver(m, md, l))
		}
	}
	return first
}

func (d *Daemon) syncAttach(m ModuleRef, md string) {
	a, ok := ReadAttach(md)
	if !ok {
		return
	}
	lm, err := d.Store.LocalModuleByName(m.Name)
	if err != nil {
		return
	}
	prev, _ := time.Parse(time.RFC3339, lm.CodexAttachedAt)
	if lm.CodexThreadID != a.ThreadID || a.At.After(prev) {
		_ = d.Store.SetCodexAttach(m.ChannelID, a.ThreadID, a.Name, a.At.UTC().Format(time.RFC3339))
	}
}

func otherSide(side string) string {
	if side == "claude" {
		return "codex"
	}
	return "claude"
}

// ingest：spec §7.1。
func (d *Daemon) ingest(m ModuleRef, md string) error {
	items, errs := ScanOutbox(md)
	for _, e := range errs {
		name := strings.SplitN(e.Error(), ":", 2)[0]
		p := filepath.Join(md, "outbox", name)
		if _, err := os.Stat(p); err == nil {
			_ = os.Rename(p, p+".rejected")
			_ = os.WriteFile(p+".rejected.txt", []byte(e.Error()+"\n"), 0o644)
			d.notify("Relais · "+m.Name, "outbox 里有一封信读不了："+e.Error())
		}
	}
	var first error
	for _, it := range items {
		if err := d.ingestOne(m, md, it); err != nil {
			d.log().Warn("收件失败", "module", m.Name, "file", it.Path, "err", err)
			if first == nil {
				first = err
			}
		}
	}
	return first
}

func (d *Daemon) ingestOne(m ModuleRef, md string, it OutboxItem) error {
	senderID, ok := d.Users[it.From]
	if !ok {
		return fmt.Errorf("未知发件人 %q", it.From)
	}
	to := []int64{d.Users[otherSide(it.From)], d.Users["hou"]}
	opts := store.SaveOpts{IdemKey: "outbox:" + it.Key}
	switch it.Kind {
	case "resolved":
		opts.Kind, opts.Owner = "resolved", it.Owner
		if it.AckOf > 0 {
			id, err := d.Store.MessageIDBySeq(m.ChannelID, it.AckOf)
			if err != nil {
				return fmt.Errorf("ack_of 指向的第 %d 封不存在", it.AckOf)
			}
			opts.AckOf = id
		}
	case "needs-human", "letter", "":
	default:
		return fmt.Errorf("outbox 信的 kind %q 不认识", it.Kind)
	}
	msg, err := d.Store.SaveMessageOpts(m.ChannelID, senderID, to, it.Summary, it.Body, "", opts)
	if err != nil {
		return err
	}
	if err := os.Remove(it.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	switch it.Kind {
	case "needs-human":
		_ = d.Store.SetNeedsHuman(m.ChannelID, it.Summary)
		d.notify("Relais · "+m.Name, it.From+" 需要你定夺："+it.Summary)
	case "resolved":
		res, err := d.Store.EvaluateHandshake(m.ChannelID, msg.ID)
		if err != nil {
			return err
		}
		switch res {
		case store.HandshakeOwnerConflict:
			d.notify("Relais · "+m.Name, "两侧提名的承接方不一致，请到控制台定")
		case store.HandshakeDone:
			if a, err := d.Store.GetAuto(m.ChannelID); err == nil && a.Mode == "autopilot" {
				if k, err := d.Store.Kickoff(m.ChannelID, d.Users["hou"]); err == nil && d.Publish != nil {
					d.Publish(m.ChannelID, k, m.Name)
				}
			} else {
				d.notify("Relais · "+m.Name, "已握手，等你确认开工")
			}
			if cm, err := d.Store.GetMessage(msg.ID, senderID, true); err == nil {
				msg = cm
			}
		}
		if hit, _ := d.Store.CountLocalTurn(m.ChannelID); hit {
			d.notify("Relais · "+m.Name, "回合到上限了，去控制台决定继续还是收")
		}
	default:
		_ = d.Store.ClearKickedOff(m.ChannelID)
		if hit, _ := d.Store.CountLocalTurn(m.ChannelID); hit {
			d.notify("Relais · "+m.Name, "回合到上限了，去控制台决定继续还是收")
		}
	}
	if d.Publish != nil {
		d.Publish(m.ChannelID, msg, m.Name)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// archive：spec §7.2；返回本轮新落盘的信（含 kickoff），供投递。
func (d *Daemon) archive(m ModuleRef, md string) ([]Letter, error) {
	lm, err := d.Store.LocalModuleByName(m.Name)
	if err != nil {
		return nil, err
	}
	msgs, err := d.Store.ListAfterSeq(m.ChannelID, lm.ArchivedSeq)
	if err != nil {
		return nil, err
	}
	var fresh []Letter
	prior, _ := ListLetters(md)
	for _, msg := range msgs {
		l := Letter{ID: msg.ID, Module: m.Name, Seq: msg.Seq, From: msg.Sender, Date: msg.CreatedAt, Kind: msg.Kind, Owner: msg.Owner, Summary: msg.Summary}
		if l.Kind == "" {
			l.Kind = "letter"
		}
		if msg.AckOf != "" {
			l.AckOf, _ = d.Store.SeqOf(msg.AckOf)
		}
		for i := len(prior) - 1; i >= 0; i-- {
			if prior[i].Kind != "kickoff" && prior[i].From != msg.Sender {
				l.ReplyTo = prior[i].Seq
				break
			}
		}
		l.Path = filepath.Join(md, LetterName(msg.Seq, msg.Sender))
		if err := writeAtomic(l.Path, RenderLetter(l, msg.Body)); err != nil {
			return fresh, err
		}
		if msg.Kind == "conclusion" {
			c := l
			c.From = "relais"
			if err := writeAtomic(filepath.Join(md, ConclusionName(msg.Seq)), RenderLetter(c, msg.Body)); err != nil {
				return fresh, err
			}
		}
		if err := d.Store.SetArchivedSeq(m.ChannelID, msg.Seq); err != nil {
			return fresh, err
		}
		fresh = append(fresh, l)
		prior = append(prior, l)
	}
	ks, err := d.Store.ListKickoffs(m.ChannelID)
	if err != nil {
		return fresh, err
	}
	for _, k := range ks {
		if _, _, _, err := d.Store.Delivery(k.ID, "archive"); err == nil {
			continue
		}
		seq, err := d.Store.SeqOf(k.AckOf)
		if err != nil {
			return fresh, fmt.Errorf("kickoff %s 的结论不存在: %w", k.ID, err)
		}
		l := Letter{ID: k.ID, Module: m.Name, Seq: seq, From: "relais", Date: k.CreatedAt, Kind: "kickoff", Owner: k.Owner, Summary: k.Summary, Path: filepath.Join(md, KickoffName(seq))}
		if err := writeAtomic(l.Path, RenderLetter(l, k.Body)); err != nil {
			return fresh, err
		}
		if err := d.Store.RecordDelivery(k.ID, "archive", "ok", ""); err != nil {
			return fresh, err
		}
		fresh = append(fresh, l)
	}
	return fresh, nil
}

func DeliveryText(module string, l Letter, body string, prior []Letter) string {
	head := fmt.Sprintf("Relais 模块「%s」：", module)
	desc := Describe(l, body, "codex", prior)
	switch l.Kind {
	case "kickoff":
		return head + desc
	}
	if l.From == "hou" {
		return head + "雇主的信，" + strings.TrimPrefix(desc, "") + "\n读它，按 relais/PROTOCOL.md 处理。"
	}
	return head + desc + "\n读它，按 relais/PROTOCOL.md 回信。"
}

// deliver：spec §7.3。claude 侧靠门铃，只记一笔；codex 侧 queue。
func (d *Daemon) deliver(m ModuleRef, md string, l Letter) error {
	if l.Kind != "kickoff" && l.From != "codex" {
		_ = d.Store.RecordDelivery(l.ID, "claude", "ok", "")
	}
	if l.From == "codex" {
		return nil
	}
	return d.deliverCodex(m, md, l)
}

func (d *Daemon) deliverCodex(m ModuleRef, md string, l Letter) error {
	fail := func(msg string) error {
		_ = d.Store.RecordDelivery(l.ID, "codex", "error", msg)
		_ = d.Store.SetCodexDeliveryError(m.ChannelID, msg)
		d.notify("Relais · "+m.Name, "没能把信送进 Codex 对话："+msg)
		return errors.New(msg)
	}
	a, ok := ReadAttach(md)
	if !ok {
		return fail("codex 侧未接入（在 Codex 对话里说：接入 relais 模块 " + m.Name + "）")
	}
	codex := ""
	if d.CodexPath != nil {
		codex = d.CodexPath()
	}
	if codex == "" {
		return fail("未设置 codex 命令路径（控制台 → 设置）")
	}
	data, _ := os.ReadFile(l.Path)
	_, body, _ := ParseLetter(data)
	all, _ := ListLetters(md)
	var prior []Letter
	for _, p := range all {
		if p.Seq < l.Seq || (p.Seq == l.Seq && p.Kind != "kickoff" && l.Kind == "kickoff") {
			prior = append(prior, p)
		}
	}
	text := DeliveryText(m.Name, l, body, prior)
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, codex, "queue", "--thread", a.ThreadID, "--message", text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if r := []rune(msg); len(r) > 200 {
			msg = string(r[:200])
		}
		if msg == "" {
			msg = err.Error()
		}
		return fail("codex queue 失败：" + msg)
	}
	_ = d.Store.RecordDelivery(l.ID, "codex", "ok", "")
	_ = d.Store.SetCodexDeliveryError(m.ChannelID, "")
	return nil
}

// Redeliver：对最后一封应投给 codex 的信（最新的非 codex 归档信或 kickoff）重跑投递。
func (d *Daemon) Redeliver(channelID int64) error {
	mods, err := d.Modules()
	if err != nil {
		return err
	}
	for _, m := range mods {
		if m.ChannelID != channelID {
			continue
		}
		md := MailDir(m.Dir, m.Name)
		d.syncAttach(m, md)
		ls, err := ListLetters(md)
		if err != nil {
			return err
		}
		for i := len(ls) - 1; i >= 0; i-- {
			if ls[i].From != "codex" {
				return d.deliverCodex(m, md, ls[i])
			}
		}
		return fmt.Errorf("没有需要投给 Codex 的信")
	}
	return fmt.Errorf("模块不存在或已关闭")
}
```

`Wait(nil, …)` 在测试里传 nil ctx：实现里 `select` 用 `ctx.Done()` 会 panic——在 `Wait` 开头加 `if ctx == nil { ctx = context.Background() }`（回到 Task 4 的 `wait.go` 补这一行）。

- [ ] **Step 5: 跑测试**

Run: `go test ./internal/local/ -v -run 'TestDaemon|TestReadSideStatus'`
Expected: PASS。若 `TestDaemonIngestsArchivesDelivers` 的 `RoundCount` 断言失败，检查 `ingestOne` 的 `default` 分支是否只对 `letter` 计数（needs-human 不计）。

- [ ] **Step 6: 跑全包 + 地板**

Run: `go test ./internal/local/ ./internal/store/ && ./scripts/check.sh`
Expected: 全绿。

- [ ] **Step 7: 提交**

```bash
git add internal/local
git commit -m "feat(local): 守卫循环——扫 outbox 入库、按序归档、codex queue 投递、两侧状态（M9 Task 7）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: 服务器：回环免钥匙、本地路由重写、本地页面路由、SSE 出口

**Files:**
- Modify: `internal/api/<定义 LocalModule 的文件>`（`grep -ln 'type LocalModule' internal/api/*.go`）、`internal/server/local.go`、`internal/server/auth.go`、`internal/server/server.go`、`internal/server/static.go`
- Test: `internal/server/local_test.go`（重写 fakeLocal 与三个测试）、`internal/server/auth_test.go`（新增回环免钥匙锚点）、`internal/server/web_test.go`（新增本地页面路由测试）

**Interfaces:**
- Produces（api 包，替换旧 `LocalModule`/`LocalSettings`）:
  ```go
  type LocalSide struct {
      Waiting   bool      `json:"waiting"`              // claude
      WaitSince time.Time `json:"wait_since,omitempty"`
      SessionName string  `json:"session_name,omitempty"`
      Attached  bool      `json:"attached"`             // codex
      ThreadName string   `json:"thread_name,omitempty"`
      AttachedAt time.Time `json:"attached_at,omitempty"`
      LastDelivery string `json:"last_delivery,omitempty"` // ok|error|""
      DeliveryError string `json:"delivery_error,omitempty"`
      DeliveryAt time.Time `json:"delivery_at,omitempty"`
  }
  type LocalModule struct {
      Name string `json:"name"`; Dir string `json:"dir"`; Mode string `json:"mode"`
      Round int `json:"round"`; RoundCap int `json:"round_cap"`
      State string `json:"state"` // 未接入|讨论中|等你|已握手|已开工|已关闭
      LastSeq int `json:"last_seq"`; LastFrom string `json:"last_from,omitempty"`; LastAt time.Time `json:"last_at,omitempty"`
      WaitingFor string `json:"waiting_for,omitempty"` // claude|codex|user|""
      Claude LocalSide `json:"claude"`; Codex LocalSide `json:"codex"`
      NeedsHumanQ string `json:"needs_human_q,omitempty"`
      PendingConclusion *LocalConclusion `json:"pending_conclusion,omitempty"`
      Rejected []string `json:"rejected,omitempty"` // outbox 里 .rejected 文件名
      Closed bool `json:"closed"`
  }
  type LocalConclusion struct{ Seq int `json:"seq"`; Owner string `json:"owner"`; Summary string `json:"summary"`; AwaitingConfirm bool `json:"awaiting_confirm"`; Path string `json:"path"` }
  type LocalModuleRequest struct{ Name string `json:"name"`; Dir string `json:"dir"`; CodexThread string `json:"codex_thread,omitempty"` }
  type LocalModulePatch struct{ Name string `json:"name,omitempty"`; Mode string `json:"mode,omitempty"`; RoundCap int `json:"round_cap,omitempty"` }
  type LocalSettings struct{ CodexPath string `json:"codex_path"`; CodexOK bool `json:"codex_ok"`; DefaultMode string `json:"default_mode"`; DefaultCap int `json:"default_cap"`; NotifyEveryLetter bool `json:"notify_every_letter"` }
  type LocalConversation struct{ ID string `json:"id"`; Name string `json:"name"`; Title string `json:"title,omitempty"`; Cwd string `json:"cwd"`; Status string `json:"status,omitempty"`; UpdatedAt time.Time `json:"updated_at"` }
  type LocalAttachRequest struct{ Side string `json:"side"`; Thread string `json:"thread"` }
  type LocalState struct{ Version string `json:"version"`; StartedAt time.Time `json:"started_at"`; CodexOK bool `json:"codex_ok"` }
  ```
  删除 `LocalRules`（连同路由）。`LocalRepo` 不变。
- Produces（server 包）:
  ```go
  type LocalManager interface {
      ScanRepos() ([]api.LocalRepo, error)
      ListModules() ([]api.LocalModule, error)
      CreateModule(req api.LocalModuleRequest) (api.LocalModule, error)
      PatchModule(name string, p api.LocalModulePatch) (api.LocalModule, error)
      CloseModule(name string) error
      ReopenModule(name string) error
      DeleteModule(name string, files bool) error
      Conversations(side, dir string) ([]api.LocalConversation, error)
      AttachModule(name string, req api.LocalAttachRequest) (api.LocalModule, error)
      Redeliver(name string) error
      Settings() (api.LocalSettings, error)
      PutSettings(api.LocalSettings) error
      State() api.LocalState
  }
  func (s *Server) SetLocal(m LocalManager, humanUser string)   // humanUser = 回环免钥匙落到的用户名（"hou"）
  func (s *Server) PublishMessage(channelID int64, m *store.Message, channelName string) // 守卫用的 SSE 出口
  ```
  路由（全部 `s.auth(s.localOnly(...))`，`localOnly` 只检查回环，不再区分人/agent）：
  `GET /api/local/state`、`GET /api/local/repos`、`GET /api/local/modules`、`POST /api/local/modules`、`PATCH /api/local/modules/{name}`、`POST /api/local/modules/{name}/close`、`POST /api/local/modules/{name}/reopen`、`DELETE /api/local/modules/{name}`（query `files=1`）、`GET /api/local/conversations?side=&dir=`、`POST /api/local/modules/{name}/attach`、`POST /api/local/modules/{name}/redeliver`、`GET/PUT /api/local/settings`。
  页面：`s.local != nil` 时 `GET /` 与 `GET /index.html` 返回 `web/local/index.html`，`GET /local/{file}` 返回 `web/local/<file>`；`/app.js`、`/style.css`、`/vendor/*` 原样可用。`s.local == nil` 时不注册 `/local/`。

- [ ] **Step 1: 写失败测试：`auth_test.go` 末尾追加回环免钥匙锚点**

```go
func TestLoopbackWithoutKeyIsHumanOnlyInLocalMode(t *testing.T) {
	// 线上（无 local）：回环不带钥匙 → 401（永久锚点）
	ts, _, _ := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/api/me")
	if resp.StatusCode != 401 {
		t.Fatalf("线上回环免钥匙不该存在: %d", resp.StatusCode)
	}
	// 本地：回环不带钥匙 → 200 且是 hou
	ts2, srv, _, _ := newLocalTestServer(t)
	resp, _ = http.Get(ts2.URL + "/api/me")
	if resp.StatusCode != 200 {
		t.Fatalf("本地回环免钥匙应 200: %d", resp.StatusCode)
	}
	var me api.MeResponse
	json.NewDecoder(resp.Body).Decode(&me)
	if me.Username != "hou" {
		t.Fatalf("应是 hou: %+v", me)
	}
	// 本地但非回环 → 401
	req := httptest.NewRequest("GET", "/api/me", nil)
	req.RemoteAddr = "10.0.0.5:1"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("非回环无钥匙应 401: %d", rec.Code)
	}
	// 本地 + agent token 仍按 token
	r := agentGet(t, ts2, users2Token(t, srv), "/api/me")
	if r.StatusCode != 200 {
		t.Fatalf("agent token 仍有效: %d", r.StatusCode)
	}
}
```

`api.MeResponse` 的确切类型名与 `agentGet` 签名照 `auth.go`/`auth_test.go` 现有代码；`users2Token` 直接改成从 `newLocalTestServer` 返回的 users 取 `users["hou"].AgentToken`（把返回值接住即可）。

- [ ] **Step 2: 重写 `local_test.go`**

```go
package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

type fakeLocal struct {
	modules   []api.LocalModule
	created   []api.LocalModuleRequest
	patched   []api.LocalModulePatch
	closed    []string
	reopened  []string
	deleted   map[string]bool
	attached  []api.LocalAttachRequest
	redeliver []string
	set       api.LocalSettings
}

func (f *fakeLocal) ScanRepos() ([]api.LocalRepo, error) {
	return []api.LocalRepo{{Dir: "/Users/x/proj", Name: "proj"}}, nil
}
func (f *fakeLocal) ListModules() ([]api.LocalModule, error) { return f.modules, nil }
func (f *fakeLocal) CreateModule(req api.LocalModuleRequest) (api.LocalModule, error) {
	if strings.Contains(req.Name, "/") {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("模块名含斜杠"))
	}
	f.created = append(f.created, req)
	m := api.LocalModule{Name: req.Name, Dir: req.Dir, Mode: "supervised", RoundCap: 8, State: "未接入"}
	f.modules = append(f.modules, m)
	return m, nil
}
func (f *fakeLocal) PatchModule(name string, p api.LocalModulePatch) (api.LocalModule, error) {
	if p.Name == "taken" {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("名字已存在"))
	}
	f.patched = append(f.patched, p)
	return api.LocalModule{Name: name}, nil
}
func (f *fakeLocal) CloseModule(name string) error {
	if name == "nope" {
		return errors.Join(ErrLocalInvalid, errors.New("不存在"))
	}
	f.closed = append(f.closed, name)
	return nil
}
func (f *fakeLocal) ReopenModule(name string) error { f.reopened = append(f.reopened, name); return nil }
func (f *fakeLocal) DeleteModule(name string, files bool) error {
	if f.deleted == nil {
		f.deleted = map[string]bool{}
	}
	f.deleted[name] = files
	return nil
}
func (f *fakeLocal) Conversations(side, dir string) ([]api.LocalConversation, error) {
	return []api.LocalConversation{{ID: side + "-1", Name: "对话", Cwd: dir, UpdatedAt: time.Now()}}, nil
}
func (f *fakeLocal) AttachModule(name string, req api.LocalAttachRequest) (api.LocalModule, error) {
	if req.Side != "codex" {
		return api.LocalModule{}, errors.Join(ErrLocalInvalid, errors.New("只有 codex 侧需要登记"))
	}
	f.attached = append(f.attached, req)
	return api.LocalModule{Name: name, Codex: api.LocalSide{Attached: true, ThreadName: "对话"}}, nil
}
func (f *fakeLocal) Redeliver(name string) error { f.redeliver = append(f.redeliver, name); return nil }
func (f *fakeLocal) Settings() (api.LocalSettings, error) { return f.set, nil }
func (f *fakeLocal) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode == "yolo" {
		return errors.Join(ErrLocalInvalid, errors.New("模式无效"))
	}
	f.set = s
	return nil
}
func (f *fakeLocal) State() api.LocalState { return api.LocalState{Version: "test", CodexOK: true} }

func newLocalTestServer(t *testing.T) (*httptest.Server, *Server, *fakeLocal, map[string]*store.User) {
	t.Helper()
	ts, st, users := newTestServer(t)
	ts.Close()
	f := &fakeLocal{set: api.LocalSettings{CodexPath: "/x", DefaultMode: "supervised", DefaultCap: 8}}
	srv := New(st, "http://relais.test", t.TempDir())
	srv.SetLocal(f, "hou")
	ts2 := httptest.NewServer(srv.Handler())
	t.Cleanup(ts2.Close)
	return ts2, srv, f, users
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, ts.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestLocalRoutesAbsentWithoutManager(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, p := range []string{"/api/local/modules", "/api/local/state", "/local/app.js"} {
		resp, _ := http.Get(ts.URL + p)
		if resp.StatusCode == 200 {
			t.Fatalf("线上不该有 %s: %d", p, resp.StatusCode)
		}
	}
	resp, _ := http.Get(ts.URL + "/")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), `id="login-view"`) {
		t.Fatal("线上首页应仍是联网页面（含登录）")
	}
}

func TestLocalRoutesLoopbackOnly(t *testing.T) {
	ts, srv, _, _ := newLocalTestServer(t)
	if r := do(t, ts, "GET", "/api/local/modules", nil); r.StatusCode != 200 {
		t.Fatalf("回环免钥匙应 200: %d", r.StatusCode)
	}
	req := httptest.NewRequest("GET", "/api/local/modules", nil)
	req.RemoteAddr = "10.0.0.5:4321"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("非回环应 401: %d", rec.Code)
	}
	if r := do(t, ts, "POST", "/api/local/heartbeat", nil); r.StatusCode != 404 {
		t.Fatalf("心跳路由应已删除: %d", r.StatusCode)
	}
	if r := do(t, ts, "GET", "/api/local/modules/x/rules", nil); r.StatusCode != 404 {
		t.Fatalf("规矩路由应已删除: %d", r.StatusCode)
	}
}

func TestLocalModuleRoutes(t *testing.T) {
	ts, _, f, _ := newLocalTestServer(t)
	if r := do(t, ts, "POST", "/api/local/modules", api.LocalModuleRequest{Name: "m", Dir: "/Users/x/proj", CodexThread: "t1"}); r.StatusCode != 200 {
		t.Fatalf("create: %d", r.StatusCode)
	}
	if len(f.created) != 1 || f.created[0].CodexThread != "t1" {
		t.Fatalf("create 参数没传全: %+v", f.created)
	}
	if r := do(t, ts, "POST", "/api/local/modules", api.LocalModuleRequest{Name: "a/b", Dir: "/x"}); r.StatusCode != 400 {
		t.Fatalf("invalid 应 400: %d", r.StatusCode)
	}
	if r := do(t, ts, "PATCH", "/api/local/modules/m", api.LocalModulePatch{Name: "m2", RoundCap: 10}); r.StatusCode != 200 || f.patched[0].RoundCap != 10 {
		t.Fatalf("patch: %d %+v", r.StatusCode, f.patched)
	}
	if r := do(t, ts, "PATCH", "/api/local/modules/m", api.LocalModulePatch{Name: "taken"}); r.StatusCode != 400 {
		t.Fatalf("重名应 400: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/close", nil); r.StatusCode != 204 || f.closed[0] != "m" {
		t.Fatalf("close: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/reopen", nil); r.StatusCode != 204 || f.reopened[0] != "m" {
		t.Fatalf("reopen: %d", r.StatusCode)
	}
	if r := do(t, ts, "DELETE", "/api/local/modules/m?files=1", nil); r.StatusCode != 204 || !f.deleted["m"] {
		t.Fatalf("delete files=1: %d %v", r.StatusCode, f.deleted)
	}
	if r := do(t, ts, "DELETE", "/api/local/modules/n", nil); r.StatusCode != 204 || f.deleted["n"] {
		t.Fatalf("delete 默认不删文件: %d %v", r.StatusCode, f.deleted)
	}
	r := do(t, ts, "GET", "/api/local/conversations?side=codex&dir=/Users/x/proj", nil)
	var convs []api.LocalConversation
	json.NewDecoder(r.Body).Decode(&convs)
	if r.StatusCode != 200 || len(convs) != 1 || convs[0].ID != "codex-1" {
		t.Fatalf("conversations: %d %+v", r.StatusCode, convs)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/attach", api.LocalAttachRequest{Side: "codex", Thread: "t9"}); r.StatusCode != 200 || f.attached[0].Thread != "t9" {
		t.Fatalf("attach: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/attach", api.LocalAttachRequest{Side: "claude"}); r.StatusCode != 400 {
		t.Fatalf("claude attach 应 400: %d", r.StatusCode)
	}
	if r := do(t, ts, "POST", "/api/local/modules/m/redeliver", nil); r.StatusCode != 204 || f.redeliver[0] != "m" {
		t.Fatalf("redeliver: %d", r.StatusCode)
	}
	if r := do(t, ts, "PUT", "/api/local/settings", api.LocalSettings{CodexPath: "/c", DefaultMode: "autopilot", DefaultCap: 6, NotifyEveryLetter: true}); r.StatusCode != 204 || f.set.DefaultCap != 6 || !f.set.NotifyEveryLetter {
		t.Fatalf("settings: %d %+v", r.StatusCode, f.set)
	}
	if r := do(t, ts, "PUT", "/api/local/settings", api.LocalSettings{DefaultMode: "yolo"}); r.StatusCode != 400 {
		t.Fatalf("坏设置应 400: %d", r.StatusCode)
	}
	r = do(t, ts, "GET", "/api/local/state", nil)
	var stt api.LocalState
	json.NewDecoder(r.Body).Decode(&stt)
	if r.StatusCode != 200 || stt.Version != "test" || !stt.CodexOK {
		t.Fatalf("state: %d %+v", r.StatusCode, stt)
	}
}

func TestLocalPagesServedAtRoot(t *testing.T) {
	ts, _, _, _ := newLocalTestServer(t)
	resp, _ := http.Get(ts.URL + "/")
	b, _ := io.ReadAll(resp.Body)
	s := string(b)
	if resp.StatusCode != 200 || !strings.Contains(s, "/local/app.js") || strings.Contains(s, `id="login-view"`) {
		t.Fatalf("本地首页应是控制台、无登录: %d", resp.StatusCode)
	}
	for _, p := range []string{"/local/app.js", "/local/style.css", "/vendor/marked.min.js"} {
		resp, _ := http.Get(ts.URL + p)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", p, resp.StatusCode)
		}
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/server/ 2>&1 | head -5`
Expected: 编译失败（接口/类型不匹配）。

- [ ] **Step 4: 改 api 类型**

删除旧 `LocalModule`、`LocalSettings`、`LocalRules`，加入 Interfaces 里列出的全部类型（`LocalRepo`、`LocalModuleRequest` 按上面定义替换）。

- [ ] **Step 5: 改 `auth.go`**

在 `auth` 的 `writeErr(w, http.StatusUnauthorized, "未登录")` 之前插入：

```go
		if s.local != nil && s.localHumanUser != "" && isLoopback(r.RemoteAddr) {
			if u, err := s.st.UserByName(s.localHumanUser); err == nil {
				h(w, r, principal{user: u, agent: false})
				return
			}
		}
```

`Server` 结构体加字段 `localHumanUser string`；删除 `beats heartbeats`。

- [ ] **Step 6: 重写 `local.go`**

保留 `ErrLocalInvalid`、`isLoopback`、`localErr`；删除 `heartbeats`；`SetLocal(m LocalManager, humanUser string)` 设两个字段；`localOnly` 替换 `localHuman`（只检查回环，非回环回 401 `"本地控制台只接受本机请求"`）；按 Interfaces 的路由表逐个写 handler（形状与现有一致：decode → 调接口 → `localErr`/`writeJSON`/204）。`DELETE` 的 `files` 取 `r.URL.Query().Get("files") == "1"`。`GET /api/local/conversations` 的 `side` 不是 `claude|codex` → 400。

新增：

```go
// PublishMessage：守卫入库的消息推到 SSE（与 handleSend 同一出口）。
func (s *Server) PublishMessage(channelID int64, m *store.Message, channelName string) {
	s.publish(channelID, toAPI(m, channelName, false))
}
```

- [ ] **Step 7: 改 `server.go` 页面路由**

在 `if s.local != nil { s.registerLocalRoutes(mux) }` 里追加：

```go
		localFS, err := fs.Sub(webFS, "web/local")
		if err != nil {
			panic(err)
		}
		mux.Handle("GET /local/", s.cacheStatic(http.StripPrefix("/local/", http.FileServerFS(localFS))))
		serveLocalIndex := s.cacheStatic(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			data, err := webFS.ReadFile("web/local/index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
		}))
		mux.Handle("GET /{$}", serveLocalIndex)
		mux.Handle("GET /index.html", serveLocalIndex)
```

`GET /{$}` 只匹配根路径（Go 1.22+ 模式），比 `GET /` 更具体，Go mux 会优先它；原 `mux.Handle("GET /", …)` 保留给其它静态文件。先在 `web/local/` 放三份占位文件（`index.html` 含 `<script src="/local/app.js">` 与 `<link href="/local/style.css">`，无 `login-view`），Task 12 再写真页面。`static.go` 的 `Version` 改 `0.7.0-m9`（`web_test.go` 若断言版本号一并改）。

- [ ] **Step 8: 跑测试**

Run: `go test ./internal/server/`
Expected: PASS。`web_test.go` 的 `TestStaticPages` 用的是 `newTestServer`（线上），应不受影响。

- [ ] **Step 9: 提交**

```bash
git add internal/api internal/server
git commit -m "feat(server): 本地模式回环免钥匙、本地路由重写（改名/重开/删除/对话列表/接入/重投）、本地页面路由、SSE 出口（M9 Task 8）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 9: cli 模块层重写（`localmgr.go`）：登记表、协议、指针块、接入、设置

**Files:**
- Modify: `internal/cli/localmgr.go`（重写）、`internal/cli/localmgr_test.go`（重写）
- Delete: `internal/cli/localhook.go`、`localhook_test.go`、`localprompt.go`、`localprompt_test.go`、`localturn_test.go`、`session.go`、`session_test.go`、`conclusion.go`、`conclusion_test.go`；`internal/guide/guide.go` 里的 `LocalText` 与其测试
- Modify: `main.go`（去掉 `session`、`local-prompt`、`conclusion` 三个 case 与用法串）、`internal/cli/bridge.go`（删 `c.Heartbeat()` 调用）、`internal/cli/client.go`（删 `Heartbeat`）、`internal/cli/bridge_test.go`（删 `TestHeartbeat*` 两个测试）、`internal/cli/local.go`（`runLocalInit` 去 `--claude/--codex`；`runLocalStatus` 改用 `ListModules` 新字段；`bootstrap` 签名改）

**Interfaces:**
- Consumes: Task 2 store 方法；Task 1/5/6/7 的 `local` 包。
- Produces: `localManager` 实现 Task 8 的 `server.LocalManager`；另有
  ```go
  func (m *localManager) bootstrap(listen string) (bootstrapResult, error)   // 不再收 agent 路径；bootstrapResult 去掉密码字段：只剩 BaseURL、HumanUser
  func (m *localManager) migrateProjectsToml() error                        // sides/claude/projects.toml → local_modules（一次性，幂等）
  func (m *localManager) moduleRefs() ([]local.ModuleRef, error)            // 守卫用：未关闭模块
  func (m *localManager) users() (map[string]int64, error)
  func detectCodex() string                                                  // PATH → /Applications/ChatGPT.app/Contents/Resources/codex → ~/.codex/plugins/.plugin-appserver/codex
  func NewDaemon(ld string, publish func(int64, *store.Message, string)) (*local.Daemon, error) // RunServe 用
  ```
  `localManager` 新增字段 `startedAt time.Time`、`daemon *local.Daemon`（`Redeliver` 用；`NewDaemon` 设置）。

**行为：**
- `CreateModule(req)`：校验（`local.ValidModuleName`、目录绝对且存在、同名模块已绑别的目录 → invalid）；建频道 + 三成员 + `SetAutoEnabled(chID, true, 2*defaultCap)` + `SetMode(默认)`；`UpsertLocalModule`；`local.EnsureMailDir`；`local.WriteProtocol`（`ErrProtocolCustom` 只记日志不报错）；`local.EnsurePointer(CLAUDE.md)`、`EnsurePointer(AGENTS.md)`；`req.CodexThread != ""` → 同 `AttachModule`。返回 `moduleInfo`。
- `moduleInfo(lm store.LocalModule)`：`GetAuto` → mode/round/cap/needs_human；`local.ListLetters` → `LastSeq/LastFrom/LastAt`（取最后一封非 kickoff）；`local.ReadSideStatus(md, local.PIDAlive)` → `Claude/Codex`；`LastDelivery(chID,"codex")` → `Codex.LastDelivery/DeliveryError/DeliveryAt`；`Claude.SessionName` 用 `local.ListClaudeSessions(home, "")` 按 `ClaudeSessionID` 找名字；`PendingConclusion`：`a.Resolved` 时从 `a.ResolutionMsgID` 的 seq 生成（`AwaitingConfirm = mode==supervised`）；`Rejected`：`outbox/*.rejected` 文件名；`State` 按 spec §7.4 顺序：`Closed` → `已关闭`；`a.KickedOff` → `已开工`；`a.Resolved` → `已握手`（同时进 `等你` 判定：supervised 时 `State=等你`）；`NeedsHumanQ != ""` 或 `Codex.DeliveryError != ""` 或 `len(Rejected)>0` → `等你`；两侧都没接（`!Claude.Waiting && !Codex.Attached`）且 `LastSeq==0` → `未接入`；否则 `讨论中`。`WaitingFor`：`a.Resolved` → owner；`NeedsHumanQ` → `user`；最后一封是 hou → `local.FirstResponder`；最后一封是 agent → 另一侧；无信 → `""`。
- `PatchModule`：`Name` 非空且不同 → `ValidModuleName` + `RenameChannel`（`ErrChannelExists` → invalid）+ `os.Rename(MailDir(dir, old), MailDir(dir, new))`（目标已存在 → invalid，且回滚频道名）；`Mode` → `SetMode`；`RoundCap>0` → `SetAutoEnabled(chID, true, 2*RoundCap)` **会清零 round_count**——改用直接 SQL 不合适，故在 store 加 `SetCap(channelID, cap int)`（`UPDATE channel_auto SET cap=?`；Task 2 漏了，这里补上并加一条单测）。
- `CloseModule`：`CloseChannel` + `SetLocalClosed(now)`。`ReopenModule`：`ReopenChannel` + `SetLocalClosed("")`。
- `DeleteModule(name, files)`：`DeleteChannel`；`files` 时 `os.RemoveAll(MailDir)`（先确认路径在 `<dir>/relais/mail/` 下）。
- `Conversations(side, dir)`：`codex` → `local.ListCodexThreads(codexHome(), dir)`；`claude` → `local.ListClaudeSessions(home, dir)`；错误 → 空数组 + 返回错误由 handler 变成 `{"error": …}`？——简化：返回 `nil, err`，handler 回 200 空数组并把错误写 `X-Relais-Error` 头；页面读该头显示提示。（在 Task 8 的 handler 里实现：`if err != nil { w.Header().Set("X-Relais-Error", err.Error()); writeJSON(200, []) }`。）
- `AttachModule(name, req)`：`Side != "codex"` → invalid；`Thread` 为空 → `FindCodexThread(codexHome, dir)`；否则在 `ListCodexThreads(codexHome, "")` 里按 `ID` 或 `Name` 精确匹配，匹配不到 → invalid；`local.WriteAttach` + `SetCodexAttach`。
- `Redeliver(name)`：`m.daemon == nil` → 错误；否则 `m.daemon.Redeliver(chID)`。
- `Settings/PutSettings`：键 `local.codex_path`（空时 `detectCodex()`）、`local.default_mode`、`local.default_cap`（默认 8）、`local.notify_every_letter`；`CodexOK` = 路径存在且可执行；`PutSettings` 校验模式与 cap>0，路径非空时必须存在。
- `State()`：`Version`（从 `server.Version`）、`startedAt`、`CodexOK`。
- `bootstrap(listen)`：server.toml（同前）；三账号（密码随机、不再写 `human.txt`、不返回密码）；`local.codex_path` 为空时 `detectCodex()`；`default_mode/default_cap` 默认；`migrateProjectsToml()`；对每个登记模块 `EnsureMailDir` + `WriteProtocol` + `EnsurePointer`×2；不再写 `sides/`。
- `migrateProjectsToml`：读 `sides/claude/projects.toml`（用现有 `loadProjectsIn`）；每条 `ChannelByName` 成功且 `LocalModuleByName` 为 `ErrNoRows` → `UpsertLocalModule` + `SetArchivedSeq(chID, 该频道 MAX(seq))`（store 加 `MaxSeq(channelID)`，一并补单测）。
- `NewDaemon(ld, publish)`：`newLocalManager(ld)` → `open()` 一个长期 store 句柄；`Daemon{Store, Modules: m.moduleRefs, CodexPath: 读设置, Publish: publish, Notify: notifyDesktop 包装（title 作 from）, Users: m.users(), Log: slog.Default()}`；`m.daemon = d`。`RunServe` 里对 `SetLocal(mgr, localHuman)` 与 `NewDaemon` 用同一个 `mgr`（`newLocalManager` 返回的指针）。

- [ ] **Step 1: 重写 `localmgr_test.go`**

保留 `TestScanRepos`、`TestCreateModulePermissionDenied`（改调用签名）与 `TestBootstrapIdempotentAndWritesLocalDir`（去掉 agent 路径断言，改断言：不写 `sides/`、不写 `human.txt`、`local.codex_path` 有值或为空但不报错）；删除 Guide/Rules 相关测试；新增：

```go
func TestCreateModuleWritesMailboxProtocolPointer(t *testing.T) {
	ld, mgr := bootstrapped(t) // 现有测试里的 helper：临时 ld + bootstrap(listen) 完成
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, "CLAUDE.md"), []byte("# 项目\n"), 0o644)
	m, err := mgr.CreateModule(api.LocalModuleRequest{Name: "黑客松", Dir: proj})
	if err != nil {
		t.Fatal(err)
	}
	if m.State != "未接入" || m.RoundCap != 8 || m.Mode != "supervised" {
		t.Fatalf("初始状态: %+v", m)
	}
	for _, p := range []string{"relais/mail/黑客松/outbox", "relais/mail/黑客松/drafts", "relais/PROTOCOL.md", "CLAUDE.md", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(proj, p)); err != nil {
			t.Fatalf("缺 %s", p)
		}
	}
	cl, _ := os.ReadFile(filepath.Join(proj, "CLAUDE.md"))
	if !strings.HasPrefix(string(cl), "# 项目\n") || !strings.Contains(string(cl), "relais/PROTOCOL.md") || strings.Contains(string(cl), "relais/AGENT.md") {
		t.Fatalf("指针块: %q", cl)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "AGENT.md")); err == nil {
		t.Fatal("不再生成 AGENT.md")
	}
	if _, err := os.Stat(filepath.Join(ld, "sides")); err == nil {
		t.Fatal("不再生成 sides/")
	}
	// 幂等 + 同名换目录拒绝
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "黑客松", Dir: proj}); err != nil {
		t.Fatal("重复创建应幂等")
	}
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "黑客松", Dir: t.TempDir()}); err == nil {
		t.Fatal("同名换目录应拒绝")
	}
	mods, _ := mgr.ListModules()
	if len(mods) != 1 || mods[0].Name != "黑客松" {
		t.Fatalf("ListModules: %+v", mods)
	}
}

func TestPatchRenameMovesMailDirAndRejectsTaken(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "a", Dir: proj})
	mgr.CreateModule(api.LocalModuleRequest{Name: "b", Dir: proj})
	os.WriteFile(filepath.Join(proj, "relais", "mail", "a", "001-hou.md"), []byte("---\nseq: 1\nfrom: hou\nkind: letter\n---\n\nx"), 0o644)
	if _, err := mgr.PatchModule("a", api.LocalModulePatch{Name: "b"}); err == nil {
		t.Fatal("重名应拒绝")
	}
	m, err := mgr.PatchModule("a", api.LocalModulePatch{Name: "c", Mode: "autopilot", RoundCap: 3})
	if err != nil || m.Name != "c" || m.Mode != "autopilot" || m.RoundCap != 3 || m.LastSeq != 1 {
		t.Fatalf("patch: %+v %v", m, err)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "c", "001-hou.md")); err != nil {
		t.Fatal("信箱目录应随改名移动")
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "a")); err == nil {
		t.Fatal("旧目录应不在")
	}
}

func TestCloseReopenDeleteModule(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	mgr.CloseModule("m")
	mods, _ := mgr.ListModules()
	if mods[0].State != "已关闭" || !mods[0].Closed {
		t.Fatalf("close: %+v", mods[0])
	}
	mgr.ReopenModule("m")
	mods, _ = mgr.ListModules()
	if mods[0].Closed {
		t.Fatal("reopen")
	}
	if err := mgr.DeleteModule("m", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "m")); err != nil {
		t.Fatal("默认不删文件")
	}
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	mgr.DeleteModule("m", true)
	if _, err := os.Stat(filepath.Join(proj, "relais", "mail", "m")); err == nil {
		t.Fatal("files=1 应删信箱目录")
	}
	if mods, _ := mgr.ListModules(); len(mods) != 0 {
		t.Fatal("删除后列表应空")
	}
}

func TestAttachAndConversations(t *testing.T) {
	_, mgr := bootstrapped(t)
	proj := t.TempDir()
	t.Setenv("CODEX_HOME", fakeCodexHomeFor(t, proj)) // 与 internal/local/attach_test.go 同样的假 state_5.sqlite，这里复制一个小 helper
	t.Setenv("HOME", t.TempDir())
	mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj})
	convs, err := mgr.Conversations("codex", proj)
	if err != nil || len(convs) != 2 || convs[0].Name != "巡天主对话" {
		t.Fatalf("conversations: %+v %v", convs, err)
	}
	m, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "codex"})
	if err != nil || !m.Codex.Attached || m.Codex.ThreadName != "巡天主对话" {
		t.Fatalf("attach 默认取最新: %+v %v", m, err)
	}
	if _, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "codex", Thread: "t-old"}); err != nil {
		t.Fatal("按 id 接入")
	}
	if _, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "codex", Thread: "不存在"}); err == nil {
		t.Fatal("找不到应拒绝")
	}
	if _, err := mgr.AttachModule("m", api.LocalAttachRequest{Side: "claude"}); err == nil {
		t.Fatal("claude 侧不登记")
	}
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "n", Dir: proj, CodexThread: "t-old"}); err != nil {
		t.Fatal("创建时顺手接入")
	}
}

func TestSettingsAndState(t *testing.T) {
	_, mgr := bootstrapped(t)
	s, _ := mgr.Settings()
	if s.DefaultMode != "supervised" || s.DefaultCap != 8 {
		t.Fatalf("默认设置: %+v", s)
	}
	fake := filepath.Join(t.TempDir(), "codex")
	os.WriteFile(fake, []byte("#!/bin/sh\necho ok"), 0o755)
	if err := mgr.PutSettings(api.LocalSettings{CodexPath: fake, DefaultMode: "autopilot", DefaultCap: 5, NotifyEveryLetter: true}); err != nil {
		t.Fatal(err)
	}
	s, _ = mgr.Settings()
	if s.CodexPath != fake || !s.CodexOK || s.DefaultMode != "autopilot" || s.DefaultCap != 5 || !s.NotifyEveryLetter {
		t.Fatalf("settings: %+v", s)
	}
	if err := mgr.PutSettings(api.LocalSettings{CodexPath: "/nope/codex", DefaultMode: "supervised", DefaultCap: 8}); err == nil {
		t.Fatal("路径不存在应拒绝")
	}
	if st := mgr.State(); st.Version == "" || !st.CodexOK {
		t.Fatalf("state: %+v", st)
	}
}

func TestMigrateProjectsToml(t *testing.T) {
	ld, mgr := bootstrapped(t)
	proj := t.TempDir()
	// 模拟 M8 遗留：频道存在、sides/claude/projects.toml 有登记、local_modules 没有
	st, _, _ := mgr.open()
	ch, _ := st.CreateChannel("old")
	for _, n := range []string{"claude", "codex", "hou"} {
		u, _ := st.UserByName(n)
		st.AddMember(ch.ID, u.ID)
	}
	u, _ := st.UserByName("hou")
	st.SaveMessage(ch.ID, u.ID, nil, "旧信", "x", "")
	st.Close()
	os.MkdirAll(filepath.Join(ld, "sides", "claude"), 0o755)
	registerProjectIn(filepath.Join(ld, "sides", "claude"), "old", proj)
	if _, err := mgr.bootstrap("127.0.0.1:18080"); err != nil {
		t.Fatal(err)
	}
	mods, _ := mgr.ListModules()
	if len(mods) != 1 || mods[0].Name != "old" || mods[0].Dir != proj {
		t.Fatalf("应导入旧登记: %+v", mods)
	}
	st, _, _ = mgr.open()
	defer st.Close()
	lm, _ := st.LocalModuleByName("old")
	if lm.ArchivedSeq != 1 {
		t.Fatalf("旧信不重新归档，archived_seq 应为当前最大 seq: %d", lm.ArchivedSeq)
	}
	if _, err := os.Stat(filepath.Join(proj, "relais", "PROTOCOL.md")); err != nil {
		t.Fatal("升级应给旧模块写协议")
	}
}
```

`bootstrapped(t)` helper：`ld := t.TempDir(); mgr := newLocalManager(ld); if _, err := mgr.bootstrap("127.0.0.1:18080"); err != nil { t.Fatal(err) }; return ld, mgr`。`fakeCodexHomeFor` 照 `internal/local/attach_test.go` 的 `fakeCodexHome` 抄一份（cli 包不能引用 local 包的测试）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ 2>&1 | head -5`
Expected: 编译失败。

- [ ] **Step 3: 在 store 补 `SetCap`、`MaxSeq`（含单测）**

```go
func (s *Store) SetCap(channelID int64, cap int) error {
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, enabled, cap) VALUES (?,1,?) ON CONFLICT(channel_id) DO UPDATE SET cap=excluded.cap`, channelID, cap)
	return err
}
func (s *Store) MaxSeq(channelID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM messages WHERE channel_id=?`, channelID).Scan(&n)
	return n, err
}
```

单测：`SetCap` 后 `GetAuto().Cap` 变、`RoundCount` 不变；`MaxSeq` 空频道为 0。

- [ ] **Step 4: 删除旧文件与旧引用**

```bash
git rm internal/cli/localhook.go internal/cli/localhook_test.go internal/cli/localprompt.go internal/cli/localprompt_test.go internal/cli/localturn_test.go internal/cli/session.go internal/cli/session_test.go internal/cli/conclusion.go internal/cli/conclusion_test.go
```

`guide.go` 删 `LocalText`（及其 test）；`main.go` 删三个 case 并更新用法串（同时加 Task 10 的 `post|wait|attach`，这里先删）；`bridge.go` 删 `c.Heartbeat()` 行与相关注释；`client.go` 删 `Heartbeat`；`bridge_test.go` 删 `TestHeartbeatSilentOn404`、`TestHeartbeatKeepsSendingOn200`；`local.go` 删 `sessionGet` 用法（`runLocalStatus` 改打印 `Claude.Waiting`/`Codex.Attached`）、`runLocalInit` 去 `--claude/--codex`、`bootstrap` 调用改单参数；`local_test.go` 里引用 `--claude/--codex`、`human.txt`、密码的断言删除或改写（`TestLocalInitRequiresBothAgents` 删除；`TestLocalBootstrapCommandJSON` 改为只断言 `base_url`、`human_user` 两个键——`printBootstrapJSON` 同步改）。

- [ ] **Step 5: 重写 `localmgr.go`**

按「行为」一节实现。骨架：

```go
type localManager struct {
	ld, scPath string
	startedAt  time.Time
	daemon     *local.Daemon
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func detectCodex() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/Applications/ChatGPT.app/Contents/Resources/codex", filepath.Join(home, ".codex", "plugins", ".plugin-appserver", "codex")} {
		if st, err := os.Stat(p); err == nil && st.Mode()&0o111 != 0 {
			return p
		}
	}
	return ""
}
```

`moduleInfo` 里 `home` 取 `os.UserHomeDir()`（测试用 `t.Setenv("HOME", …)` 控制）。`NewDaemon` 的 Notify 包装：`func(title, body string) { notifyDesktop(title, body) }`（`notifyDesktop(from, summary)` 现有签名：第一个参数进标题）。

- [ ] **Step 6: 跑测试与地板**

Run: `go test ./internal/cli/ ./internal/store/ && ./scripts/check.sh`
Expected: 全绿（`check.sh` 里 `bash -n` 安装脚本仍是旧脚本，能过）。

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "refactor(cli): 本地模块层改为登记表 + 信箱 + 协议 + 接入；删除无头讨论脑、hook、会话登记、conclusion 子命令与心跳（M9 Task 9）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 10: agent 侧三条命令、`serve` 起守卫、子命令表

**Files:**
- Create: `internal/cli/localcmd.go`
- Modify: `main.go`（加 `post|wait|attach`）、`internal/cli/admin.go`（`RunServe`）、`internal/cli/local.go`（`runLocalStatus` 打印两侧状态）
- Test: `internal/cli/localcmd_test.go`、`internal/cli/local_test.go`（`RunServe` 起守卫的冒烟）

**Interfaces:**
- Consumes: `local.Post/Wait/FindModuleDir/DetectSide/FindCodexThread/ListCodexThreads/WriteAttach/Describe/ListLetters`；Task 9 的 `NewDaemon`。
- Produces:
  ```go
  func RunPost(args []string, stdout io.Writer) error   // relais post <模块> <文件> [--resolved --owner X | --ack | --needs-human] [--as side]
  func RunWait(args []string, stdout io.Writer) error   // relais wait <模块> [--as side] [--timeout 30m]
  func RunAttach(args []string, stdout io.Writer) error // relais attach <模块> [--as side] [--thread id|名]
  ```
  `main.go` 的 case 调 `cli.RunPost(args[1:], os.Stdout)` 等。

**行为：**
- 三条命令共用 `resolveSide(flag string) (string, error)`（`--as` 优先，否则 `local.DetectSide(os.Getenv)`）与 `resolveModule(name) (projectDir, mailDir string, err)`（`local.FindModuleDir(cwd, name)`）。
- `RunPost`：`local.Post` → 打印 `已交给 relais（模块 X，来自 claude）。`；claude 侧加一行 `现在在后台运行：relais wait X`。
- `RunWait`：`local.Wait(ctx(SIGINT 取消), md, side, 1s, timeout, os.Getenv("CLAUDE_CODE_SESSION_ID"))` → 对每封打印 `local.Describe(l, body, side, prior)`（prior = 该封之前的全部信；body 读文件取正文）；超时打印 `没等到新信，继续等请再运行一次：relais wait X` 并 exit 0（不算错误）。
- `RunAttach`：`side == "claude"` → 打印 `Claude 侧的接入就是在后台运行：relais wait X`，不落文件，返回 nil；`codex` → `--thread` 空时 `FindCodexThread(codexHome(), projectDir)`，非空时在 `ListCodexThreads(codexHome(), "")` 里按 `ID`/`Name` 精确匹配 → `WriteAttach` → 打印 `已接入 Codex 对话「<Name 或 Title>」（模块 X）。来信会直接出现在这个对话里。`
- `RunServe`：`cfg.LocalDir != ""` 时 `mgr := newLocalManager(cfg.LocalDir); srv.SetLocal(mgr, localHuman); d, err := NewDaemon(cfg.LocalDir, srv.PublishMessage); go d.Run(ctx)`，`ctx` 由 `signal.NotifyContext(SIGINT, SIGTERM)` 提供；`http.ListenAndServe` 前打印 `守卫已启动（每 2 秒扫一次 outbox）`。
- `runLocalStatus`：每行 `名称 模式 第 n/cap 回合 状态 claude:在等/没在等 codex:已接入「名」/未接入`。

- [ ] **Step 1: 写失败测试 `localcmd_test.go`**

```go
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/local"
)

func projectWithModule(t *testing.T) (string, string) {
	t.Helper()
	proj := t.TempDir()
	if err := local.EnsureMailDir(proj, "m"); err != nil {
		t.Fatal(err)
	}
	return proj, local.MailDir(proj, "m")
}

func TestRunPostFromSubdirDetectsSide(t *testing.T) {
	proj, md := projectWithModule(t)
	sub := filepath.Join(proj, "src")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "r.md"), []byte("回信\n"), 0o644)
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	os.Chdir(sub)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CODEX_HOME", "")
	var out bytes.Buffer
	if err := RunPost([]string{"m", "r.md"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "来自 claude") || !strings.Contains(out.String(), "relais wait m") {
		t.Fatalf("输出: %s", out.String())
	}
	items, _ := local.ScanOutbox(md)
	if len(items) != 1 || items[0].From != "claude" {
		t.Fatalf("outbox: %+v", items)
	}
	t.Setenv("CLAUDECODE", "")
	if err := RunPost([]string{"m", "r.md"}, &out); err == nil || !strings.Contains(err.Error(), "--as") {
		t.Fatalf("判断不出侧应提示 --as: %v", err)
	}
	out.Reset()
	if err := RunPost([]string{"m", "r.md", "--as", "codex", "--resolved", "--owner", "codex"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "relais wait") {
		t.Fatal("codex 侧不提示 wait")
	}
	if err := RunPost([]string{"nope", "r.md", "--as", "codex"}, &out); err == nil || !strings.Contains(err.Error(), "m") {
		t.Fatalf("模块不存在应列出已有模块: %v", err)
	}
}

func TestRunWaitPrintsDescriptions(t *testing.T) {
	proj, md := projectWithModule(t)
	os.WriteFile(filepath.Join(md, "001-codex.md"), local.RenderLetter(local.Letter{Seq: 1, From: "codex", Kind: "letter"}, "x"), 0o644)
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	os.Chdir(proj)
	var out bytes.Buffer
	if err := RunWait([]string{"m", "--as", "claude"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "第 1 封 来自 codex") || !strings.Contains(out.String(), "001-codex.md") {
		t.Fatalf("输出: %s", out.String())
	}
	out.Reset()
	if err := RunWait([]string{"m", "--as", "claude", "--timeout", "50ms"}, &out); err != nil {
		t.Fatalf("超时不算错误: %v", err)
	}
	if !strings.Contains(out.String(), "没等到新信") {
		t.Fatalf("超时提示: %s", out.String())
	}
}

func TestRunAttach(t *testing.T) {
	proj, md := projectWithModule(t)
	t.Setenv("CODEX_HOME", fakeCodexHomeFor(t, proj))
	wd, _ := os.Getwd()
	t.Cleanup(func() { os.Chdir(wd) })
	os.Chdir(proj)
	var out bytes.Buffer
	if err := RunAttach([]string{"m", "--as", "codex"}, &out); err != nil {
		t.Fatal(err)
	}
	a, ok := local.ReadAttach(md)
	if !ok || a.ThreadID != "t-new" || !strings.Contains(out.String(), "巡天主对话") {
		t.Fatalf("attach: %+v %s", a, out.String())
	}
	out.Reset()
	if err := RunAttach([]string{"m", "--as", "codex", "--thread", "t-old"}, &out); err != nil {
		t.Fatal(err)
	}
	if a, _ := local.ReadAttach(md); a.ThreadID != "t-old" {
		t.Fatal("--thread 按 id")
	}
	if err := RunAttach([]string{"m", "--as", "codex", "--thread", "没有"}, &out); err == nil {
		t.Fatal("找不到应报错")
	}
	out.Reset()
	if err := RunAttach([]string{"m", "--as", "claude"}, &out); err != nil || !strings.Contains(out.String(), "relais wait m") {
		t.Fatalf("claude 侧只提示 wait: %v %s", err, out.String())
	}
}
```

- [ ] **Step 2: 写 `RunServe` 起守卫的测试（追加到 `local_test.go`）**

```go
func TestRunServeStartsDaemonInLocalMode(t *testing.T) {
	ld := t.TempDir()
	mgr := newLocalManager(ld)
	if _, err := mgr.bootstrap("127.0.0.1:18092"); err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if _, err := mgr.CreateModule(api.LocalModuleRequest{Name: "m", Dir: proj}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = RunServe([]string{"--config", localServerConfigPath(ld)}) }()
	waitListen("127.0.0.1:18092", 5*time.Second)
	md := local.MailDir(proj, "m")
	os.WriteFile(filepath.Join(md, "drafts", "a.md"), []byte("第一封\n"), 0o644)
	if _, err := local.Post(md, "claude", filepath.Join(md, "drafts", "a.md"), local.PostOpts{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(md, "001-claude.md")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("守卫应在几秒内归档 outbox 里的信")
		}
		time.Sleep(200 * time.Millisecond)
	}
	resp, err := http.Get("http://127.0.0.1:18092/api/local/modules")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("回环免钥匙: %v", err)
	}
	var mods []api.LocalModule
	json.NewDecoder(resp.Body).Decode(&mods)
	if len(mods) != 1 || mods[0].LastSeq != 1 || mods[0].LastFrom != "claude" || mods[0].WaitingFor != "codex" {
		t.Fatalf("状态: %+v", mods)
	}
}
```

（`RunServe` 无法优雅停止——测试进程结束即释放端口；端口 18092 与其它测试错开。）

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/cli/ -run 'TestRunPost|TestRunWait|TestRunAttach|TestRunServeStartsDaemon' 2>&1 | head -3`
Expected: 未定义符号。

- [ ] **Step 4: 实现 `localcmd.go`**

```go
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/hou-physics/relais/internal/local"
)

func resolveSide(as string) (string, error) {
	if as != "" {
		if as != "claude" && as != "codex" {
			return "", fmt.Errorf("--as 只能是 claude 或 codex")
		}
		return as, nil
	}
	return local.DetectSide(os.Getenv)
}

func resolveModule(name string) (string, string, error) {
	if name == "" {
		return "", "", fmt.Errorf("缺模块名")
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	proj, err := local.FindModuleDir(wd, name)
	if err != nil {
		return "", "", err
	}
	return proj, local.MailDir(proj, name), nil
}

func RunPost(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("post", flag.ContinueOnError)
	as := fs.String("as", "", "claude|codex（默认按环境判断）")
	resolved := fs.Bool("resolved", false, "提议收敛")
	owner := fs.String("owner", "", "承接方 claude|codex|user（配 --resolved）")
	ack := fs.Bool("ack", false, "附和对方最近一封收敛提议")
	needsHuman := fs.Bool("needs-human", false, "需要雇主定夺")
	fs.SetOutput(io.Discard)
	// 允许标志放在位置参数之后：先拆出前两个非标志参数
	var pos []string
	var rest []string
	for _, a := range args {
		if len(pos) < 2 && len(a) > 0 && a[0] != '-' {
			pos = append(pos, a)
		} else {
			rest = append(rest, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("用法: relais post <模块> <文件> [--resolved --owner claude|codex|user | --ack | --needs-human] [--as claude|codex]")
	}
	side, err := resolveSide(*as)
	if err != nil {
		return err
	}
	_, md, err := resolveModule(pos[0])
	if err != nil {
		return err
	}
	if _, err := local.Post(md, side, pos[1], local.PostOpts{Resolved: *resolved, Owner: *owner, Ack: *ack, NeedsHuman: *needsHuman}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已交给 relais（模块 %s，来自 %s）。\n", pos[0], side)
	if side == "claude" {
		fmt.Fprintf(stdout, "现在在后台运行：relais wait %s\n", pos[0])
	}
	return nil
}

func RunWait(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	as := fs.String("as", "", "claude|codex（默认按环境判断）")
	timeout := fs.Duration("timeout", 0, "最长等待（默认不超时）")
	fs.SetOutput(io.Discard)
	var pos, rest []string
	for _, a := range args {
		if len(pos) < 1 && len(a) > 0 && a[0] != '-' {
			pos = append(pos, a)
		} else {
			rest = append(rest, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("用法: relais wait <模块> [--timeout 30m] [--as claude|codex]")
	}
	side, err := resolveSide(*as)
	if err != nil {
		return err
	}
	_, md, err := resolveModule(pos[0])
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	letters, err := local.Wait(ctx, md, side, time.Second, *timeout, os.Getenv("CLAUDE_CODE_SESSION_ID"))
	if err == local.ErrWaitTimeout {
		fmt.Fprintf(stdout, "没等到新信，继续等请再运行一次：relais wait %s\n", pos[0])
		return nil
	}
	if err != nil {
		return err
	}
	all, _ := local.ListLetters(md)
	for _, l := range letters {
		data, _ := os.ReadFile(l.Path)
		_, body, _ := local.ParseLetter(data)
		var prior []local.Letter
		for _, p := range all {
			if p.Seq < l.Seq || (p.Seq == l.Seq && l.Kind == "kickoff" && p.Kind != "kickoff") {
				prior = append(prior, p)
			}
		}
		fmt.Fprintln(stdout, local.Describe(l, body, side, prior))
	}
	return nil
}

func RunAttach(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	as := fs.String("as", "", "claude|codex（默认按环境判断）")
	thread := fs.String("thread", "", "Codex 对话 id 或对话名（默认取当前目录最近活动的对话）")
	fs.SetOutput(io.Discard)
	var pos, rest []string
	for _, a := range args {
		if len(pos) < 1 && len(a) > 0 && a[0] != '-' {
			pos = append(pos, a)
		} else {
			rest = append(rest, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("用法: relais attach <模块> [--thread <id 或对话名>] [--as claude|codex]")
	}
	side, err := resolveSide(*as)
	if err != nil {
		return err
	}
	proj, md, err := resolveModule(pos[0])
	if err != nil {
		return err
	}
	if side == "claude" {
		fmt.Fprintf(stdout, "Claude 侧的接入就是在后台运行：relais wait %s\n", pos[0])
		return nil
	}
	var th local.Thread
	if *thread == "" {
		th, err = local.FindCodexThread(codexHome(), proj)
		if err != nil {
			return err
		}
	} else {
		all, err := local.ListCodexThreads(codexHome(), "")
		if err != nil {
			return err
		}
		found := false
		for _, t := range all {
			if t.ID == *thread || (t.Name != "" && t.Name == *thread) {
				th, found = t, true
				break
			}
		}
		if !found {
			return fmt.Errorf("Codex 里没有 id 或名字为 %q 的对话", *thread)
		}
	}
	name := th.Name
	if name == "" {
		name = th.Title
	}
	if err := local.WriteAttach(md, local.Attach{ThreadID: th.ID, Name: name, Cwd: th.Cwd, At: time.Now()}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "已接入 Codex 对话「%s」（模块 %s）。来信会直接出现在这个对话里。\n", name, filepath.Base(pos[0]))
	return nil
}
```

`ListCodexThreads(codexHome(), "")` 最多返回 20 条，按名字找旧对话可能漏——`--thread` 按 id 时改为直接查询：给 `local` 加 `func CodexThreadByIDOrName(codexHome, key string) (Thread, error)`（SQL `WHERE id=? OR name=?` 取 1 条），`RunAttach` 用它；`AttachModule`（Task 9）同样改用。补一条单测。

- [ ] **Step 5: `main.go` 与 `RunServe`**

`main.go` 用法串加 `post|wait|attach`；三个 case。`RunServe`：

```go
	srv := server.New(st, cfg.BaseURL, cfg.DataDir)
	if cfg.LocalDir != "" {
		mgr := newLocalManager(cfg.LocalDir)
		srv.SetLocal(mgr, localHuman)
		d, err := mgr.newDaemon(srv.PublishMessage)
		if err != nil {
			return err
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go d.Run(ctx)
		fmt.Printf("本地模式：控制台 %s，守卫每 2 秒扫一次 outbox（%s）\n", cfg.BaseURL, cfg.LocalDir)
	}
```

（Task 9 的 `NewDaemon(ld, publish)` 改为方法 `(m *localManager) newDaemon(publish)`，让 `SetLocal` 与守卫共用同一个 `mgr`，`Redeliver` 才找得到 `m.daemon`。）

- [ ] **Step 6: 跑测试与地板**

Run: `go test ./internal/cli/ && ./scripts/check.sh`
Expected: 全绿。

- [ ] **Step 7: 提交**

```bash
git add -A
git commit -m "feat(cli): relais post/wait/attach 三条纯文件命令；serve 在本地模式起守卫（M9 Task 10）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 11: 常驻只剩一个、安装脚本升级路径

**Files:**
- Modify: `internal/cli/local.go`（`installLocalServices`、新增 `uninstallOldBridges`、`runLocalBootstrap`）、`安装 Relais 本地模式.command`
- Test: `internal/cli/local_test.go`

**Interfaces:**
- Produces:
  ```go
  func installLocalServices(ld, scPath string) error          // 只装 com.relais.local.serve
  func uninstallOldBridges(launchctl func(args ...string) error, plistDir string) []string // 对 com.relais.local.bridge.{claude,codex}：存在则 bootout（失败忽略）并删文件；返回删掉的 label
  ```

- [ ] **Step 1: 写失败测试**

```go
func TestUninstallOldBridges(t *testing.T) {
	dir := t.TempDir()
	for _, l := range []string{"com.relais.local.bridge.claude", "com.relais.local.bridge.codex", "com.relais.local.serve"} {
		os.WriteFile(filepath.Join(dir, l+".plist"), []byte("<plist/>"), 0o644)
	}
	var calls [][]string
	removed := uninstallOldBridges(func(args ...string) error { calls = append(calls, args); return nil }, dir)
	if len(removed) != 2 {
		t.Fatalf("应卸两个 bridge: %v", removed)
	}
	for _, l := range []string{"com.relais.local.bridge.claude", "com.relais.local.bridge.codex"} {
		if _, err := os.Stat(filepath.Join(dir, l+".plist")); err == nil {
			t.Fatalf("%s.plist 应删除", l)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "com.relais.local.serve.plist")); err != nil {
		t.Fatal("serve 的 plist 不能动")
	}
	if len(calls) != 2 || calls[0][0] != "bootout" {
		t.Fatalf("应调用 launchctl bootout: %v", calls)
	}
	if again := uninstallOldBridges(func(args ...string) error { return nil }, dir); len(again) != 0 {
		t.Fatal("幂等")
	}
}

func TestInstallerScriptShape(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "安装 Relais 本地模式.command"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, must := range []string{"local bootstrap --json", "com.relais.local.serve", "kickstart", "open \"$BASE\""} {
		if !strings.Contains(s, must) {
			t.Fatalf("安装脚本缺 %q", must)
		}
	}
	for _, gone := range []string{"choose file", "--claude", "--codex", "human.txt", "密码", "bridge.claude", "bridge.codex"} {
		if strings.Contains(s, gone) {
			t.Fatalf("安装脚本不该再有 %q", gone)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/cli/ -run 'TestUninstallOldBridges|TestInstallerScriptShape' 2>&1 | head -3`
Expected: 未定义 / 断言失败。

- [ ] **Step 3: 实现**

```go
func uninstallOldBridges(launchctl func(args ...string) error, plistDir string) []string {
	var removed []string
	for _, side := range []string{"claude", "codex"} {
		label := "com.relais.local.bridge." + side
		p := filepath.Join(plistDir, label+".plist")
		if _, err := os.Stat(p); err != nil {
			continue
		}
		_ = launchctl("bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), label))
		_ = os.Remove(p)
		removed = append(removed, label)
	}
	return removed
}

func installLocalServices(ld, scPath string) error {
	relais, _ := os.Executable()
	home, _ := os.UserHomeDir()
	for _, l := range uninstallOldBridges(func(args ...string) error { return exec.Command("launchctl", args...).Run() }, filepath.Join(home, "Library", "LaunchAgents")) {
		fmt.Fprintf(os.Stderr, "已卸载旧常驻 %s（M9 起不再需要 bridge）\n", l)
	}
	label := "com.relais.local.serve"
	p := plistPathFor(label)
	if !plistNeedsInstall(p, relais) {
		fmt.Fprintf(os.Stderr, "常驻 %s 已存在，跳过\n", label)
		return nil
	}
	if _, err := os.Stat(p); err == nil {
		fmt.Fprintf(os.Stderr, "常驻 %s 指向旧二进制，重装\n", label)
	}
	_, err := installPlist(label, []string{relais, "serve", "--config", scPath}, map[string]string{"HOME": home, "PATH": os.Getenv("PATH")})
	return err
}
```

`runLocalBootstrap`：删 `--claude/--codex`；`printBootstrapJSON` 只输出 `base_url`、`human_user`。

安装脚本改为：

```bash
#!/bin/bash
# 双击安装 / 升级 Relais 本地模式（M9）。重复双击 = 升级，数据与模块不动。
set -euo pipefail
cd "$(dirname "$0")"

dialog() { osascript -e "display dialog \"$1\" buttons {\"好\"} default button 1 with title \"Relais 本地模式\"" >/dev/null 2>&1 || true; }
fail() { dialog "$1"; echo "错误: $1" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "没找到 Go。请先安装 Go（go.dev/dl）再双击本文件。"

BIN_DIR=""
for D in /opt/homebrew/bin /usr/local/bin; do
  if [ -d "$D" ] && [ -w "$D" ]; then BIN_DIR="$D"; break; fi
done
if [ -z "$BIN_DIR" ]; then BIN_DIR="$HOME/bin"; mkdir -p "$BIN_DIR"; fi
echo "编译 relais → $BIN_DIR/relais"
CGO_ENABLED=0 go build -o "$BIN_DIR/relais" . || fail "编译失败，详情见终端。"
RELAIS="$BIN_DIR/relais"

echo "配置本地模式…"
OUT="$("$RELAIS" local bootstrap --json)" || fail "bootstrap 失败，详情见终端。"
BASE="$(printf '%s' "$OUT" | sed -n 's/.*"base_url":"\([^"]*\)".*/\1/p')"

# 升级路径：常驻已存在时重启，让新二进制生效（旧的两个 bridge 常驻由 bootstrap 卸掉）
if [ -f "$HOME/Library/LaunchAgents/com.relais.local.serve.plist" ]; then
  launchctl kickstart -k "gui/$(id -u)/com.relais.local.serve" 2>/dev/null || true
fi

WHERE="relais 已装到 $BIN_DIR"
if [ "$BIN_DIR" = "$HOME/bin" ]; then WHERE="$WHERE（请确认它在 PATH 里）"; fi
dialog "完成。控制台：$BASE\n不需要登录，直接打开就是。\n$WHERE"
open "$BASE"
```

- [ ] **Step 4: 跑测试与地板**

Run: `go test ./internal/cli/ && ./scripts/check.sh`
Expected: 全绿。

- [ ] **Step 5: 提交**

```bash
git add -A
git commit -m "feat(install): 常驻只剩 serve，升级时卸掉两个 bridge；安装脚本不再选 agent 路径、不再显示密码（M9 Task 11）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 12: 本地控制台页面（`web/local/`）

**Files:**
- Create: `internal/server/web/local/index.html`、`internal/server/web/local/style.css`、`internal/server/web/local/app.js`（覆盖 Task 8 的占位文件）
- Modify: `scripts/check.sh`（加 `node --check internal/server/web/local/app.js`）
- Test: `internal/server/web_test.go`（新增 `TestLocalConsolePage`）

**Interfaces:**
- Consumes（全部同源 fetch，不带钥匙）：`GET /api/local/state`、`GET /api/local/modules`、`POST /api/local/modules`、`PATCH /api/local/modules/{n}`、`POST …/close|reopen|redeliver|attach`、`DELETE …?files=1`、`GET /api/local/conversations?side=&dir=`、`GET /api/local/repos`、`GET/PUT /api/local/settings`；频道：`GET /api/channels/{n}/messages`、`GET /api/messages/{id}`（正文）、`POST /api/channels/{n}/messages`（`{to:["claude","codex"], summary, body_md}`）、`GET /api/channels/{n}/auto`、`POST …/auto/kickoff`、`POST …/auto/resume`、`GET /api/events?channel={n}`（SSE，事件 `message`，data 为 `api.Message` JSON）。
- 规则：`index.html` 无 `login`/`invite`/`admin` 字样，无 `http://`/`https://`；只有中文；字体栈本机。

- [ ] **Step 1: 写失败测试（追加到 `web_test.go`）**

```go
func TestLocalConsolePage(t *testing.T) {
	ts, _, _, _ := newLocalTestServer(t)
	resp, _ := http.Get(ts.URL + "/")
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, want := range []string{`id="module-list"`, `id="now"`, `id="todo"`, `id="timeline"`, `id="composer"`, `id="new-module"`, `id="settings"`, `/local/app.js`, `/local/style.css`, `/vendor/marked.min.js`, `/vendor/purify.min.js`} {
		if !strings.Contains(s, want) {
			t.Fatalf("控制台缺 %q", want)
		}
	}
	for _, bad := range []string{"login", "invite", "admin", "http://", "https://", "data-i18n", "Deutsch"} {
		if strings.Contains(s, bad) {
			t.Fatalf("控制台不该含 %q", bad)
		}
	}
	resp, _ = http.Get(ts.URL + "/local/style.css")
	css, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"--ink: #010120", "--line: #ebebeb", "--mint: #c8f6f9", "#fc4c02", "#ef2cc1", "#bdbbff", "prefers-color-scheme: dark", "border-radius: 4px"} {
		if !strings.Contains(string(css), want) {
			t.Fatalf("样式缺 %q", want)
		}
	}
	if strings.Contains(string(css), "box-shadow") || strings.Contains(string(css), "@import") {
		t.Fatal("无阴影、不引外部样式")
	}
	resp, _ = http.Get(ts.URL + "/local/app.js")
	js, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"/api/local/modules", "/api/events", "auto/kickoff", "auto/resume", "redeliver", "conversations", "接入 relais 模块"} {
		if !strings.Contains(string(js), want) {
			t.Fatalf("脚本缺 %q", want)
		}
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/server/ -run TestLocalConsolePage`
Expected: FAIL（占位页缺 id）。

- [ ] **Step 3: 写 `index.html`**

```html
<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Relais · 本地</title>
<link rel="stylesheet" href="/local/style.css">
</head>
<body>
<header class="top">
  <div class="brand"><span class="eyebrow">RELAIS · 本地</span><span id="daemon-dot" class="dot" title="守卫"></span><span id="daemon-text" class="muted">连接中…</span></div>
  <div class="top-actions">
    <button class="btn ghost" id="open-new">新建模块</button>
    <button class="btn ghost" id="open-settings">设置</button>
  </div>
</header>

<main class="layout">
  <aside class="side">
    <div class="eyebrow">模块</div>
    <div id="module-list" class="module-list"></div>
    <div id="empty" class="empty" hidden>
      <p>还没有模块。</p>
      <button class="btn" id="empty-new">新建第一个模块</button>
    </div>
  </aside>

  <section class="main" id="main" hidden>
    <div class="card" id="now">
      <div class="row between"><div class="eyebrow">现在</div><div id="now-round" class="mono muted"></div></div>
      <h2 id="now-line"></h2>
      <div id="progress" class="progress" hidden><span></span></div>
      <div class="sides">
        <div class="side-row"><span class="eyebrow">CLAUDE</span><span id="side-claude"></span><button class="btn tiny ghost copy" data-copy-side="claude" hidden>复制接入指令</button></div>
        <div class="side-row"><span class="eyebrow">CODEX</span><span id="side-codex"></span><button class="btn tiny ghost copy" data-copy-side="codex" hidden>复制接入指令</button><button class="btn tiny ghost" id="pick-codex" hidden>从列表选</button></div>
      </div>
      <div id="codex-pick" class="pick" hidden></div>
    </div>

    <div class="card mint" id="todo" hidden>
      <div class="eyebrow">该你做</div>
      <div id="todo-body"></div>
    </div>

    <div class="card" id="letters">
      <div class="row between"><div class="eyebrow">往来</div><span id="letters-count" class="mono muted"></span></div>
      <div id="timeline" class="timeline"></div>
      <form id="composer" class="composer">
        <textarea id="compose-body" rows="4" placeholder="写给两侧的信。第一行会作为摘要。"></textarea>
        <div class="row between">
          <label class="muted">先回 <select id="compose-first"><option value="claude">Claude</option><option value="codex">Codex</option></select></label>
          <button class="btn" type="submit">发出</button>
        </div>
      </form>
    </div>

    <details class="card" id="module-settings">
      <summary class="eyebrow">本模块</summary>
      <div class="form">
        <label>名称 <input id="ms-name"></label>
        <label>模式 <select id="ms-mode"><option value="supervised">监督：握手后我确认才开工</option><option value="autopilot">甩手：握手即开工</option></select></label>
        <label>回合上限 <input id="ms-cap" type="number" min="1" max="99"></label>
        <div class="row gap">
          <button class="btn" id="ms-save">保存</button>
          <button class="btn ghost" id="ms-close">关闭模块</button>
          <button class="btn ghost" id="ms-reopen" hidden>重开模块</button>
          <button class="btn danger" id="ms-delete">删除</button>
        </div>
        <div class="muted small">目录：<span id="ms-dir" class="mono"></span></div>
      </div>
    </details>
  </section>
</main>

<dialog id="new-module">
  <form method="dialog" class="form" id="new-form">
    <div class="eyebrow">新建模块</div>
    <label>名字 <input id="nm-name" required placeholder="例如：黑客松巡天智能体"></label>
    <label>项目目录 <select id="nm-repo"></select></label>
    <label id="nm-dir-wrap" hidden>手填路径 <input id="nm-dir" placeholder="/Users/你/项目"></label>
    <label>现在接入 Codex 对话（可选） <select id="nm-codex"><option value="">稍后再说</option></select></label>
    <div class="row gap"><button class="btn" value="ok">创建</button><button class="btn ghost" value="cancel" formnovalidate>取消</button></div>
    <div id="nm-error" class="error"></div>
  </form>
</dialog>

<dialog id="settings">
  <form method="dialog" class="form" id="settings-form">
    <div class="eyebrow">设置</div>
    <label>codex 命令路径 <span class="row gap"><input id="st-codex" class="grow"><button class="btn tiny ghost" type="button" id="st-test">测试</button></span><span id="st-codex-ok" class="small muted"></span></label>
    <label>默认模式 <select id="st-mode"><option value="supervised">监督</option><option value="autopilot">甩手</option></select></label>
    <label>默认回合上限 <input id="st-cap" type="number" min="1" max="99"></label>
    <label class="row gap"><input type="checkbox" id="st-notify"> 每封信都弹桌面通知</label>
    <div class="row gap"><button class="btn" value="ok">保存</button><button class="btn ghost" value="cancel" formnovalidate>取消</button></div>
    <div id="st-error" class="error"></div>
  </form>
</dialog>

<script src="/vendor/marked.min.js"></script>
<script src="/vendor/purify.min.js"></script>
<script src="/local/app.js"></script>
</body>
</html>
```

- [ ] **Step 4: 写 `style.css`**

```css
:root {
  --canvas: #ffffff; --ink: #010120; --line: #ebebeb; --muted: #5c5c6b; --mint: #c8f6f9; --mint-ink: #010120;
  --danger: #b3261e; --grad: linear-gradient(90deg, #fc4c02, #ef2cc1, #bdbbff);
  --font: Inter, -apple-system, "PingFang SC", "Helvetica Neue", sans-serif;
  --mono: "JetBrains Mono", "SF Mono", Menlo, monospace;
  --s1: 4px; --s2: 8px; --s3: 12px; --s4: 16px; --s6: 24px; --s8: 32px;
}
@media (prefers-color-scheme: dark) {
  :root { --canvas: #010120; --ink: #ffffff; --line: #26264a; --muted: #a3a3b8; --mint: #c8f6f9; --mint-ink: #010120; }
}
* { box-sizing: border-box; }
html, body { margin: 0; background: var(--canvas); color: var(--ink); font-family: var(--font); font-weight: 400; letter-spacing: -0.01em; font-size: 15px; line-height: 1.5; }
h2 { font-size: 20px; font-weight: 500; margin: var(--s2) 0; }
.eyebrow { font-family: var(--mono); text-transform: uppercase; font-size: 11px; letter-spacing: 0.08em; color: var(--muted); }
.mono { font-family: var(--mono); font-size: 12px; }
.muted { color: var(--muted); }
.small { font-size: 12px; }
.row { display: flex; align-items: center; }
.row.between { justify-content: space-between; }
.row.gap { gap: var(--s2); flex-wrap: wrap; }
.grow { flex: 1; }
.top { display: flex; justify-content: space-between; align-items: center; padding: var(--s3) var(--s6); border-bottom: 1px solid var(--line); }
.brand { display: flex; align-items: center; gap: var(--s2); }
.dot { width: 8px; height: 8px; border-radius: 50%; background: var(--line); display: inline-block; }
.dot.on { background: var(--ink); }
.layout { display: grid; grid-template-columns: 260px 1fr; min-height: calc(100vh - 49px); }
.side { border-right: 1px solid var(--line); padding: var(--s4); }
.main { padding: var(--s4) var(--s6); display: flex; flex-direction: column; gap: var(--s4); max-width: 880px; }
.card { border: 1px solid var(--line); border-radius: 4px; padding: var(--s4); background: var(--canvas); }
.card.mint { background: var(--mint); color: var(--mint-ink); border-color: var(--mint); }
.card.mint .eyebrow { color: var(--mint-ink); opacity: .7; }
.module-list { display: flex; flex-direction: column; gap: var(--s1); margin-top: var(--s2); }
.module { border: 1px solid var(--line); border-radius: 4px; padding: var(--s2) var(--s3); cursor: pointer; display: flex; flex-direction: column; gap: 2px; }
.module.active { border-color: var(--ink); }
.module .name { font-weight: 500; }
.module .meta { display: flex; justify-content: space-between; align-items: center; }
.chip { font-family: var(--mono); text-transform: uppercase; font-size: 10px; letter-spacing: 0.08em; border: 1px solid var(--line); border-radius: 4px; padding: 1px 6px; color: var(--muted); }
.chip.you { background: var(--mint); color: var(--mint-ink); border-color: var(--mint); }
.chip.ink { background: var(--ink); color: var(--canvas); border-color: var(--ink); }
.btn { font: inherit; font-weight: 500; border-radius: 999px; padding: var(--s2) var(--s4); border: 1px solid var(--ink); background: var(--ink); color: var(--canvas); cursor: pointer; }
.btn.ghost { background: transparent; color: var(--ink); border-color: var(--line); }
.btn.danger { background: transparent; color: var(--danger); border-color: var(--line); }
.btn.tiny { padding: 2px var(--s3); font-size: 12px; }
.btn:disabled { opacity: .5; cursor: default; }
.progress { height: 3px; border-radius: 4px; background: var(--line); overflow: hidden; margin: var(--s2) 0; }
.progress span { display: block; height: 100%; width: 40%; background: var(--grad); animation: slide 1.6s linear infinite; }
@keyframes slide { from { transform: translateX(-100%); } to { transform: translateX(250%); } }
.sides { display: flex; flex-direction: column; gap: var(--s1); margin-top: var(--s2); }
.side-row { display: flex; gap: var(--s3); align-items: center; }
.pick { border-top: 1px solid var(--line); margin-top: var(--s2); padding-top: var(--s2); display: flex; flex-direction: column; gap: var(--s1); }
.pick button { text-align: left; }
.timeline { display: flex; flex-direction: column; gap: var(--s2); margin: var(--s3) 0; }
.letter { border: 1px solid var(--line); border-radius: 4px; padding: var(--s2) var(--s3); }
.letter .head { display: flex; gap: var(--s2); align-items: center; }
.letter .summary { margin-top: 2px; }
.letter .body { border-top: 1px solid var(--line); margin-top: var(--s2); padding-top: var(--s2); }
.letter .body pre { overflow: auto; }
.letter.from-hou { border-color: var(--ink); }
.composer { display: flex; flex-direction: column; gap: var(--s2); border-top: 1px solid var(--line); padding-top: var(--s3); }
textarea, input, select { font: inherit; color: var(--ink); background: var(--canvas); border: 1px solid var(--line); border-radius: 4px; padding: var(--s2); }
textarea { resize: vertical; }
.form { display: flex; flex-direction: column; gap: var(--s3); }
.form label { display: flex; flex-direction: column; gap: var(--s1); }
.form label.row { flex-direction: row; align-items: center; }
dialog { border: 1px solid var(--line); border-radius: 4px; padding: var(--s6); background: var(--canvas); color: var(--ink); width: min(520px, 92vw); }
dialog::backdrop { background: rgba(1, 1, 32, .35); }
.error { color: var(--danger); font-size: 13px; min-height: 1em; }
.empty { margin-top: var(--s4); display: flex; flex-direction: column; gap: var(--s2); }
details.card summary { cursor: pointer; }
details.card .form { margin-top: var(--s3); }
.todo-item { display: flex; flex-direction: column; gap: var(--s2); padding: var(--s2) 0; }
.todo-item + .todo-item { border-top: 1px solid rgba(1, 1, 32, .12); }
@media (max-width: 900px) {
  .layout { grid-template-columns: 1fr; }
  .side { border-right: 0; border-bottom: 1px solid var(--line); }
  .module-list { flex-direction: row; overflow-x: auto; }
  .module { min-width: 180px; }
  .main { padding: var(--s4); }
}
```

- [ ] **Step 5: 写 `app.js`**

```js
"use strict";
const $ = (id) => document.getElementById(id);
let modules = [], current = null, messages = [], sse = null, settings = null;

async function api(path, opts = {}) {
  const resp = await fetch(path, { headers: { "Content-Type": "application/json" }, ...opts });
  if (resp.status === 204) return null;
  const text = await resp.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = { error: text }; }
  if (!resp.ok) throw new Error((data && data.error) || ("请求失败 " + resp.status));
  if (resp.headers.get("X-Relais-Error")) data.__warn = resp.headers.get("X-Relais-Error");
  return data;
}
function md(text) { return DOMPurify.sanitize(marked.parse(text || "")); }
function esc(s) { return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])); }
function ago(iso) {
  if (!iso || iso.startsWith("0001")) return "";
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "刚刚";
  if (s < 3600) return Math.floor(s / 60) + " 分钟前";
  if (s < 86400) return Math.floor(s / 3600) + " 小时前";
  return new Date(iso).toLocaleDateString("zh-CN");
}
function toast(msg) { alert(msg); }
async function copy(text) { try { await navigator.clipboard.writeText(text); toast("已复制：" + text); } catch { prompt("复制这句话：", text); } }
const attachHint = (name) => "接入 relais 模块 " + name;

// ---------- 守卫与模块列表 ----------
async function loadState() {
  try {
    const st = await api("/api/local/state");
    $("daemon-dot").classList.add("on");
    $("daemon-text").textContent = "守卫在跑 · " + st.version + (st.codex_ok ? "" : " · codex 路径未设置");
  } catch (e) {
    $("daemon-dot").classList.remove("on");
    $("daemon-text").textContent = "守卫没响应：" + e.message;
  }
}
async function loadModules() {
  modules = await api("/api/local/modules");
  $("empty").hidden = modules.length > 0;
  $("main").hidden = modules.length === 0;
  const list = $("module-list");
  list.innerHTML = "";
  for (const m of modules) {
    const el = document.createElement("div");
    el.className = "module" + (current && current.name === m.name ? " active" : "");
    const chipClass = m.state === "等你" ? "you" : (m.state === "讨论中" ? "ink" : "");
    el.innerHTML = `<div class="name">${esc(m.name)}</div>
      <div class="meta"><span class="chip ${chipClass}">${esc(m.state)}</span><span class="mono muted">${m.round}/${m.round_cap} · ${esc(ago(m.last_at)) || "无信"}</span></div>`;
    el.onclick = () => select(m.name);
    list.appendChild(el);
  }
  if (!current && modules.length) select(modules[0].name);
  else if (current) {
    const fresh = modules.find((m) => m.name === current.name);
    if (fresh) { current = fresh; renderNow(); }
  }
}

// ---------- 当前模块 ----------
async function select(name) {
  current = modules.find((m) => m.name === name);
  if (!current) return;
  document.querySelectorAll(".module").forEach((el) => el.classList.toggle("active", el.querySelector(".name").textContent === name));
  renderNow();
  await loadMessages();
  openSSE();
}
function sideText(m) {
  const c = m.claude, x = m.codex;
  const claude = c.waiting ? `在等信${c.session_name ? " · 对话「" + esc(c.session_name) + "」" : ""} · 从 ${esc(new Date(c.wait_since).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" }))} 起`
    : `没在等 · 在 Claude 对话里说：${esc(attachHint(m.name))}`;
  let codex = x.attached ? `已接入「${esc(x.thread_name || "未命名对话")}」` : `未接入 · 在 Codex 对话里说：${esc(attachHint(m.name))}`;
  if (x.attached && x.last_delivery === "ok") codex += ` · 已投递 ${esc(ago(x.delivery_at))}`;
  if (x.delivery_error) codex += ` · <span class="muted">投递失败</span>`;
  return { claude, codex };
}
function nowLine(m) {
  if (m.closed) return "模块已关闭";
  if (m.pending_conclusion) return m.pending_conclusion.awaiting_confirm ? `已握手，等你确认开工（承接方 ${m.pending_conclusion.owner}）` : `已握手，承接方 ${m.pending_conclusion.owner}`;
  if (m.needs_human_q) return "等你回答";
  if (m.last_seq === 0) return "还没有信。先在下面写第一封，或让任一侧开题。";
  const who = { claude: "Claude", codex: "Codex", user: "你" }[m.waiting_for] || "";
  return `${who ? "等 " + who + " 回信" : "空闲"} · 上封信来自 ${m.last_from} · ${ago(m.last_at)}`;
}
function renderNow() {
  const m = current;
  $("now-line").textContent = nowLine(m);
  $("now-round").textContent = `第 ${m.round}/${m.round_cap} 回合 · ${m.mode === "autopilot" ? "甩手" : "监督"}`;
  const busy = !m.closed && !m.needs_human_q && !m.pending_conclusion && (m.waiting_for === "claude" || m.waiting_for === "codex");
  $("progress").hidden = !busy;
  const t = sideText(m);
  $("side-claude").innerHTML = t.claude;
  $("side-codex").innerHTML = t.codex;
  document.querySelector('[data-copy-side="claude"]').hidden = m.claude.waiting;
  document.querySelector('[data-copy-side="codex"]').hidden = m.codex.attached;
  $("pick-codex").hidden = false;
  renderTodo(m);
  $("ms-name").value = m.name; $("ms-mode").value = m.mode; $("ms-cap").value = m.round_cap; $("ms-dir").textContent = m.dir;
  $("ms-close").hidden = m.closed; $("ms-reopen").hidden = !m.closed;
}
function renderTodo(m) {
  const items = [];
  if (m.pending_conclusion && m.pending_conclusion.awaiting_confirm) {
    items.push(`<div class="todo-item"><div><b>确认开工</b> · 承接方 ${esc(m.pending_conclusion.owner)} · ${esc(m.pending_conclusion.summary)}</div>
      <div class="small mono">${esc(m.pending_conclusion.path)}</div><div><button class="btn" data-act="kickoff">确认开工</button></div></div>`);
  }
  if (m.needs_human_q) {
    items.push(`<div class="todo-item"><div><b>回答问题</b> · ${esc(m.needs_human_q)}</div>
      <textarea id="answer" rows="3" placeholder="你的回答会作为一封信发给两侧"></textarea>
      <div class="row gap"><label class="small">先回 <select id="answer-first"><option value="">按接话规则</option><option value="claude">Claude</option><option value="codex">Codex</option></select></label>
      <button class="btn" data-act="answer">发送并继续</button><button class="btn ghost" data-act="resume">只继续，不回答</button></div></div>`);
  }
  if (m.codex.delivery_error) {
    items.push(`<div class="todo-item"><div><b>Codex 没收到信</b> · ${esc(m.codex.delivery_error)}</div><div><button class="btn" data-act="redeliver">重新投递</button></div></div>`);
  }
  if (m.rejected && m.rejected.length) {
    items.push(`<div class="todo-item"><div><b>有信没法读</b> · outbox 里：${esc(m.rejected.join("、"))}（同名 .txt 里有原因）</div></div>`);
  }
  if (!m.closed && !m.claude.waiting && !m.codex.attached && m.last_seq === 0) {
    items.push(`<div class="todo-item"><div><b>接入两侧对话</b> · 在各自的对话里说一句：</div>
      <div class="row gap"><button class="btn ghost tiny" data-copy="${esc(attachHint(m.name))}">复制「${esc(attachHint(m.name))}」</button></div></div>`);
  }
  $("todo").hidden = items.length === 0;
  $("todo-body").innerHTML = items.join("");
}
async function act(name) {
  const n = encodeURIComponent(current.name);
  try {
    if (name === "kickoff") await api(`/api/channels/${n}/auto/kickoff`, { method: "POST" });
    if (name === "resume") await api(`/api/channels/${n}/auto/resume`, { method: "POST" });
    if (name === "redeliver") await api(`/api/local/modules/${n}/redeliver`, { method: "POST" });
    if (name === "answer") {
      const text = $("answer").value.trim();
      if (!text) return toast("先写点什么");
      const first = $("answer-first").value;
      await sendLetter(text, first);
      await api(`/api/channels/${n}/auto/resume`, { method: "POST" });
    }
    await loadModules();
  } catch (e) { toast(e.message); }
}
async function sendLetter(text, first) {
  const n = encodeURIComponent(current.name);
  const body = (first ? `@${first} 先回\n\n` : "") + text;
  const summary = text.split("\n").find((l) => l.trim()) || "（无摘要）";
  await api(`/api/channels/${n}/messages`, { method: "POST", body: JSON.stringify({ to: ["claude", "codex"], summary: summary.replace(/^#+\s*/, "").slice(0, 80), body_md: body }) });
}

// ---------- 往来 ----------
const kindLabel = { resolved: "收敛提议", conclusion: "结论", kickoff: "开工", "needs-human": "需要你" };
async function loadMessages() {
  messages = await api(`/api/channels/${encodeURIComponent(current.name)}/messages`);
  renderTimeline();
}
function renderTimeline() {
  const tl = $("timeline");
  tl.innerHTML = "";
  $("letters-count").textContent = messages.length ? `${messages.length} 封` : "";
  for (const m of messages) {
    const el = document.createElement("div");
    el.className = "letter from-" + m.from;
    const label = m.kind === "resolved" && m.ack_of ? "附和" : (kindLabel[m.kind] || "");
    const when = new Date(m.created_at).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
    el.innerHTML = `<div class="head"><span class="mono">${m.seq ? String(m.seq).padStart(3, "0") : "—"}</span><span class="eyebrow">${esc(m.from)}</span><span class="mono muted">${esc(when)}</span>${label ? `<span class="chip">${esc(label)}</span>` : ""}${m.owner ? `<span class="mono muted">承接方 ${esc(m.owner)}</span>` : ""}</div>
      <div class="summary">${esc(m.summary)}</div><div class="body" hidden></div>`;
    el.querySelector(".summary").onclick = async () => {
      const b = el.querySelector(".body");
      if (b.hidden && !b.innerHTML) { const full = await api(`/api/messages/${m.id}`); b.innerHTML = md(full.body_md); }
      b.hidden = !b.hidden;
    };
    tl.appendChild(el);
  }
  tl.lastElementChild && tl.lastElementChild.scrollIntoView({ block: "nearest" });
}
function openSSE() {
  if (sse) sse.close();
  sse = new EventSource("/api/events?channel=" + encodeURIComponent(current.name));
  sse.addEventListener("message", (ev) => {
    const m = JSON.parse(ev.data);
    if (!messages.some((x) => x.id === m.id)) { messages.push(m); renderTimeline(); }
    loadModules();
  });
}

// ---------- Codex 对话点选 ----------
async function pickCodex() {
  const box = $("codex-pick");
  if (!box.hidden) { box.hidden = true; return; }
  box.hidden = false;
  box.innerHTML = `<div class="muted small">读取中…</div>`;
  try {
    const convs = await api(`/api/local/conversations?side=codex&dir=${encodeURIComponent(current.dir)}`);
    box.innerHTML = convs.length ? "" : `<div class="muted small">${esc(convs.__warn || "这个目录下没有 Codex 对话；先在 Codex 里打开项目建一个对话。")}</div>`;
    for (const c of convs) {
      const b = document.createElement("button");
      b.className = "btn ghost tiny";
      b.textContent = `${c.name || c.title || c.id} · ${ago(c.updated_at)}`;
      b.onclick = async () => {
        try { await api(`/api/local/modules/${encodeURIComponent(current.name)}/attach`, { method: "POST", body: JSON.stringify({ side: "codex", thread: c.id }) }); box.hidden = true; await loadModules(); }
        catch (e) { toast(e.message); }
      };
      box.appendChild(b);
    }
  } catch (e) { box.innerHTML = `<div class="error">${esc(e.message)}</div>`; }
}

// ---------- 本模块设置 ----------
async function saveModule() {
  const patch = {};
  if ($("ms-name").value.trim() !== current.name) patch.name = $("ms-name").value.trim();
  if ($("ms-mode").value !== current.mode) patch.mode = $("ms-mode").value;
  if (Number($("ms-cap").value) !== current.round_cap) patch.round_cap = Number($("ms-cap").value);
  if (!Object.keys(patch).length) return;
  try {
    const m = await api(`/api/local/modules/${encodeURIComponent(current.name)}`, { method: "PATCH", body: JSON.stringify(patch) });
    current = m; await loadModules(); await select(m.name);
  } catch (e) { toast(e.message); }
}
async function closeModule(reopen) {
  try { await api(`/api/local/modules/${encodeURIComponent(current.name)}/${reopen ? "reopen" : "close"}`, { method: "POST" }); await loadModules(); }
  catch (e) { toast(e.message); }
}
async function deleteModule() {
  if (!confirm(`删除模块「${current.name}」的记录？项目里的信件文件默认保留。`)) return;
  const files = confirm("同时删除项目里的 relais/mail/" + current.name + " 目录？（取消 = 保留文件）");
  try {
    await api(`/api/local/modules/${encodeURIComponent(current.name)}${files ? "?files=1" : ""}`, { method: "DELETE" });
    current = null; if (sse) sse.close(); await loadModules();
  } catch (e) { toast(e.message); }
}

// ---------- 新建模块 ----------
async function openNew() {
  $("nm-error").textContent = "";
  $("nm-name").value = ""; $("nm-dir").value = ""; $("nm-dir-wrap").hidden = true;
  const repos = await api("/api/local/repos");
  const sel = $("nm-repo");
  sel.innerHTML = repos.map((r) => `<option value="${esc(r.dir)}">${esc(r.name)} · ${esc(r.dir)}</option>`).join("") + `<option value="__manual">手填路径…</option>`;
  await loadCodexOptions();
  $("new-module").showModal();
}
async function loadCodexOptions() {
  const dir = $("nm-repo").value === "__manual" ? $("nm-dir").value.trim() : $("nm-repo").value;
  const sel = $("nm-codex");
  sel.innerHTML = `<option value="">稍后再说</option>`;
  if (!dir) return;
  try {
    const convs = await api(`/api/local/conversations?side=codex&dir=${encodeURIComponent(dir)}`);
    for (const c of convs) sel.innerHTML += `<option value="${esc(c.id)}">${esc(c.name || c.title || c.id)} · ${esc(ago(c.updated_at))}</option>`;
  } catch { /* 没有也行 */ }
}
async function createModule() {
  const dir = $("nm-repo").value === "__manual" ? $("nm-dir").value.trim() : $("nm-repo").value;
  try {
    const m = await api("/api/local/modules", { method: "POST", body: JSON.stringify({ name: $("nm-name").value.trim(), dir, codex_thread: $("nm-codex").value }) });
    $("new-module").close(); await loadModules(); await select(m.name);
  } catch (e) { $("nm-error").textContent = e.message; }
}

// ---------- 设置 ----------
async function openSettings() {
  settings = await api("/api/local/settings");
  $("st-codex").value = settings.codex_path || ""; $("st-codex-ok").textContent = settings.codex_ok ? "可用 ✓" : "找不到或不可执行";
  $("st-mode").value = settings.default_mode; $("st-cap").value = settings.default_cap; $("st-notify").checked = !!settings.notify_every_letter;
  $("st-error").textContent = "";
  $("settings").showModal();
}
async function saveSettings() {
  try {
    await api("/api/local/settings", { method: "PUT", body: JSON.stringify({ codex_path: $("st-codex").value.trim(), default_mode: $("st-mode").value, default_cap: Number($("st-cap").value), notify_every_letter: $("st-notify").checked }) });
    $("settings").close(); loadState();
  } catch (e) { $("st-error").textContent = e.message; }
}

// ---------- 绑定 ----------
document.addEventListener("click", (ev) => {
  const t = ev.target.closest("[data-act],[data-copy],[data-copy-side]");
  if (!t) return;
  if (t.dataset.act) act(t.dataset.act);
  if (t.dataset.copy) copy(t.dataset.copy);
  if (t.dataset.copySide) copy(attachHint(current.name));
});
$("pick-codex").onclick = pickCodex;
$("composer").onsubmit = async (ev) => {
  ev.preventDefault();
  const text = $("compose-body").value.trim();
  if (!text) return;
  try { await sendLetter(text, $("compose-first").value); $("compose-body").value = ""; await loadModules(); } catch (e) { toast(e.message); }
};
$("ms-save").onclick = saveModule;
$("ms-close").onclick = () => closeModule(false);
$("ms-reopen").onclick = () => closeModule(true);
$("ms-delete").onclick = deleteModule;
$("open-new").onclick = openNew; $("empty-new").onclick = openNew;
$("nm-repo").onchange = () => { $("nm-dir-wrap").hidden = $("nm-repo").value !== "__manual"; loadCodexOptions(); };
$("nm-dir").onchange = loadCodexOptions;
$("new-form").onsubmit = (ev) => { if (ev.submitter && ev.submitter.value === "ok") { ev.preventDefault(); createModule(); } };
$("open-settings").onclick = openSettings;
$("settings-form").onsubmit = (ev) => { if (ev.submitter && ev.submitter.value === "ok") { ev.preventDefault(); saveSettings(); } };
$("st-test").onclick = async () => {
  try { await api("/api/local/settings", { method: "PUT", body: JSON.stringify({ codex_path: $("st-codex").value.trim(), default_mode: $("st-mode").value, default_cap: Number($("st-cap").value), notify_every_letter: $("st-notify").checked }) }); const s = await api("/api/local/settings"); $("st-codex-ok").textContent = s.codex_ok ? "可用 ✓" : "找不到或不可执行"; }
  catch (e) { $("st-codex-ok").textContent = e.message; }
};

(async function init() {
  await loadState();
  try { await loadModules(); } catch (e) { toast(e.message); }
  setInterval(loadState, 15000);
  setInterval(() => loadModules().catch(() => {}), 5000);
})();
```

`api.LocalModule` 的 `pending_conclusion.path` 由 Task 9 的 `moduleInfo` 填（`filepath.Join(md, ConclusionName(seq))`）。

- [ ] **Step 6: `check.sh` 加 `node --check internal/server/web/local/app.js`**

- [ ] **Step 7: 跑测试与地板，再真机看一眼**

Run: `go test ./internal/server/ && ./scripts/check.sh`
Expected: 全绿。然后临时目录冒烟：`RELAIS_LOCAL_DIR=$(mktemp -d) relais local bootstrap --no-service --listen 127.0.0.1:18099`、`relais serve --config "$RELAIS_LOCAL_DIR/server.toml"`，浏览器开 `http://127.0.0.1:18099`：新建模块 → 看到两侧接入提示 → 写第一封 → 时间线出现 → 设置抽屉打开保存。截图存 `docs/superpowers/plans/screenshots/m9-console.png`（不进 git 也行，只为人眼确认）。用完 `rm -rf "$RELAIS_LOCAL_DIR"`。

- [ ] **Step 8: 提交**

```bash
git add -A
git commit -m "feat(web): 本地控制台独立页面——现在/该你做/往来/本模块/新建/设置，白底细线 4px 圆角，渐变只做进行中指示（M9 Task 12）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 13: 版本、文档、D46、真实冒烟脚本

**Files:**
- Modify: `main.go`（`version = "0.7.0-m9"`、用法串）、`internal/server/static.go`（`Version`）、`README.md`（「本地单人模式」一节重写）、`docs/decisions.md`（D46）、`CONTEXT.md`（若执行期新增术语）
- Create: `scripts/smoke-m9.sh`（隔离冒烟）

- [ ] **Step 1: 版本号**

两处改 `0.7.0-m9`；`grep -rn "0.6.0-m8" --include=*.go --include=*_test.go .` 应为空。

- [ ] **Step 2: README「本地单人模式」重写**

替换整节（标题改为 `## 本地单人模式（M9：接入现有对话）`）：

```markdown
## 本地单人模式（M9：接入现有对话）

一个人、一台 Mac，让你正在用的 Claude Code 对话和 Codex 对话隔着 Relais 互相写信讨论，直到双方都说"可以收敛"。讨论就发生在你开着的那两个对话里，你随时能看、能插话；Relais 只负责搬信。

1. **安装**：双击仓库根目录的 `安装 Relais 本地模式.command`。它编译并安装 `relais`，装一个常驻（`relais serve`），最后在浏览器打开控制台。控制台只认本机，不需要登录。
2. **新建模块**：控制台 → 新建模块，填名字、点选项目文件夹。项目里会生成 `relais/PROTOCOL.md`（两侧共读的传信协议）和 `relais/mail/<模块>/`（信箱），并在 `CLAUDE.md`/`AGENTS.md` 末尾加两行指针。
3. **接入两侧对话**：在 Claude Code 对话里说「接入 relais 模块 X」，它会在后台跑 `relais wait X`；在 Codex 对话里说同一句，它会跑 `relais attach X`。控制台的「现在」一栏会显示两侧是否接上。
4. **讨论**：任一侧（或你在控制台）写第一封信。之后 Claude 侧靠 `wait` 被叫醒、Codex 侧由 Relais 直接把信塞进对话。每封信都落在 `relais/mail/<模块>/NNN-<发件人>.md`，你在两个对话里都看得见，也能直接插话。
5. **收敛与开工**：双方各发一封收敛信（第二封是附和）即握手。结论落在 `conclusion-NNN.md`；监督模式下你在控制台点「确认开工」，承接方那一侧会收到开工通知。需要你定夺、回合到顶、投递失败时，控制台「该你做」会亮起并弹桌面通知。

agent 侧只有三条命令，全部只读写项目目录、不联网：`relais post <模块> <文件> [--resolved --owner …|--ack|--needs-human]`、`relais wait <模块>`、`relais attach <模块>`。协议全文见项目里的 `relais/PROTOCOL.md`，它只规定信怎么走，不规定信里写什么。
```

- [ ] **Step 3: D46**

在 `docs/decisions.md` 顶部（D45 之上）追加「D46 M9 执行期实现选择」，内容 = 本计划开头「已知偏离 spec」五条 + 执行期新增的偏离（如 `X-Relais-Error` 头、`CodexThreadByIDOrName`、`SetCap/MaxSeq`、`GET /{$}` 路由）。状态 live（v0.7.0-m9）。

- [ ] **Step 4: 冒烟脚本 `scripts/smoke-m9.sh`**

```bash
#!/usr/bin/env bash
# M9 隔离冒烟：临时 RELAIS_LOCAL_DIR、--no-service、假 codex、临时项目。绝不碰 ~/Library、launchd。
set -euo pipefail
cd "$(dirname "$0")/.."
export RELAIS_LOCAL_DIR="$(mktemp -d)"
PROJ="$(mktemp -d)"
FAKE="$(mktemp -d)"
LOG="$FAKE/codex.log"
cat > "$FAKE/codex" <<EOS
#!/bin/sh
printf '%s\n' "\$@" >> "$LOG"
EOS
chmod +x "$FAKE/codex"
export CODEX_HOME="$FAKE/codexhome"; mkdir -p "$CODEX_HOME"
sqlite3 "$CODEX_HOME/state_5.sqlite" "CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT NOT NULL, title TEXT NOT NULL, name TEXT, archived INTEGER NOT NULL DEFAULT 0, updated_at_ms INTEGER); INSERT INTO threads VALUES ('t-1','$PROJ','冒烟对话','冒烟',0,1);"
trap 'kill $SERVE 2>/dev/null || true; rm -rf "$RELAIS_LOCAL_DIR" "$PROJ" "$FAKE"' EXIT
go build -o "$FAKE/relais" .
R="$FAKE/relais"
"$R" local bootstrap --no-service --listen 127.0.0.1:18098 --json
"$R" serve --config "$RELAIS_LOCAL_DIR/server.toml" >"$FAKE/serve.log" 2>&1 & SERVE=$!
sleep 1
curl -sf -X PUT localhost:18098/api/local/settings -d "{\"codex_path\":\"$FAKE/codex\",\"default_mode\":\"supervised\",\"default_cap\":8}"
curl -sf -X POST localhost:18098/api/local/modules -d "{\"name\":\"smoke\",\"dir\":\"$PROJ\"}" >/dev/null
cd "$PROJ"
"$R" attach smoke --as codex
printf '@codex 先回\n\n第一封' > /tmp/first.md
curl -sf -X POST localhost:18098/api/channels/smoke/messages -d '{"to":["claude","codex"],"summary":"第一封","body_md":"@codex 先回\n\n第一封"}' >/dev/null
sleep 3
test -f relais/mail/smoke/001-hou.md || { echo "❌ 雇主信没归档"; exit 1; }
grep -q "由你先回" "$LOG" || { echo "❌ codex 没收到投递"; cat "$LOG"; exit 1; }
printf '提议：就这么办\n' > relais/mail/smoke/drafts/c.md
"$R" post smoke relais/mail/smoke/drafts/c.md --as codex --resolved --owner codex
sleep 3
"$R" wait smoke --as claude --timeout 5s | grep -q "002-codex.md" || { echo "❌ claude wait 没拿到"; exit 1; }
printf '附和\n' > relais/mail/smoke/drafts/a.md
"$R" post smoke relais/mail/smoke/drafts/a.md --as claude --ack
sleep 3
test -f relais/mail/smoke/conclusion-003.md || { echo "❌ 没有结论文件"; ls relais/mail/smoke; exit 1; }
curl -sf -X POST localhost:18098/api/channels/smoke/auto/kickoff >/dev/null
sleep 3
test -f relais/mail/smoke/kickoff-003.md || { echo "❌ 没有开工文件"; exit 1; }
grep -q "你是承接方" "$LOG" || { echo "❌ codex 没收到开工通知"; exit 1; }
curl -sf -X PATCH localhost:18098/api/local/modules/smoke -d '{"name":"smoke2"}' >/dev/null
test -d relais/mail/smoke2 || { echo "❌ 改名没移目录"; exit 1; }
curl -sf -X DELETE localhost:18098/api/local/modules/smoke2 >/dev/null
test -d relais/mail/smoke2 || { echo "❌ 删除默认不该删文件"; exit 1; }
echo "✅ M9 冒烟通过"
```

Run: `bash scripts/smoke-m9.sh`
Expected: `✅ M9 冒烟通过`。（`sqlite3` 命令 macOS 自带。）

- [ ] **Step 5: 全量地板 + 提交**

```bash
./scripts/check.sh
git add -A
git commit -m "docs(m9): 版本 0.7.0-m9、README 本地模式改写、D46 执行期选择、隔离冒烟脚本（M9 Task 13）

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

## 自审记录

- **spec 覆盖**：§2 探针→D45 已记；§3 形状→T7/T10；§4 布局命名→T1（kickoff 命名偏离已记）；§4.1 信封→T1/T7；§5 协议→T6；§6.1–6.4→T3/T4/T5/T1+T10；§7.1–7.5→T7（通知走 `Notify`）+T9（`notify_every_letter` 设置存了但每封信通知的调用点在 T7 `archive` 后：执行 T7 时若 `Daemon` 有 `NotifyEveryLetter func() bool` 且为真，则每封新归档信 `Notify("Relais · 模块", "第 N 封 来自 X：摘要")`——执行者在 T7 加该字段与一行调用，T9 的 `newDaemon` 从设置读）；§8 API/鉴权→T8/T9；§9 页面→T12（i18n 无、外链无、令牌齐）；§10 数据模型→T2；§11 砍/迁移/升级→T9/T11；§12 安全不变量→T8 锚点（线上 401/404、本地非回环 401）、T9（删除默认留文件、路径校验）、T7（exec 参数数组）；§13 测试→各任务 + T13 冒烟。
- **类型一致性**：`LocalManager` 接口在 T8 定义、T9 实现、T8 测试 fake 三处签名一致（`CreateModule(api.LocalModuleRequest)`、`AttachModule(name, api.LocalAttachRequest)`、`DeleteModule(name, files bool)`）；`Daemon.Publish` 签名 `(int64, *store.Message, string)` 与 `server.PublishMessage` 一致；T9 的 `NewDaemon` 在 T10 改为方法 `newDaemon`——执行 T9 时直接按方法实现。
- **占位扫描**：无 TBD/TODO；每步有代码或精确命令。
