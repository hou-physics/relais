package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/api"
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
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
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

func (m *localManager) CreateModule(name, dir string) (api.LocalModule, error) {
	var out api.LocalModule
	if !validModuleName(name) {
		return out, invalid("模块名 %q 不能为空、含空格或斜杠", name)
	}
	if !filepath.IsAbs(dir) || strings.Contains(dir, "..") {
		return out, invalid("目录必须是绝对路径且不含 ..")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return out, invalid("目录 %q 不存在", dir)
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
	if _, err := os.Stat(filepath.Join(dir, "relais", "config.toml")); os.IsNotExist(err) {
		if _, err := initProject(dir, cfg.BaseURL, name, "claude"); err != nil {
			return out, err
		}
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
	if err := writeLocalAgentGuide(dir, m.ld, name); err != nil { // Task 6 才有 guide.LocalText；本任务先写占位实现见下
		return out, err
	}
	return m.moduleInfo(st, name, dir)
}

// writeLocalAgentGuide：把本地模式的工作脑说明追加到 <dir>/relais/AGENT.md（Task 6 换成 guide.LocalText；本任务只保证文件存在并含标记行）。
func writeLocalAgentGuide(dir, ld, name string) error {
	p := filepath.Join(dir, "relais", "AGENT.md")
	data, _ := os.ReadFile(p)
	if strings.Contains(string(data), "## 本地模式") {
		return nil
	}
	add := fmt.Sprintf("\n## 本地模式（模块 %s）\n本侧配置目录：%s\n", name, filepath.Join(ld, "sides", "<claude|codex>"))
	return os.WriteFile(p, append(data, []byte(add)...), 0o644)
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
	n := 0
	if entries, err := os.ReadDir(filepath.Join(dir, "relais", "conclusions")); err == nil {
		for _, e := range entries {
			if _, ok := conclusionIDFor(e.Name(), name); ok {
				n++
			}
		}
	}
	return api.LocalModule{Name: name, Dir: dir, Mode: a.Mode, Round: store.Round(a.RoundCount), RoundCap: store.Round(a.Cap),
		State: state, NeedsHumanQ: a.NeedsHumanQ, Conclusions: n, BridgeAlive: map[string]bool{}, LastHeartbeat: map[string]time.Time{}}, nil
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

func (m *localManager) Rules(name string) (string, error) {
	dir, err := m.moduleDir(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "relais", "RULES.md"))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

func (m *localManager) PutRules(name, text string) error {
	dir, err := m.moduleDir(name)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "relais", "RULES.md"), []byte(text), 0o644)
}

func (m *localManager) Settings() (api.LocalSettings, error) {
	st, _, err := m.open()
	if err != nil {
		return api.LocalSettings{}, err
	}
	defer st.Close()
	var s api.LocalSettings
	s.ClaudePath, _ = st.GetSetting("local.claude_path")
	s.CodexPath, _ = st.GetSetting("local.codex_path")
	s.DefaultMode, _ = st.GetSetting("local.default_mode")
	if s.DefaultMode == "" {
		s.DefaultMode = "supervised"
	}
	return s, nil
}

func (m *localManager) PutSettings(s api.LocalSettings) error {
	if s.DefaultMode != "supervised" && s.DefaultMode != "autopilot" {
		return invalid("默认模式只能是 supervised 或 autopilot")
	}
	paths := map[string]string{}
	for name, p := range map[string]string{"claude": s.ClaudePath, "codex": s.CodexPath} {
		abs, err := checkAgentPath(name, p)
		if err != nil {
			return err
		}
		paths[name] = abs
	}
	st, cfg, err := m.open()
	if err != nil {
		return err
	}
	defer st.Close()
	for k, v := range map[string]string{"local.claude_path": paths["claude"], "local.codex_path": paths["codex"], "local.default_mode": s.DefaultMode} {
		if err := st.SetSetting(k, v); err != nil {
			return err
		}
	}
	for _, side := range []string{"claude", "codex"} {
		u, err := st.UserByName(side)
		if err != nil {
			return err
		}
		if err := m.writeSide(side, cfg.BaseURL, u.AgentToken, paths[side]); err != nil {
			return err
		}
	}
	return nil
}
