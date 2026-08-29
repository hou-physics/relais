// Package server 实现 Relais 的 HTTP API、SSE 与内嵌网页。
// 权限语义（spec §5）由 store 层查询强制，本包只负责解析"哪把钥匙"。
package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

type Server struct {
	st      *store.Store
	baseURL string
	dataDir string
}

func New(st *store.Store, baseURL, dataDir string) *Server {
	return &Server{st: st, baseURL: baseURL, dataDir: dataDir}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe))
	mux.HandleFunc("GET /api/channels", s.auth(s.handleChannels))
	mux.HandleFunc("GET /api/channels/{name}/members", s.auth(s.handleMembers))
	mux.HandleFunc("GET /api/channels/{name}/messages", s.auth(s.handleList))
	mux.HandleFunc("POST /api/channels/{name}/messages", s.auth(s.handleSend))
	mux.HandleFunc("GET /api/messages/{id}", s.auth(s.handleGet))
	mux.HandleFunc("POST /api/messages/{id}/read", s.auth(s.handleRead))
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, format string, args ...any) {
	writeJSON(w, code, api.ErrorResponse{Error: fmt.Sprintf(format, args...)})
}

func (s *Server) publish(channelID int64, m api.Message) {
	// Task 9 实现；本任务先加空方法占位
}
