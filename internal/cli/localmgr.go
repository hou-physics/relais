package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/local"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

// localManager：本地模式的环境层（bootstrap）与模块层（登记表、信箱、协议、指针块、接入、设置）。
// CLI（relais local …）与服务器（/api/local/*，经 server.LocalManager 接口）共用这一份逻辑（D44）。
// 服务器侧只写模块目录下的 relais/PROTOCOL.md、relais/mail/<模块>/，以及 CLAUDE.md/AGENTS.md 的标记块。
type localManager struct {
	ld, scPath string
	startedAt  time.Time
	daemon     *local.Daemon
	shared     *store.Store // newDaemon 打开的长期句柄；非 nil 时各方法复用它而不是每次开关库
}

func newLocalManager(ld string) *localManager {
	return &localManager{ld: ld, scPath: localServerConfigPath(ld), startedAt: time.Now()}
}

type bootstrapResult struct {
	BaseURL, HumanUser string
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", server.ErrLocalInvalid, fmt.Sprintf(format, a...))
}

func (m *localManager) open() (*store.Store, *ServerConfig, error) {
	st, cfg, err := openServerStore(m.scPath)
	if err != nil {
		return nil, nil, err
	}
	if !isLoopbackListen(cfg.Listen) {
		st.Close()
		return nil, nil, fmt.Errorf("%s 里的 listen = %q 不是回环地址；本地模式只允许 127.0.0.1/localhost", m.scPath, cfg.Listen)
	}
	return st, cfg, nil
}

// withStore：守卫已开长期句柄时复用它，否则临时开一个；release 只关临时的。
func (m *localManager) withStore() (*store.Store, func(), error) {
	if m.shared != nil {
		return m.shared, func() {}, nil
	}
	st, _, err := m.open()
	if err != nil {
		return nil, nil, err
	}
	return st, func() { st.Close() }, nil
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

func isExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0
}

// detectCodex：PATH → ChatGPT.app 自带 → Codex 插件目录；都没有返回 ""。
func detectCodex() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{"/Applications/ChatGPT.app/Contents/Resources/codex", filepath.Join(home, ".codex", "plugins", ".plugin-appserver", "codex")} {
		if isExecutable(p) {
			return p
		}
	}
	return ""
}

// bootstrap：只做"环境"——server.toml（含 local_dir）、三账号、设置、旧登记迁移、已登记模块的协议与指针块；幂等。
// 三个账号的密码随机生成、从不打印也不落盘：本地控制台走回环免钥匙（M9）。
func (m *localManager) bootstrap(listen string) (bootstrapResult, error) {
	var res bootstrapResult
	if !isLoopbackListen(listen) {
		return res, fmt.Errorf("本地模式只允许监听回环地址，得到 %q", listen)
	}
	if err := os.MkdirAll(m.ld, 0o700); err != nil {
		return res, err
	}
	if _, err := os.Stat(m.scPath); os.IsNotExist(err) {
		sc := fmt.Sprintf("listen = %q\ndata_dir = %q\nbase_url = %q\nlocal_dir = %q\n", listen, filepath.Join(m.ld, "data"), "http://"+listen, m.ld)
		if err := os.WriteFile(m.scPath, []byte(sc), 0o600); err != nil {
			return res, err
		}
	} else if err := ensureLocalDirLine(m.scPath, m.ld); err != nil { // 旧版 server.toml 没有 local_dir，补上
		return res, err
	}
	st, cfg, err := m.open()
	if err != nil {
		return res, err
	}
	defer st.Close()
	res.BaseURL = cfg.BaseURL
	res.HumanUser = localHuman
	for _, u := range []struct {
		name, display string
		admin         bool
	}{{"claude", "Claude 侧", false}, {"codex", "Codex 侧", false}, {localHuman, "Hou", true}} {
		usr, err := st.UserByName(u.name)
		if err != nil {
			if usr, err = st.CreateUser(u.name, u.display, newPassword()); err != nil {
				return res, err
			}
		}
		if u.admin && !usr.IsAdmin { // 含上次中途失败的补救
			if err := st.SetAdmin(usr.ID, true); err != nil {
				return res, err
			}
		}
	}
	defaults := map[string]func() string{
		"local.codex_path":   detectCodex,
		"local.default_mode": func() string { return "supervised" },
		"local.default_cap":  func() string { return "8" },
	}
	for k, def := range defaults {
		if v, _ := st.GetSetting(k); v == "" {
			if d := def(); d != "" {
				if err := st.SetSetting(k, d); err != nil {
					return res, err
				}
			}
		}
	}
	if err := m.migrateWith(st); err != nil {
		return res, err
	}
	lms, err := st.LocalModules()
	if err != nil {
		return res, err
	}
	for _, lm := range lms {
		if err := writeModuleFiles(lm.Dir, lm.Name); err != nil {
			// 项目目录可能已被挪走/删除：不挡安装，只记日志
			slog.Warn("本地模块文件未能更新", "module", lm.Name, "dir", lm.Dir, "err", err)
		}
	}
	return res, nil
}

// writeModuleFiles：信箱目录、协议（手改过的只记日志）、CLAUDE.md/AGENTS.md 指针块。
func writeModuleFiles(dir, name string) error {
	if err := local.EnsureMailDir(dir, name); err != nil {
		return err
	}
	if err := local.WriteProtocol(dir); err != nil {
		if !errors.Is(err, local.ErrProtocolCustom) {
			return err
		}
		slog.Info("relais/PROTOCOL.md 已被手改，保留不覆盖", "dir", dir)
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := local.EnsurePointer(filepath.Join(dir, f)); err != nil {
			return err
		}
	}
	return nil
}

// ensureLocalDirLine：server.toml 缺 local_dir 时追加一行（M7 生成的旧文件）。
func ensureLocalDirLine(scPath, ld string) error {
	data, err := os.ReadFile(scPath)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "local_dir") {
		return nil
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return os.WriteFile(scPath, append(data, []byte(fmt.Sprintf("local_dir = %q\n", ld))...), 0o600)
}

// migrateProjectsToml：M8 的 sides/claude/projects.toml → local_modules（一次性，幂等）。
// 旧频道的历史消息不重新归档：archived_seq 置为该频道当前最大 seq。
func (m *localManager) migrateProjectsToml() error {
	st, release, err := m.withStore()
	if err != nil {
		return err
	}
	defer release()
	return m.migrateWith(st)
}

func (m *localManager) migrateWith(st *store.Store) error {
	ps, err := loadProjectsIn(filepath.Join(m.ld, "sides", "claude"))
	if err != nil {
		return err
	}
	for _, p := range ps {
		ch, err := st.ChannelByName(p.Channel)
		if err != nil {
			continue
		}
		if _, err := st.LocalModuleByName(p.Channel); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return err
			}
			continue
		}
		if err := st.UpsertLocalModule(ch.ID, p.Dir); err != nil {
			return err
		}
		seq, err := st.MaxSeq(ch.ID)
		if err != nil {
			return err
		}
		if err := st.SetArchivedSeq(ch.ID, seq); err != nil {
			return err
		}
		// 旧 kickoff 不占 seq，archived_seq 管不到它：记成已归档、已投，免得第一轮把历史
		// 开工通知全重放一遍（终审修复 #3）。
		if err := st.MarkChannelDelivered(ch.ID); err != nil {
			return err
		}
	}
	return nil
}

// moduleRefs：守卫用——未关闭的登记模块。
func (m *localManager) moduleRefs() ([]local.ModuleRef, error) {
	st, release, err := m.withStore()
	if err != nil {
		return nil, err
	}
	defer release()
	lms, err := st.LocalModules()
	if err != nil {
		return nil, err
	}
	var out []local.ModuleRef
	for _, lm := range lms {
		if lm.ClosedAt != "" {
			continue
		}
		if a, err := st.GetAuto(lm.ChannelID); err != nil || a.Closed {
			continue
		}
		out = append(out, local.ModuleRef{ChannelID: lm.ChannelID, Name: lm.Name, Dir: lm.Dir})
	}
	return out, nil
}

func (m *localManager) users() (map[string]int64, error) {
	st, release, err := m.withStore()
	if err != nil {
		return nil, err
	}
	defer release()
	return usersIn(st)
}

func usersIn(st *store.Store) (map[string]int64, error) {
	out := map[string]int64{}
	for _, n := range []string{"claude", "codex", localHuman} {
		u, err := st.UserByName(n)
		if err != nil {
			return nil, fmt.Errorf("账号 %s 不存在，请先运行安装（relais local bootstrap）", n)
		}
		out[n] = u.ID
	}
	return out, nil
}

// newDaemon：给 RunServe 用的守卫（Task 10 接线）。开一个长期 store 句柄，本管理器此后也复用它；
// Redeliver 经 m.daemon 调用。
func (m *localManager) newDaemon(publish func(int64, *store.Message, string)) (*local.Daemon, error) {
	st, _, err := m.open()
	if err != nil {
		return nil, err
	}
	users, err := usersIn(st)
	if err != nil {
		st.Close()
		return nil, err
	}
	m.shared = st
	d := &local.Daemon{
		Store:   st,
		Modules: m.moduleRefs,
		CodexPath: func() string {
			if p, _ := st.GetSetting("local.codex_path"); p != "" {
				return p
			}
			return detectCodex()
		},
		Publish: publish,
		Notify:  func(title, body string) { notifyDesktop(title, body) },
		NotifyEveryLetter: func() bool {
			v, _ := st.GetSetting("local.notify_every_letter")
			return v == "true"
		},
		Log:   slog.Default(),
		Users: users,
	}
	m.daemon = d
	return d, nil
}

// serialized：守卫在跑时，把会挪动/改写信箱目录的操作放进守卫的轮次锁里（终审修复 #4）。
func (m *localManager) serialized(fn func() error) error {
	if m.daemon != nil {
		return m.daemon.Do(fn)
	}
	return fn()
}

// mailboxMissing：守卫在跑时以它上一轮的观察为准；没有守卫（CLI）时直接看目录。
func (m *localManager) mailboxMissing(lm store.LocalModule) bool {
	if m.daemon != nil {
		return slices.Contains(m.daemon.MissingModules(), lm.ChannelID)
	}
	st, err := os.Stat(local.MailDir(lm.Dir, lm.Name))
	return err != nil || !st.IsDir()
}

func (m *localManager) ScanRepos() ([]api.LocalRepo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var out []api.LocalRepo
	// macOS 受 TCC 保护或与代码无关的家目录顶层：不进，免得 launchd 下的 serve 触发隐私弹窗/静默拒绝
	skipTop := map[string]bool{"Library": true, "Applications": true, "Movies": true, "Music": true, "Pictures": true, "Public": true,
		"Desktop": true, "Documents": true, "Downloads": true} // 桌面/文稿/下载受 TCC 保护：扫描不进，手填路径仍可用
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || (dir == home && skipTop[e.Name()]) {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if st, err := os.Stat(filepath.Join(p, ".git")); err == nil && st.IsDir() {
				info, _ := e.Info()
				out = append(out, api.LocalRepo{Dir: p, Name: e.Name(), ModifiedAt: info.ModTime()})
				continue
			}
			if depth > 1 {
				walk(p, depth-1)
			}
		}
	}
	walk(home, 2)
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	if len(out) > 50 {
		out = out[:50]
	}
	return out, nil
}

func (m *localManager) CreateModule(req api.LocalModuleRequest) (api.LocalModule, error) {
	name, dir := req.Name, req.Dir
	var out api.LocalModule
	if !local.ValidModuleName(name) {
		return out, invalid("模块名 %q 不能为空、首尾空格、以 . 开头、含斜杠、.. 或控制字符", name)
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || slices.Contains(strings.Split(dir, string(filepath.Separator)), "..") {
		return out, invalid("目录必须是绝对路径且不含 ..")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		if err != nil && errors.Is(err, fs.ErrPermission) {
			return out, invalid("macOS 未授权 relais 访问该文件夹：系统设置 → 隐私与安全性 → 文件与文件夹（或完全磁盘访问）里允许 relais，然后重试")
		}
		return out, invalid("目录 %q 不存在", dir)
	}
	st, release, err := m.withStore()
	if err != nil {
		return out, err
	}
	defer release()
	registered := false
	if lm, err := st.LocalModuleByName(name); err == nil && lm.Dir != dir {
		return out, invalid("模块 %q 已绑定目录 %s，不能改绑；请删除后重建或换个名字", name, lm.Dir)
	} else if errors.Is(err, sql.ErrNoRows) {
		// 未登记却已有归档信（删模块时保留了文件）：新频道从 seq 1 重来会覆盖旧信，拒绝
		if ls, _ := local.ListLetters(local.MailDir(dir, name)); len(ls) > 0 {
			return out, invalid("目录里已有模块 %q 的旧信件（relais/mail/%s/）；请先把该目录改名或删掉，再新建", name, name)
		}
	} else if err != nil {
		return out, err
	} else {
		registered = true
	}
	users, err := usersIn(st)
	if err != nil {
		return out, err
	}
	ch, err := st.ChannelByName(name)
	adopt := err == nil && !registered // 频道已存在（联网/M8 遗留）但没登记：收编它的历史
	if err != nil {
		if ch, err = st.CreateChannel(name); err != nil {
			return out, err
		}
	}
	for _, uid := range users {
		if ok, _ := st.IsMember(ch.ID, uid); !ok {
			if err := st.AddMember(ch.ID, uid); err != nil {
				return out, err
			}
		}
	}
	if a, _ := st.GetAuto(ch.ID); !a.Enabled { // 已开启的不重置（重复创建幂等、不清回合）
		mode, capN := defaultModeCap(st)
		if err := st.SetAutoEnabled(ch.ID, true, 2*capN); err != nil {
			return out, err
		}
		if err := st.SetMode(ch.ID, mode); err != nil {
			return out, err
		}
	}
	if err := st.UpsertLocalModule(ch.ID, dir); err != nil {
		return out, err
	}
	if adopt {
		// 收编已有频道：旧信不重新归档、旧 kickoff 不重放、不补投（终审修复 #3，同 migrateWith）
		seq, err := st.MaxSeq(ch.ID)
		if err != nil {
			return out, err
		}
		if err := st.SetArchivedSeq(ch.ID, seq); err != nil {
			return out, err
		}
		if err := st.MarkChannelDelivered(ch.ID); err != nil {
			return out, err
		}
	}
	if err := writeModuleFiles(dir, name); err != nil {
		return out, err
	}
	lm, err := st.LocalModuleByName(name)
	if err != nil {
		return out, err
	}
	if req.CodexThread != "" {
		if err := attachCodex(st, lm, req.CodexThread); err != nil {
			return out, err
		}
		if lm, err = st.LocalModuleByName(name); err != nil {
			return out, err
		}
	}
	return m.moduleInfo(st, lm)
}

func defaultModeCap(st *store.Store) (string, int) {
	mode, _ := st.GetSetting("local.default_mode")
	if mode != "supervised" && mode != "autopilot" {
		mode = "supervised"
	}
	capN := 8
	if v, _ := st.GetSetting("local.default_cap"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			capN = n
		}
	}
	return mode, capN
}

// moduleByName：登记表里的模块；不存在 → invalid（handler 回 400）。
func moduleByName(st *store.Store, name string) (store.LocalModule, error) {
	lm, err := st.LocalModuleByName(name)
	if errors.Is(err, sql.ErrNoRows) {
		return lm, invalid("模块 %q 不存在", name)
	}
	return lm, err
}

func parseRFC3339(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// moduleInfo：登记表 + channel_auto + 信箱文件 → 控制台看到的一行（spec §7.4）。
func (m *localManager) moduleInfo(st *store.Store, lm store.LocalModule) (api.LocalModule, error) {
	a, err := st.GetAuto(lm.ChannelID)
	if err != nil {
		return api.LocalModule{}, err
	}
	md := local.MailDir(lm.Dir, lm.Name)
	out := api.LocalModule{Name: lm.Name, Dir: lm.Dir, Mode: a.Mode, Round: store.Round(a.RoundCount), RoundCap: store.Round(a.Cap),
		NeedsHumanQ: a.NeedsHumanQ, Closed: lm.ClosedAt != "" || a.Closed}
	out.MailboxMissing = !out.Closed && m.mailboxMissing(lm)

	letters, err := local.ListLetters(md)
	if err != nil && !os.IsNotExist(err) { // 目录不在（项目被挪走）时按空信箱显示；其它错误如实报
		return api.LocalModule{}, fmt.Errorf("读模块 %q 的信箱失败: %w", lm.Name, err)
	}
	var last *local.Letter
	for i := len(letters) - 1; i >= 0; i-- {
		if letters[i].Kind != "kickoff" {
			last = &letters[i]
			break
		}
	}
	if last != nil {
		out.LastSeq, out.LastFrom, out.LastAt = last.Seq, last.From, last.Date
	}

	ss := local.ReadSideStatus(md, local.PIDAlive)
	out.Claude = api.LocalSide{Waiting: ss.ClaudeWaiting, WaitSince: ss.ClaudeWaitSince, Cursor: ss.ClaudeCursor}
	// wait 收到信就退出（Claude 就是这样被叫醒的），所以 Claude 回信期间 waiting=false。
	// 游标已经读到最后一封给 Claude 的信 = 已收信、正在回，算接上了（终审修复 #1）。
	lastForClaude := 0
	for i := len(letters) - 1; i >= 0; i-- {
		if letters[i].From != "claude" {
			lastForClaude = letters[i].Seq
			break
		}
	}
	out.Claude.Working = !ss.ClaudeWaiting && lastForClaude > 0 && ss.ClaudeCursor >= lastForClaude
	if ss.ClaudeSessionID != "" {
		if home, err := os.UserHomeDir(); err == nil {
			if sess, err := local.ListClaudeSessions(home, ""); err == nil {
				for _, s := range sess {
					if s.ID == ss.ClaudeSessionID {
						out.Claude.SessionName = s.Name
						break
					}
				}
			}
		}
	}
	out.Codex = api.LocalSide{Attached: ss.CodexAttached, ThreadName: ss.CodexThreadName, AttachedAt: ss.CodexAttachedAt,
		DeliveryError: lm.CodexDeliveryError} // 登记表上的错误在重新接入/投递成功时清掉，比最后一条投递记录更贴近"现在"
	if _, status, _, at, err := st.LastDelivery(lm.ChannelID, "codex"); err == nil {
		out.Codex.LastDelivery, out.Codex.DeliveryAt = status, parseRFC3339(at)
	}

	if a.Resolved {
		pc := &api.LocalConclusion{AwaitingConfirm: a.Mode == "supervised"}
		if seq, err := st.SeqOf(a.ResolutionMsgID); err == nil {
			pc.Seq = seq
			pc.Path = filepath.Join(md, local.ConclusionName(seq))
			for _, l := range letters {
				if l.Seq == seq && l.Kind != "kickoff" {
					pc.Owner, pc.Summary = l.Owner, l.Summary
				}
			}
		}
		out.PendingConclusion = pc
	}

	if entries, err := os.ReadDir(filepath.Join(md, "outbox")); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".rejected") {
				out.Rejected = append(out.Rejected, e.Name())
			}
		}
	}

	waitingYou := a.NeedsHumanQ != "" || out.Codex.DeliveryError != "" || len(out.Rejected) > 0 || out.MailboxMissing
	switch {
	case out.Closed:
		out.State = "已关闭"
	case a.KickedOff:
		out.State = "已开工"
	case a.Resolved && a.Mode == "supervised":
		out.State = "等你"
	case a.Resolved:
		out.State = "已握手"
	case waitingYou:
		out.State = "等你"
	case !out.Claude.Waiting && !out.Claude.Working && !out.Codex.Attached && out.LastSeq == 0:
		out.State = "未接入"
	default:
		out.State = "讨论中"
	}

	switch {
	case out.Closed || a.KickedOff:
	case a.Resolved:
		if out.PendingConclusion != nil {
			out.WaitingFor = out.PendingConclusion.Owner
		}
	case a.NeedsHumanQ != "":
		out.WaitingFor = "user"
	case last != nil && last.From == localHuman:
		body := ""
		if data, err := os.ReadFile(last.Path); err == nil {
			_, body, _ = local.ParseLetter(data)
		}
		out.WaitingFor = local.FirstResponder(*last, body, letters)
	case last != nil && (last.From == "claude" || last.From == "codex"):
		out.WaitingFor = map[string]string{"claude": "codex", "codex": "claude"}[last.From]
	}
	return out, nil
}

func (m *localManager) ListModules() ([]api.LocalModule, error) {
	st, release, err := m.withStore()
	if err != nil {
		return nil, err
	}
	defer release()
	lms, err := st.LocalModules()
	if err != nil {
		return nil, err
	}
	out := []api.LocalModule{}
	for _, lm := range lms {
		info, err := m.moduleInfo(st, lm)
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, nil
}

func (m *localManager) PatchModule(name string, p api.LocalModulePatch) (api.LocalModule, error) {
	var out api.LocalModule
	if p.Mode != "" && p.Mode != "supervised" && p.Mode != "autopilot" {
		return out, invalid("模式只能是 supervised 或 autopilot")
	}
	if p.RoundCap < 0 {
		return out, invalid("回合上限必须大于 0")
	}
	st, release, err := m.withStore()
	if err != nil {
		return out, err
	}
	defer release()
	lm, err := moduleByName(st, name)
	if err != nil {
		return out, err
	}
	if p.Name != "" && p.Name != name {
		if err := m.serialized(func() error { return renameModule(st, lm, p.Name) }); err != nil {
			return out, err
		}
		name = p.Name
	}
	if p.Mode != "" {
		if err := st.SetMode(lm.ChannelID, p.Mode); err != nil {
			return out, err
		}
	}
	if p.RoundCap > 0 {
		if err := st.SetCap(lm.ChannelID, 2*p.RoundCap); err != nil {
			return out, err
		}
	}
	if lm, err = st.LocalModuleByName(name); err != nil {
		return out, err
	}
	return m.moduleInfo(st, lm)
}

// renameModule：频道改名 + 信箱目录跟着挪；挪目录失败时把频道名改回去。
func renameModule(st *store.Store, lm store.LocalModule, newName string) error {
	if !local.ValidModuleName(newName) {
		return invalid("模块名 %q 不能为空、首尾空格、以 . 开头、含斜杠、.. 或控制字符", newName)
	}
	oldDir, newDir := local.MailDir(lm.Dir, lm.Name), local.MailDir(lm.Dir, newName)
	if _, err := os.Lstat(newDir); err == nil {
		return invalid("目录 %s 已存在，不能改名为 %q", newDir, newName)
	}
	if err := st.RenameChannel(lm.ChannelID, newName); err != nil {
		if errors.Is(err, store.ErrChannelExists) {
			return invalid("模块名 %q 已被占用", newName)
		}
		return err
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		if os.IsNotExist(err) { // 旧信箱不在（项目被清理过）：直接建新的
			if err := local.EnsureMailDir(lm.Dir, newName); err == nil {
				return nil
			}
		}
		if rbErr := st.RenameChannel(lm.ChannelID, lm.Name); rbErr != nil {
			return fmt.Errorf("挪信箱目录失败（%v），且频道名回滚失败: %w", err, rbErr)
		}
		return invalid("挪信箱目录失败：%v", err)
	}
	return nil
}

func (m *localManager) CloseModule(name string) error {
	st, release, err := m.withStore()
	if err != nil {
		return err
	}
	defer release()
	lm, err := moduleByName(st, name)
	if err != nil {
		return err
	}
	if err := st.CloseChannel(lm.ChannelID); err != nil {
		return err
	}
	return st.SetLocalClosed(lm.ChannelID, time.Now().UTC().Format(time.RFC3339))
}

func (m *localManager) ReopenModule(name string) error {
	st, release, err := m.withStore()
	if err != nil {
		return err
	}
	defer release()
	lm, err := moduleByName(st, name)
	if err != nil {
		return err
	}
	if err := st.ReopenChannel(lm.ChannelID); err != nil {
		return err
	}
	return st.SetLocalClosed(lm.ChannelID, "")
}

// DeleteModule：删频道与登记；files 时再删信箱目录（只删 <dir>/relais/mail/<模块>，先核对路径）。
func (m *localManager) DeleteModule(name string, files bool) error {
	st, release, err := m.withStore()
	if err != nil {
		return err
	}
	defer release()
	return m.serialized(func() error { return deleteModule(st, name, files) })
}

func deleteModule(st *store.Store, name string, files bool) error {
	lm, err := moduleByName(st, name)
	if err != nil {
		return err
	}
	md := local.MailDir(lm.Dir, lm.Name)
	if files {
		root := filepath.Join(lm.Dir, "relais", "mail")
		rel, err := filepath.Rel(root, md)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || strings.ContainsRune(rel, filepath.Separator) {
			return invalid("信箱路径 %s 不在 %s 下，拒绝删除", md, root)
		}
		// relais/mail（或 relais）若是指向项目外的符号链接，RemoveAll 会删到项目外：解析后再核对
		if realRoot, err := filepath.EvalSymlinks(root); err == nil {
			realDir, err := filepath.EvalSymlinks(lm.Dir)
			if err != nil {
				return err
			}
			if r, err := filepath.Rel(realDir, realRoot); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return invalid("信箱目录是符号链接，拒绝删除")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := st.DeleteChannel(lm.ChannelID); err != nil {
		return err
	}
	if files {
		return os.RemoveAll(md)
	}
	return nil
}

func (m *localManager) Conversations(side, dir string) ([]api.LocalConversation, error) {
	out := []api.LocalConversation{}
	switch side {
	case "codex":
		ts, err := local.ListCodexThreads(codexHome(), dir)
		if err != nil {
			return nil, err
		}
		for _, t := range ts {
			out = append(out, api.LocalConversation{ID: t.ID, Name: t.Name, Title: t.Title, Cwd: t.Cwd, UpdatedAt: t.UpdatedAt})
		}
	case "claude":
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		ss, err := local.ListClaudeSessions(home, dir)
		if err != nil {
			return nil, err
		}
		for _, s := range ss {
			out = append(out, api.LocalConversation{ID: s.ID, Name: s.Name, Cwd: s.Cwd, Status: s.Status, UpdatedAt: s.UpdatedAt})
		}
	default:
		return nil, invalid("side 只能是 claude 或 codex")
	}
	return out, nil
}

// AttachModule：登记 codex 侧接入的对话（claude 侧的接入就是在对话里跑 relais wait，不登记）。
func (m *localManager) AttachModule(name string, req api.LocalAttachRequest) (api.LocalModule, error) {
	var out api.LocalModule
	if req.Side != "codex" {
		return out, invalid("只有 codex 侧需要登记接入；claude 侧在对话里运行 relais wait 即可")
	}
	st, release, err := m.withStore()
	if err != nil {
		return out, err
	}
	defer release()
	lm, err := moduleByName(st, name)
	if err != nil {
		return out, err
	}
	if err := m.serialized(func() error { return attachCodex(st, lm, req.Thread) }); err != nil {
		return out, err
	}
	if lm, err = st.LocalModuleByName(name); err != nil {
		return out, err
	}
	return m.moduleInfo(st, lm)
}

// attachCodex：thread 空 → 该目录最新的 Codex 对话；否则按 id 或对话名精确匹配。写 .attach-codex + 登记表。
func attachCodex(st *store.Store, lm store.LocalModule, thread string) error {
	var t local.Thread
	var err error
	if thread == "" {
		t, err = local.FindCodexThread(codexHome(), lm.Dir)
	} else {
		t, err = local.CodexThreadByIDOrName(codexHome(), thread)
	}
	if err != nil {
		return invalid("%v", err)
	}
	name := t.Name
	if name == "" {
		name = t.Title
	}
	if err := local.EnsureMailDir(lm.Dir, lm.Name); err != nil {
		return err
	}
	at := time.Now().UTC().Truncate(time.Second)
	if err := local.WriteAttach(local.MailDir(lm.Dir, lm.Name), local.Attach{ThreadID: t.ID, Name: name, Cwd: t.Cwd, At: at}); err != nil {
		return err
	}
	return st.SetCodexAttach(lm.ChannelID, t.ID, name, at.Format(time.RFC3339))
}

func (m *localManager) Redeliver(name string) error {
	st, release, err := m.withStore()
	if err != nil {
		return err
	}
	lm, err := moduleByName(st, name)
	release()
	if err != nil {
		return err
	}
	if m.daemon == nil {
		return errors.New("守卫未启动（relais serve 未带本地模式运行），无法重投")
	}
	return m.daemon.Redeliver(lm.ChannelID)
}

func (m *localManager) Settings() (api.LocalSettings, error) {
	st, release, err := m.withStore()
	if err != nil {
		return api.LocalSettings{}, err
	}
	defer release()
	var s api.LocalSettings
	s.CodexPath, _ = st.GetSetting("local.codex_path")
	if s.CodexPath == "" {
		s.CodexPath = detectCodex()
	}
	s.CodexOK = s.CodexPath != "" && isExecutable(s.CodexPath)
	s.DefaultMode, s.DefaultCap = defaultModeCap(st)
	v, _ := st.GetSetting("local.notify_every_letter")
	s.NotifyEveryLetter = v == "true"
	return s, nil
}

func (m *localManager) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode != "supervised" && s.DefaultMode != "autopilot" {
		return invalid("默认模式只能是 supervised 或 autopilot")
	}
	if s.DefaultCap <= 0 {
		return invalid("默认回合上限必须大于 0")
	}
	codexPath := s.CodexPath
	if codexPath != "" {
		if fi, err := os.Stat(codexPath); err != nil || !fi.Mode().IsRegular() {
			return invalid("codex 路径 %q 不存在或不是普通文件", codexPath)
		}
		codexPath, _ = filepath.Abs(codexPath)
	}
	st, release, err := m.withStore()
	if err != nil {
		return err
	}
	defer release()
	notify := "false"
	if s.NotifyEveryLetter {
		notify = "true"
	}
	for k, v := range map[string]string{
		"local.codex_path": codexPath, "local.default_mode": s.DefaultMode,
		"local.default_cap": strconv.Itoa(s.DefaultCap), "local.notify_every_letter": notify,
	} {
		if err := st.SetSetting(k, v); err != nil {
			return err
		}
	}
	return nil
}

func (m *localManager) State() api.LocalState {
	s, _ := m.Settings()
	return api.LocalState{Version: server.Version, StartedAt: m.startedAt, CodexOK: s.CodexOK}
}
