// Package server 实现 Relais 的 HTTP API、SSE 与内嵌网页。
// 权限语义（spec §5）由 store 层查询强制，本包只负责解析"哪把钥匙"。
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

//go:embed web
var webFS embed.FS

type Server struct {
	st      *store.Store
	baseURL string
	dataDir string
	hub     *hub
}

func New(st *store.Store, baseURL, dataDir string) *Server {
	return &Server{st: st, baseURL: baseURL, dataDir: dataDir, hub: newHub()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/me", s.auth(s.handleMe))
	mux.HandleFunc("GET /api/channels", s.auth(s.handleChannels))
	mux.HandleFunc("GET /api/channels/{name}/members", s.auth(s.handleMembers))
	mux.HandleFunc("GET /api/channels/{name}/messages", s.auth(s.handleList))
	mux.HandleFunc("POST /api/channels/{name}/messages", s.auth(s.handleSend))
	mux.HandleFunc("GET /api/channels/{name}/drafts", s.auth(s.handleListDrafts))
	mux.HandleFunc("POST /api/channels/{name}/drafts", s.auth(s.handleCreateDraft))
	mux.HandleFunc("GET /api/messages/{id}", s.auth(s.handleGet))
	mux.HandleFunc("POST /api/messages/{id}/read", s.auth(s.handleRead))
	mux.HandleFunc("POST /api/drafts/{id}/send", s.auth(s.handleSendDraft))
	mux.HandleFunc("DELETE /api/drafts/{id}", s.auth(s.handleDeleteDraft))
	mux.HandleFunc("GET /api/events", s.auth(s.handleEvents))
	mux.HandleFunc("POST /api/invites", s.auth(s.handleCreateInvite))
	mux.HandleFunc("GET /api/join/{code}", s.handleJoinInfo)
	mux.HandleFunc("POST /api/join", s.handleJoin)
	mux.HandleFunc("GET /download/{file}", s.handleDownload)

	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	staticFiles := http.FileServerFS(sub)
	mux.HandleFunc("GET /join/{code}", func(w http.ResponseWriter, r *http.Request) {
		data, err := webFS.ReadFile("web/join.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.Handle("GET /", staticFiles)

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
