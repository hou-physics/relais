package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

// ErrLocalInvalid：本地管理接口的"输入无效"哨兵，handler 据此回 400 而非 500。
var ErrLocalInvalid = errors.New("本地管理：输入无效")

// LocalManager：本地模式的环境/模块操作，由 cli 包实现并经 SetLocal 注入（D44）。
type LocalManager interface {
	ScanRepos() ([]api.LocalRepo, error)
	ListModules() ([]api.LocalModule, error)
	CreateModule(name, dir string) (api.LocalModule, error)
	CloseModule(name string) error
	Rules(name string) (string, error)
	PutRules(name, text string) error
	Settings() (api.LocalSettings, error)
	PutSettings(api.LocalSettings) error
}

const heartbeatAlive = 20 * time.Second

type heartbeats struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (h *heartbeats) beat(side string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.last == nil {
		h.last = map[string]time.Time{}
	}
	h.last[side] = time.Now()
}

func (h *heartbeats) snapshot() (alive map[string]bool, last map[string]time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	alive, last = map[string]bool{}, map[string]time.Time{}
	for k, v := range h.last {
		last[k] = v
		alive[k] = time.Since(v) < heartbeatAlive
	}
	return
}

// SetLocal 注入本地管理器；注入后 Handler() 才注册 /api/local/*（线上不暴露，spec §3.1）。
func (s *Server) SetLocal(m LocalManager) { s.local = m }

func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// localHuman：人的钥匙 + 回环双重限制（spec §8）。
func (s *Server) localHuman(h func(http.ResponseWriter, *http.Request, principal)) func(http.ResponseWriter, *http.Request, principal) {
	return func(w http.ResponseWriter, r *http.Request, p principal) {
		if p.agent {
			writeErr(w, http.StatusForbidden, "本地管理仅限网页（人的钥匙）")
			return
		}
		if !isLoopback(r.RemoteAddr) {
			writeErr(w, http.StatusForbidden, "本地管理只接受本机请求")
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
	mux.HandleFunc("GET /api/local/repos", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
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
	mux.HandleFunc("GET /api/local/modules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		mods, err := s.local.ListModules()
		if err != nil {
			s.localErr(w, err)
			return
		}
		alive, last := s.beats.snapshot()
		for i := range mods {
			mods[i].BridgeAlive, mods[i].LastHeartbeat = alive, last
		}
		if mods == nil {
			mods = []api.LocalModule{}
		}
		writeJSON(w, http.StatusOK, mods)
	})))
	mux.HandleFunc("POST /api/local/modules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalModuleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		m, err := s.local.CreateModule(req.Name, req.Dir)
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, m)
	})))
	mux.HandleFunc("POST /api/local/modules/{name}/close", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		if err := s.local.CloseModule(r.PathValue("name")); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/local/modules/{name}/rules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		text, err := s.local.Rules(r.PathValue("name"))
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, api.LocalRules{Text: text})
	})))
	mux.HandleFunc("PUT /api/local/modules/{name}/rules", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		var req api.LocalRules
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求格式不对")
			return
		}
		if err := s.local.PutRules(r.PathValue("name"), req.Text); err != nil {
			s.localErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})))
	mux.HandleFunc("GET /api/local/settings", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
		st, err := s.local.Settings()
		if err != nil {
			s.localErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, st)
	})))
	mux.HandleFunc("PUT /api/local/settings", s.auth(s.localHuman(func(w http.ResponseWriter, r *http.Request, p principal) {
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
	mux.HandleFunc("POST /api/local/heartbeat", s.auth(func(w http.ResponseWriter, r *http.Request, p principal) {
		if !p.agent {
			writeErr(w, http.StatusForbidden, "心跳仅限 agent 钥匙")
			return
		}
		s.beats.beat(p.user.Username)
		w.WriteHeader(http.StatusNoContent)
	}))
}
