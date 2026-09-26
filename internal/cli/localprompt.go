package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// localPrompt 组装讨论脑一轮的提示词（D40 基线规矩 + 项目 RULES.md + 本轮信息）。
// humanNotes 为本侧上次发言后雇主在频道里写的话（D42 ⑦：被拒的一侧不跑 agent，下次唤醒时补上），空则不出该段。
func localPrompt(side, channel, msgPath, rules, guidance, humanNotes string, first bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "你是 Relais 本地模式里「%s 侧」的讨论脑，负责频道（模块）「%s」。对方是另一个 AI（%s 侧）。你们的雇主是同一个人。\n\n",
		side, channel, otherSide(side))
	b.WriteString("## 规矩（必须遵守）\n")
	b.WriteString("1. 信里只写观点、问题、提议、决定。大段参考材料给路径，不整段粘贴。\n")
	b.WriteString("2. 不替雇主做决定：需要人定的事，用 NEEDS_HUMAN 停下来问。\n")
	b.WriteString("3. 正文写给不懂技术的人也能读；技术细节放最后的「工程附录」。\n")
	b.WriteString("4. 不写密钥、不贴数据库内容、不写个人数据。\n")
	b.WriteString("5. 不替对方改代码，也不改任何文件：你只读仓库。要对方改什么，写在信里。\n")
	b.WriteString("6. 明确不同意对方的 RESOLVED 时，写普通回信说明分歧，不要写 RESOLVED。\n")
	if strings.TrimSpace(rules) != "" {
		b.WriteString("\n## 本项目铁律（relais/RULES.md）\n")
		b.WriteString(strings.TrimSpace(rules))
		b.WriteString("\n")
	}
	b.WriteString("\n## 本轮\n")
	if first {
		fmt.Fprintf(&b, "这是你在本频道第一次发言。先读 relais/inbox/ 与 relais/sent/ 下信头 channel: %s 的全部往来（文件名以日期开头），再读新信。\n", channel)
	}
	fmt.Fprintf(&b, "新信在文件 %s。当前目录是项目根，可用只读工具查看代码。同目录下可能混有其他模块的信，只看信头 channel: %s 的。\n", msgPath, channel)
	if strings.TrimSpace(humanNotes) != "" {
		b.WriteString("\n## 雇主在频道里说过的话（你上次发言之后）\n")
		b.WriteString(strings.TrimSpace(humanNotes))
		b.WriteString("\n")
	}
	if strings.TrimSpace(guidance) != "" {
		b.WriteString("\n## 雇主引导（优先遵循）\n")
		b.WriteString(strings.TrimSpace(guidance))
		b.WriteString("\n")
	}
	b.WriteString("\n## 输出格式（只输出以下三种之一，不要解释、不要代码围栏）\n")
	b.WriteString("A) 认为议题已可收敛（双方立场一致，无未决点）：\n")
	b.WriteString("RESOLVED: <一句话结论>\n---\nowner: claude|codex|user\nowner_reason: <一句：谁一直主要负责这条线、为何由它开工；定不下来写 user>\nack_of: <若是附和对方的 RESOLVED，填对方那封的 id（信头 id: 字段）；自己首次提议留空>\n---\n<完整结论正文：做什么、不做什么、验收标准。附和时可改措辞，不得新增条款；要新增就写成普通回信>\n\n")
	b.WriteString("B) 需要雇主定夺或需要事实澄清：\nNEEDS_HUMAN: <一行问题>\n\n")
	b.WriteString("C) 普通回信：\n---\nsummary: <一句话摘要>\n---\n<正文>\n")
	return b.String()
}

func otherSide(side string) string {
	if side == "claude" {
		return "codex"
	}
	return "claude"
}

// RunLocalPrompt 供本地 hook 调：relais local-prompt [--first]，读 RELAIS_MSG_PATH、项目 RULES.md、私有引导，打印提示词。
func RunLocalPrompt(args []string) error {
	fs := flag.NewFlagSet("local-prompt", flag.ContinueOnError)
	first := fs.Bool("first", false, "本频道首次唤醒（补读历史）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, proj, err := findProject()
	if err != nil {
		return err
	}
	c, cfg, err := newClient()
	if err != nil {
		return err
	}
	msgPath := os.Getenv("RELAIS_MSG_PATH")
	if msgPath == "" {
		return fmt.Errorf("RELAIS_MSG_PATH 未设置（此命令由 bridge hook 调用）")
	}
	rules, _ := os.ReadFile(filepath.Join(root, "relais", "RULES.md"))
	guidance, _ := c.GuidancePull(proj.Channel) // 取不到就当空，不阻塞本轮
	notes := humanNotesSinceLastSend(c, proj.Channel, cfg.Username, os.Getenv("RELAIS_MSG_ID"))
	fmt.Print(localPrompt(cfg.Username, proj.Channel, msgPath, string(rules), guidance, notes, *first))
	return nil
}

// humanNotesSinceLastSend 收集本侧上次发言之后、人（既非本侧也非对侧）写进频道的消息正文，
// 排除本轮触发的那封（它已作为"新信"交给讨论脑）与 kickoff。
// D42 ⑦ 下人的消息只由一侧接话，另一侧被拒时不跑 agent，靠这里在下次唤醒时读到人的话。
// 取不到就返回空，不阻塞本轮。
func humanNotesSinceLastSend(c *Client, channel, side, triggerID string) string {
	list, err := c.Envelopes(channel, false)
	if err != nil {
		return ""
	}
	peer := otherSide(side)
	lastSeq := 0
	for _, m := range list {
		if m.From == side && m.Seq > lastSeq {
			lastSeq = m.Seq
		}
	}
	var b strings.Builder
	for _, m := range list {
		if m.Seq <= lastSeq || m.From == side || m.From == peer || m.ID == triggerID || m.Kind == "kickoff" {
			continue
		}
		full, err := c.Message(m.ID)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "- 第 %d 条（%s）：%s\n", m.Seq, m.From, full.Summary)
		if body := strings.TrimSpace(full.Body); body != "" {
			for _, line := range strings.Split(body, "\n") {
				b.WriteString("  " + line + "\n")
			}
		}
	}
	return b.String()
}
