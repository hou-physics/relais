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
	"slices"
	"strings"
	"sync"
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
	// NotifyEveryLetter：非 nil 且返回 true 时，archive 每落盘一封非 kickoff、非
	// relais 发件的信都额外通知一次（控制者裁决 M9 Task 7 #1）。nil 视为 false。
	NotifyEveryLetter func() bool

	// mu：串行化 RunOnce 与 Redeliver（修复轮 1 裁决 c）——两者都会读写同一模块的
	// outbox/归档文件与登记表，并发跑会互相踩脚。
	mu sync.Mutex
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
	d.mu.Lock()
	defer d.mu.Unlock()
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
	// DB 里的 codex_attached_at 是 RFC3339（秒精度），.attach-codex 的 At 有亚秒精度；
	// 不截到秒比较的话 a.At.After(prev) 永远为真，会让 SetCodexAttach 每 2s 跑一次，
	// 顺带清掉 codex_delivery_error（见 SetCodexAttach 实现）。截到秒再比（修复轮 1 F1）。
	at := a.At.UTC().Truncate(time.Second)
	if a.ThreadID != lm.CodexThreadID || at.After(prev) {
		_ = d.Store.SetCodexAttach(m.ChannelID, a.ThreadID, a.Name, at.Format(time.RFC3339))
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
		_ = d.rejectOutbox(m, filepath.Join(md, "outbox", e.Name), e.Err.Error())
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

// rejectOutbox：outbox 里读不了/存不了的信，改名 <name>.rejected，附 <name>.rejected.txt
// 写明原因，通知一次；总是返回 nil——这类信是「永久性存不下去」，不该在下一轮重试
// （修复轮 1 F3，从 ingest 的 ScanOutbox 错误处理与 ingestOne 的未知发件人/未知
// kind/ack_of 找不到三处共用）。
func (d *Daemon) rejectOutbox(m ModuleRef, path, reason string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	_ = os.Rename(path, path+".rejected")
	_ = os.WriteFile(path+".rejected.txt", []byte(reason+"\n"), 0o644)
	d.notify("Relais · "+m.Name, "outbox 里有一封信没法发出（已改名 .rejected）："+reason)
	return nil
}

func (d *Daemon) ingestOne(m ModuleRef, md string, it OutboxItem) error {
	senderID, ok := d.Users[it.From]
	if !ok {
		return d.rejectOutbox(m, it.Path, fmt.Sprintf("未知发件人 %q", it.From))
	}
	to := []int64{d.Users[otherSide(it.From)], d.Users["hou"]}
	opts := store.SaveOpts{IdemKey: "outbox:" + it.Key}
	switch it.Kind {
	case "resolved":
		opts.Kind, opts.Owner = "resolved", it.Owner
		if it.AckOf > 0 {
			id, err := d.Store.MessageIDBySeq(m.ChannelID, it.AckOf)
			if err != nil {
				return d.rejectOutbox(m, it.Path, fmt.Sprintf("ack_of 指向的第 %d 封不存在", it.AckOf))
			}
			opts.AckOf = id
		}
	case "needs-human", "letter", "":
	default:
		return d.rejectOutbox(m, it.Path, fmt.Sprintf("outbox 信的 kind %q 不认识", it.Kind))
	}

	// replay：这个 IdemKey 在之前某一轮已经落过库——说明上一轮 SaveMessageOpts 之后的
	// 某个步骤失败了，文件才没被删掉留到这一轮重试。EvaluateHandshake/Kickoff/GetMessage
	// 本身是幂等的，重放时照跑没问题；但会重复产生用户可见效果的都要跳过：计回合
	// （修复轮 1 F2）、needs-human/OwnerConflict 的重复通知、重复 SSE Publish、
	// 以及默认分支的 ClearKickedOff（修复轮 2，同一个 !replay 模式）。
	replay, err := d.Store.SentKeyExists(m.ChannelID, opts.IdemKey)
	if err != nil {
		return err
	}

	msg, err := d.Store.SaveMessageOpts(m.ChannelID, senderID, to, it.Summary, it.Body, "", opts)
	if err != nil {
		return err
	}

	switch it.Kind {
	case "needs-human":
		_ = d.Store.SetNeedsHuman(m.ChannelID, it.Summary)
		if !replay {
			d.notify("Relais · "+m.Name, it.From+" 需要你定夺："+it.Summary)
		}
	case "resolved":
		res, err := d.Store.EvaluateHandshake(m.ChannelID, msg.ID)
		if err != nil {
			return err
		}
		switch res {
		case store.HandshakeOwnerConflict:
			if !replay {
				d.notify("Relais · "+m.Name, "两侧提名的承接方不一致，请到控制台定")
			}
		case store.HandshakeDone:
			if a, err := d.Store.GetAuto(m.ChannelID); err == nil && a.Mode == "autopilot" {
				k, err := d.Store.Kickoff(m.ChannelID, d.Users["hou"])
				if err != nil {
					d.log().Warn("自动开工失败", "module", m.Name, "err", err)
					d.notify("Relais · "+m.Name, "已握手但自动开工失败："+err.Error())
				} else if d.Publish != nil {
					d.Publish(m.ChannelID, k, m.Name)
				}
			} else {
				d.notify("Relais · "+m.Name, "已握手，等你确认开工")
			}
			if cm, err := d.Store.GetMessage(msg.ID, senderID, true); err == nil {
				msg = cm
			}
		}
		if !replay {
			if hit, _ := d.Store.CountLocalTurn(m.ChannelID); hit {
				d.notify("Relais · "+m.Name, "回合到上限了，去控制台决定继续还是收")
			}
		}
	default:
		if !replay {
			_ = d.Store.ClearKickedOff(m.ChannelID)
			if hit, _ := d.Store.CountLocalTurn(m.ChannelID); hit {
				d.notify("Relais · "+m.Name, "回合到上限了，去控制台决定继续还是收")
			}
		}
	}
	if !replay && d.Publish != nil {
		d.Publish(m.ChannelID, msg, m.Name)
	}
	// 文件删除放最后：前面任何一步出错都会提前 return，把文件留下，下一轮重试
	// （修复轮 1 F2）。
	if err := os.Remove(it.Path); err != nil && !os.IsNotExist(err) {
		return err
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
		l := Letter{ID: msg.ID, Module: m.Name, Seq: msg.Seq, From: msg.Sender, To: msg.To, Date: msg.CreatedAt, Kind: msg.Kind, Owner: msg.Owner, Summary: msg.Summary}
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
		if l.Kind != "kickoff" && l.From != "relais" && d.NotifyEveryLetter != nil && d.NotifyEveryLetter() {
			d.notify("Relais · "+m.Name, fmt.Sprintf("第 %d 封 来自 %s：%s", l.Seq, l.From, l.Summary))
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
		l := Letter{ID: k.ID, Module: m.Name, Seq: seq, From: "relais", To: k.To, Date: k.CreatedAt, Kind: "kickoff", Owner: k.Owner, Summary: k.Summary, Path: filepath.Join(md, KickoffName(seq))}
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
		// desc（Describe）本身已经带了「雇主的信，由谁先回」那行，这里不再重复加前缀
		// （修复轮 1 minor a：原先的 "雇主的信，"+desc 会把这句话说两遍）。
		return head + desc + "\n读它，按 relais/PROTOCOL.md 处理。"
	}
	return head + desc + "\n读它，按 relais/PROTOCOL.md 回信。"
}

// deliver：spec §7.3。claude 侧靠门铃，只记一笔；codex 侧 queue。只投给信封 to 里
// 点了名的一侧（kickoff 例外，一律投两侧）——修复轮 1 F4：以前不管 to 是谁都投 codex，
// 雇主只想跟 claude 说的悄悄话也会被塞进 Codex 对话。
func (d *Daemon) deliver(m ModuleRef, md string, l Letter) error {
	if l.Kind == "kickoff" || slices.Contains(l.To, "claude") {
		_ = d.Store.RecordDelivery(l.ID, "claude", "ok", "")
	}
	if l.Kind != "kickoff" && !slices.Contains(l.To, "codex") {
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
	data, err := os.ReadFile(l.Path)
	if err != nil {
		return fail("读不到归档信 " + l.Path + "：" + err.Error())
	}
	_, body, err := ParseLetter(data)
	if err != nil {
		return fail("归档信解析失败 " + l.Path + "：" + err.Error())
	}
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
	d.mu.Lock()
	defer d.mu.Unlock()
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
		// 挑投递目标的规则跟 deliver() 保持一致（修复轮 2）：只挑 to 里点了名 codex 的信，
		// kickoff 例外一律算在内；不能再按 From != "codex" 挑，那样会把雇主只写给 claude
		// 的悄悄话也当成该投给 codex 的目标。
		for i := len(ls) - 1; i >= 0; i-- {
			if ls[i].Kind == "kickoff" || slices.Contains(ls[i].To, "codex") {
				return d.deliverCodex(m, md, ls[i])
			}
		}
		return fmt.Errorf("没有需要投给 Codex 的信")
	}
	return fmt.Errorf("模块不存在或已关闭")
}
