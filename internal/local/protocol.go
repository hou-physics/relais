package local

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const (
	ProtocolMarker   = "<!-- relais-protocol v1 -->"
	PointerMarker    = "<!-- relais-local -->"
	pointerEndMarker = "<!-- /relais-local -->"
	pointerBlock     = PointerMarker + "\n本项目接入了 Relais。雇主说「接入 relais 模块 X」时，读 relais/PROTOCOL.md 并照做。\n" + pointerEndMarker + "\n"
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
- claude 侧：在后台运行 ` + "`relais wait X`" + `。它会一直等，信到了就退出并打印信的路径。每处理完一封（不管回不回信），都要再在后台运行一次 ` + "`relais wait X`" + `，否则收不到下一封。
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
- claude 侧：不管刚才是回了信、还是这封不用回，都再在后台运行一次 ` + "`relais wait X`" + `。
- codex 侧：不用做。

## 6. 收敛怎么算
双方各一封收敛信、第二封带 --ack、承接方一致，就算握手。结论会落在 ` + "`relais/mail/X/conclusion-<序号>.md`" + `，承接方会收到开工通知（文件 ` + "`kickoff-<序号>.md`" + `，发件人 relais）。承接方是 user 时两侧都只等雇主。

## 7. 雇主的信
雇主写的信（发件人 hou）会送到两侧，但只由一侧先回：通知里会写明是不是你。不是你就不回，等对方的信。claude 侧不用回时也要再运行一次 ` + "`relais wait X`" + `。

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
