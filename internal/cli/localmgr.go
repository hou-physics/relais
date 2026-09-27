package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/guide"
	"github.com/hou-physics/relais/internal/server"
	"github.com/hou-physics/relais/internal/store"
)

// localManager：本地模式的环境层（bootstrap）与模块层（增/列/关/规矩/设置）。
// CLI（relais local …）与服务器（/api/local/*，经 server.LocalManager 接口）共用这一份逻辑（D44）。
type localManager struct {
	ld     string
	scPath string
}

func newLocalManager(ld string) *localManager {
	return &localManager{ld: ld, scPath: localServerConfigPath(ld)}
}

type bootstrapResult struct {
	BaseURL, HumanUser, HumanPassword string
	PasswordShown                     bool
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

func (m *localManager) sideDir(side string) string { return filepath.Join(m.ld, "sides", side) }

// bootstrap：只做"环境"——server.toml（含 local_dir）、三账号、两侧配置与 hook、设置；不建模块；幂等。
func (m *localManager) bootstrap(listen, claudePath, codexPath string) (bootstrapResult, error) {
	var res bootstrapResult
	if !isLoopbackListen(listen) {
		return res, fmt.Errorf("本地模式只允许监听回环地址，得到 %q", listen)
	}
	agents := map[string]string{"claude": claudePath, "codex": codexPath}
	for name, p := range agents {
		abs, err := checkAgentPath(name, p)
		if err != nil {
			return res, err
		}
		agents[name] = abs
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
	ensureUser := func(name, display string, admin bool) (*store.User, error) {
		if u, err := st.UserByName(name); err == nil {
			if admin && !u.IsAdmin { // 上次中途失败的补救
				if err := st.SetAdmin(u.ID, true); err != nil {
					return nil, err
				}
			}
			return u, nil
		}
		pw := newPassword()
		u, err := st.CreateUser(name, display, pw)
		if err != nil {
			return nil, err
		}
		if admin {
			if err := st.SetAdmin(u.ID, true); err != nil {
				return nil, err
			}
			note := fmt.Sprintf("网页 %s 登录账号: %s\n初始密码: %s\n（本文件仅首次创建时写入；改密码后可删）\n", cfg.BaseURL, name, pw)
			if err := os.WriteFile(filepath.Join(m.ld, "human.txt"), []byte(note), 0o600); err != nil {
				return nil, err
			}
			res.HumanPassword, res.PasswordShown = pw, true
		}
		return u, nil
	}
	claudeU, err := ensureUser("claude", "Claude 侧", false)
	if err != nil {
		return res, err
	}
	codexU, err := ensureUser("codex", "Codex 侧", false)
	if err != nil {
		return res, err
	}
	if _, err := ensureUser(localHuman, "Hou", true); err != nil {
		return res, err
	}
	for side, u := range map[string]*store.User{"claude": claudeU, "codex": codexU} {
		if err := m.writeSide(side, cfg.BaseURL, u.AgentToken, agents[side]); err != nil {
			return res, err
		}
	}
	for k, v := range map[string]string{"local.claude_path": agents["claude"], "local.codex_path": agents["codex"]} {
		if err := st.SetSetting(k, v); err != nil {
			return res, err
		}
	}
	if v, _ := st.GetSetting("local.default_mode"); v == "" {
		if err := st.SetSetting("local.default_mode", "supervised"); err != nil {
			return res, err
		}
	}
	return res, nil
}

func checkAgentPath(name, p string) (string, error) {
	if p == "" {
		return "", invalid("没找到 %s：请指定路径（Codex 常在 ~/.codex/plugins/.plugin-appserver/codex）", name)
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "", invalid("%s 路径 %q 不存在或不是普通文件", name, p)
	}
	abs, _ := filepath.Abs(p)
	return abs, nil
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

// writeSide：某一侧的 config.toml / setup.toml / hook。
func (m *localManager) writeSide(side, baseURL, token, agentPath string) error {
	d := m.sideDir(side)
	if err := saveGlobalTo(d, &GlobalConfig{Server: baseURL, Token: token, Username: side}); err != nil {
		return err
	}
	info := SetupInfo{OS: runtime.GOOS, Agent: side, AgentPath: agentPath, Mode: "auto"}
	hp, err := writeLocalHook(d, info)
	if err != nil {
		return err
	}
	info.HookPath = hp
	return saveSetupTo(d, info)
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

func validModuleName(name string) bool {
	return name != "" && !strings.ContainsAny(name, " /\\\t\n") && len(name) <= 64
}

// CreateModule：M9 接口签名（api.LocalModuleRequest）的适配层；req.CodexThread（M9 新字段，codex
// 侧对话登记）留给 Task 9 处理，这里先忽略。
func (m *localManager) CreateModule(req api.LocalModuleRequest) (api.LocalModule, error) {
	name, dir := req.Name, req.Dir
	var out api.LocalModule
	if !validModuleName(name) {
		return out, invalid("模块名 %q 不能为空、含空格或斜杠", name)
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
	if existing, err := m.moduleDir(name); err == nil && existing != dir {
		return out, invalid("模块 %q 已绑定目录 %s，不能改绑；请关闭后用新名字创建", name, existing)
	}
	st, cfg, err := m.open()
	if err != nil {
		return out, err
	}
	defer st.Close()
	users := map[string]*store.User{}
	for _, n := range []string{"claude", "codex", localHuman} {
		u, err := st.UserByName(n)
		if err != nil {
			return out, fmt.Errorf("账号 %s 不存在，请先运行安装（relais local bootstrap）", n)
		}
		users[n] = u
	}
	ch, err := st.ChannelByName(name)
	if err != nil {
		if ch, err = st.CreateChannel(name); err != nil {
			return out, err
		}
	}
	for _, u := range users {
		if ok, _ := st.IsMember(ch.ID, u.ID); !ok {
			if err := st.AddMember(ch.ID, u.ID); err != nil {
				return out, err
			}
		}
	}
	mode, _ := st.GetSetting("local.default_mode")
	if mode == "" {
		mode = "supervised"
	}
	if a, _ := st.GetAuto(ch.ID); !a.Enabled {
		if err := st.SetAutoEnabled(ch.ID, true, 16); err != nil { // 16 条 = 8 回合（D39/D42 ①）
			return out, err
		}
		if err := st.SetMode(ch.ID, mode); err != nil {
			return out, err
		}
	}
	for _, side := range []string{"claude", "codex"} {
		if err := registerProjectIn(m.sideDir(side), name, dir); err != nil {
			return out, err
		}
	}
	freshAgent := false // initProject 刚写了联网版 AGENT.md，本地模式要换掉它的开头
	if _, err := os.Stat(filepath.Join(dir, "relais", "config.toml")); os.IsNotExist(err) {
		if _, err := initProject(dir, cfg.BaseURL, name, "claude"); err != nil {
			return out, err
		}
		freshAgent = true
	}
	if err := os.MkdirAll(filepath.Join(dir, "relais", "conclusions"), 0o755); err != nil {
		return out, err
	}
	rules := filepath.Join(dir, "relais", "RULES.md")
	if _, err := os.Stat(rules); os.IsNotExist(err) {
		if err := os.WriteFile(rules, []byte(rulesTemplate), 0o644); err != nil {
			return out, err
		}
	}
	if err := writeLocalAgentGuide(dir, m.ld, name, freshAgent); err != nil {
		return out, err
	}
	for _, f := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := ensureLocalPointer(filepath.Join(dir, f)); err != nil {
			return out, err
		}
	}
	return m.moduleInfo(st, name, dir)
}

const (
	networkedAgentHeader = "# Relais — agent 使用说明"
	localAgentPreamble   = "# Relais — 本地模式 agent 说明\n\n本项目由 Relais 本地模式管理；下面按模块与侧列出工作脑该做的事。\n"
	localPointerMarker   = "<!-- relais-local -->"
	localPointerBlock    = "\n" + localPointerMarker + "\n## Relais 本地模式\n本项目接入了 Relais 本地模式。读 relais/AGENT.md 的「本地模式」各节：雇主说「把这个拿去讨论」或「开工」时，按那里的说明执行，不要反问。\n<!-- /relais-local -->\n"
)

// writeLocalAgentGuide：把本地模式的工作脑说明（两侧各一段，guide.LocalText）追加到 <dir>/relais/AGENT.md。
// 标记按模块区分，同一目录的第二个模块会得到自己的说明段；同一模块重复创建不重复追加。
// 本地模块不要联网版说明（relais draft/先给雇主过目会与本地流程矛盾）：initProject 刚写的，或文件仍以联网标题开头时，
// 换成本地开头，只保留已有的「## 本地模式（模块 …）」各段。读失败（非不存在）直接报错，绝不覆盖。
func writeLocalAgentGuide(dir, ld, name string, fresh bool) error {
	p := filepath.Join(dir, "relais", "AGENT.md")
	raw, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	s := string(raw)
	if fresh || strings.HasPrefix(s, networkedAgentHeader) {
		kept := ""
		if i := strings.Index(s, "\n## 本地模式（模块 "); i >= 0 {
			kept = s[i:]
		}
		s = localAgentPreamble + kept
	} else if s == "" {
		s = localAgentPreamble
	}
	marker := fmt.Sprintf("## 本地模式（模块 %s）", name)
	if !strings.Contains(s, marker) {
		s += "\n" + marker + "\n" + guide.LocalText("claude", name, filepath.Join(ld, "sides", "claude")) + guide.LocalText("codex", name, filepath.Join(ld, "sides", "codex"))
	}
	if s == string(raw) {
		return nil
	}
	return os.WriteFile(p, []byte(s), 0o644)
}

// ensureLocalPointer：Claude Code 读 CLAUDE.md、Codex 读 AGENTS.md，都不会自己去读 relais/AGENT.md；
// 在两者末尾追加一个带标记的指针块（文件不存在就新建；已有标记则不动；只追加，不改其它内容）。
func ensureLocalPointer(p string) error {
	raw, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(raw), localPointerMarker) {
		return nil
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(localPointerBlock); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (m *localManager) moduleInfo(st *store.Store, name, dir string) (api.LocalModule, error) {
	ch, err := st.ChannelByName(name)
	if err != nil {
		return api.LocalModule{}, invalid("模块 %q 不存在", name)
	}
	a, err := st.GetAuto(ch.ID)
	if err != nil {
		return api.LocalModule{}, err
	}
	state := "running"
	switch {
	case a.Closed:
		state = "closed"
	case a.KickedOff:
		state = "kicked_off"
	case a.Resolved:
		state = "resolved"
	case a.NeedsHumanQ != "":
		state = "needs_human"
	case a.Paused:
		state = "paused"
	}
	// M9 的 LocalModule 已不再有 Conclusions/BridgeAlive/LastHeartbeat 字段（改由
	// outbox/守卫循环那一套状态取代，Task 9 重写）；这里先只填还能对上的字段。
	return api.LocalModule{Name: name, Dir: dir, Mode: a.Mode, Round: store.Round(a.RoundCount), RoundCap: store.Round(a.Cap),
		State: state, NeedsHumanQ: a.NeedsHumanQ}, nil
}

// conclusionIDFor：文件名是否为 <channel>-<26位ULID>.md（与 conclusion.go 的规则一致）。
func conclusionIDFor(filename, channel string) (string, bool) {
	rest := strings.TrimSuffix(strings.TrimPrefix(filename, channel+"-"), ".md")
	if !strings.HasPrefix(filename, channel+"-") || !strings.HasSuffix(filename, ".md") || len(rest) != ulidLen || strings.Contains(rest, "-") {
		return "", false
	}
	return rest, true
}

func (m *localManager) ListModules() ([]api.LocalModule, error) {
	st, _, err := m.open()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	dirs := map[string]string{}
	if ps, err := loadProjectsIn(m.sideDir("claude")); err == nil {
		for _, p := range ps {
			dirs[p.Channel] = p.Dir
		}
	}
	chs, err := st.AllChannels()
	if err != nil {
		return nil, err
	}
	out := []api.LocalModule{}
	for _, c := range chs {
		ch, err := st.ChannelByName(c.Name)
		if err != nil {
			continue
		}
		a, _ := st.GetAuto(ch.ID)
		if !a.Enabled && !a.Closed {
			continue
		}
		info, err := m.moduleInfo(st, c.Name, dirs[c.Name])
		if err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *localManager) CloseModule(name string) error {
	st, _, err := m.open()
	if err != nil {
		return err
	}
	defer st.Close()
	ch, err := st.ChannelByName(name)
	if err != nil {
		return invalid("模块 %q 不存在", name)
	}
	if err := st.CloseChannel(ch.ID); err != nil {
		return err
	}
	for _, side := range []string{"claude", "codex"} {
		if err := sessionClear(m.sideDir(side), name); err != nil {
			return err
		}
	}
	return nil
}

func (m *localManager) moduleDir(name string) (string, error) {
	ps, err := loadProjectsIn(m.sideDir("claude"))
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if p.Channel == name {
			return p.Dir, nil
		}
	}
	return "", invalid("模块 %q 不存在", name)
}

// Settings/PutSettings：M9 的 api.LocalSettings 去掉了 ClaudePath（不再是 codex 之外可经此接口
// 改的字段）、加了 CodexOK/DefaultCap/NotifyEveryLetter。这里只做字段级适配，语义（比如
// default_cap/notify 具体怎么用）留给 Task 9。
func (m *localManager) Settings() (api.LocalSettings, error) {
	st, _, err := m.open()
	if err != nil {
		return api.LocalSettings{}, err
	}
	defer st.Close()
	var s api.LocalSettings
	s.CodexPath, _ = st.GetSetting("local.codex_path")
	s.DefaultMode, _ = st.GetSetting("local.default_mode")
	if s.DefaultMode == "" {
		s.DefaultMode = "supervised"
	}
	if capStr, _ := st.GetSetting("local.default_cap"); capStr != "" {
		if n, err := strconv.Atoi(capStr); err == nil {
			s.DefaultCap = n
		}
	}
	if s.DefaultCap == 0 {
		s.DefaultCap = 8
	}
	notify, _ := st.GetSetting("local.notify_every_letter")
	s.NotifyEveryLetter = notify == "true"
	if fi, err := os.Stat(s.CodexPath); err == nil && fi.Mode().IsRegular() {
		s.CodexOK = true
	}
	return s, nil
}

func (m *localManager) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode != "supervised" && s.DefaultMode != "autopilot" {
		return invalid("默认模式只能是 supervised 或 autopilot")
	}
	codexAbs, err := checkAgentPath("codex", s.CodexPath)
	if err != nil {
		return err
	}
	st, cfg, err := m.open()
	if err != nil {
		return err
	}
	defer st.Close()
	cap := s.DefaultCap
	if cap <= 0 {
		cap = 8
	}
	notify := "false"
	if s.NotifyEveryLetter {
		notify = "true"
	}
	for k, v := range map[string]string{
		"local.codex_path": codexAbs, "local.default_mode": s.DefaultMode,
		"local.default_cap": strconv.Itoa(cap), "local.notify_every_letter": notify,
	} {
		if err := st.SetSetting(k, v); err != nil {
			return err
		}
	}
	u, err := st.UserByName("codex")
	if err != nil {
		return err
	}
	return m.writeSide("codex", cfg.BaseURL, u.AgentToken, codexAbs)
}

// ---- M9 Task 9 未完成：以下方法只满足 server.LocalManager 接口以保持编译通过，
// 真实实现（模块改名/重开/删除、对话列表、codex 接入登记、重投、汇总状态）由 Task 9 补齐。

func (m *localManager) PatchModule(name string, p api.LocalModulePatch) (api.LocalModule, error) {
	return api.LocalModule{}, errors.New("M9 Task 9 未完成")
}

func (m *localManager) ReopenModule(name string) error {
	return errors.New("M9 Task 9 未完成")
}

func (m *localManager) DeleteModule(name string, files bool) error {
	return errors.New("M9 Task 9 未完成")
}

func (m *localManager) Conversations(side, dir string) ([]api.LocalConversation, error) {
	return nil, errors.New("M9 Task 9 未完成")
}

func (m *localManager) AttachModule(name string, req api.LocalAttachRequest) (api.LocalModule, error) {
	return api.LocalModule{}, errors.New("M9 Task 9 未完成")
}

func (m *localManager) Redeliver(name string) error {
	return errors.New("M9 Task 9 未完成")
}

func (m *localManager) State() api.LocalState {
	return api.LocalState{}
}
