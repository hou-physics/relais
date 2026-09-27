package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

// ErrLocalInvalid：本地管理接口的"输入无效"哨兵，handler 据此回 400 而非 500。
var ErrLocalInvalid = errors.New("本地管理：输入无效")

// LocalManager：本地模式的模块操作，由 cli 包实现并经 SetLocal 注入（M9）。
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

// SetLocal 注入本地管理器；注入后 Handler() 才注册 /api/local/* 与 /local/*（线上不暴露）。
// humanUser：回环免钥匙落到的用户名（"hou"）。
func (s *Server) SetLocal(m LocalManager, humanUser string) {
	s.local = m
	s.localHumanUser = humanUser
}

// PublishMessage：守卫入库的消息推到 SSE（与 handleSend 同一出口）。
func (s *Server) PublishMessage(channelID int64, m *store.Message, channelName string) {
	s.publish(channelID, toAPI(m, channelName, false))
}

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// isLoopbackHost：hostname（可能带端口）是否字面写的就是回环地址——与 isLoopback 不同，
// 这里判断的是 Host/Origin 头里的"名字"本身，而不是 TCP 连接的实际来源 IP。
func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	switch strings.ToLower(host) {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	default:
		return false
	}
}

// normHostPort：小写 host:port；没写端口按 scheme 补默认端口（http 80、https 443），
// IPv6 去掉方括号后再拼，供 Origin 与 Host 比较。
func normHostPort(hostport, scheme string) string {
	hostport = strings.ToLower(hostport)
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = strings.Trim(hostport, "[]"), "80"
		if strings.ToLower(scheme) == "https" {
			port = "443"
		}
	}
	return net.JoinHostPort(host, port)
}

// loopbackBrowserOK：本地模式回环免钥匙的第二道判断，挡两类攻击——
//  1. DNS rebinding：攻击者控制的域名先解析到真实公网 IP 通过校验，再改解析到
//     127.0.0.1；此时 TCP 连接确实来自回环（isLoopback 为真），但请求的 Host 头
//     仍是攻击者的域名，不是字面的回环地址，得单独挡。
//  2. CSRF：浏览器对 127.0.0.1 的"简单请求"（如 Content-Type: text/plain 的表单
//     POST）不受同源策略限制，任意网站都能悄悄发出；靠 Origin（写请求且带
//     Origin 时必须也是回环）或没有 Origin 时的 Sec-Fetch-Site 兜底挡。
//
// 只在回环免钥匙这条捷径上生效，token/cookie 路径不受影响。
func loopbackBrowserOK(r *http.Request) (ok bool, reason string) {
	if !isLoopbackHost(r.Host) {
		return false, "本地控制台只接受本机地址访问"
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true, ""
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		// Origin 必须与 Host 同一个 host:port——回环上别的端口（本机别的开发服务器里的
		// 网页）也不算同源（终审修复 minor）。
		u, err := url.Parse(origin)
		if err != nil || !isLoopbackHost(u.Host) || normHostPort(u.Host, u.Scheme) != normHostPort(r.Host, "http") {
			return false, "本地控制台拒绝跨站请求"
		}
		return true, ""
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
		return true, ""
	default: // cross-site、same-site 一律拒绝
		return false, "本地控制台拒绝跨站请求"
	}
}

// localOnly：本地控制台只检查回环，不再区分人/agent 钥匙（M9：守卫循环可能用 agent 钥匙自己敲接口）。
func (s *Server) localOnly(h func(http.ResponseWriter, *http.Request, principal)) func(http.ResponseWriter, *http.Request, principal) {
	return func(w http.ResponseWriter, r *http.Request, p principal) {
		if !isLoopback(r.RemoteAddr) {
			writeErr(w, http.StatusUnauthorized, "本地控制台只接受本机请求")
			return
		}
		h(w, r, p)
	}
}

func (s *Server) localErr(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrLocalInvalid) {
		writeErr(w, http.StatusBadRequest, "%s", err.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, "本地管理失败: %v", err)
}

func (s *Server) registerLocalRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/local/state", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		writeJSON(w, http.StatusOK, s.local.State())
	})))
	mux.HandleFunc("GET /api/local/repos", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		repos, err := s.local.ScanRepos()
		if err != nil {
			s.localErr(w, err)
			return
		}
		if repos == nil {
			repos = []api.LocalRepo{}
		}
		writeJSON(w, http.StatusOK, repos)
	})))
	mux.HandleFunc("GET /api/local/modules", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		mods, err := s.local.ListModules()
		if err != nil {
			s.localErr(w, err)
			return
		}
		if mods == nil {
			mods = []api.LocalModule{}
		}
		writeJSON(w, http.StatusOK, mods)
	})))
	mux.HandleFunc("POST /api/local/modules", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalModuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		m, err := s.local.CreateModule(req)
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})))
	mux.HandleFunc("PATCH /api/local/modules/{name}", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalModulePatch
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		m, err := s.local.PatchModule(r.PathValue("name"), req)
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})))
	mux.HandleFunc("POST /api/local/modules/{name}/close", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		if err := s.local.CloseModule(r.PathValue("name")); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("POST /api/local/modules/{name}/reopen", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		if err := s.local.ReopenModule(r.PathValue("name")); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("DELETE /api/local/modules/{name}", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		files := r.URL.Query().Get("files") == "1"
		if err := s.local.DeleteModule(r.PathValue("name"), files); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/local/conversations", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		side := r.URL.Query().Get("side")
		if side != "claude" && side != "codex" {
			writeErr(w, http.StatusBadRequest, "side 必须是 claude 或 codex")
			return
		}
		convs, err := s.local.Conversations(side, r.URL.Query().Get("dir"))
		if err != nil {
			// 头字段只能放 ASCII 直接字节，中文错误文案原样塞进去在浏览器里会乱码；
			// 转义成 %xx，前端用 decodeURIComponent 解回来。
			w.Header().Set("X-Relais-Error", url.QueryEscape(err.Error()))
			writeJSON(w, http.StatusOK, []api.LocalConversation{})
			return
		}
		if convs == nil {
			convs = []api.LocalConversation{}
		}
		writeJSON(w, http.StatusOK, convs)
	})))
	mux.HandleFunc("POST /api/local/modules/{name}/attach", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalAttachRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		m, err := s.local.AttachModule(r.PathValue("name"), req)
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})))
	mux.HandleFunc("POST /api/local/modules/{name}/redeliver", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		if err := s.local.Redeliver(r.PathValue("name")); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/local/settings", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		st, err := s.local.Settings()
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})))
	mux.HandleFunc("PUT /api/local/settings", s.auth(s.localOnly(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalSettings
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		if err := s.local.PutSettings(req); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	// 兜底：/api/local/ 下未列出的子路径（如已删除的 heartbeat/rules）一律 404，不落到
	// 全局的 "GET /" 静态兜底（那样非 GET 方法会被判成 405）。按方法逐个注册——一条不带
	// 方法的 "/api/local/" 会和 "GET /" 产生 net/http 判为歧义的 pattern 冲突（panic）。
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		mux.Handle(method+" /api/local/", http.NotFoundHandler())
	}
}
