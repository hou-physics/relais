package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

type principal struct {
	user  *store.User
	agent bool // true = agent 钥匙（Bearer token），false = 人的钥匙（session cookie）
}

func (s *Server) auth(h func(http.ResponseWriter, *http.Request, principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); bearer != "" && bearer != r.Header.Get("Authorization") {
			u, err := s.st.UserByAgentToken(bearer)
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "agent token 无效，请重新 relais login")
				return
			}
			h(w, r, principal{user: u, agent: true})
			return
		}
		if c, err := r.Cookie("relais_session"); err == nil {
			u, err := s.st.UserBySession(c.Value)
			if err == nil {
				h(w, r, principal{user: u, agent: false})
				return
			}
		}
		writeErr(w, http.StatusUnauthorized, "未登录")
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req api.LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	u, err := s.st.Authenticate(req.Username, req.Password)
	if errors.Is(err, store.ErrAuth) {
		writeErr(w, http.StatusUnauthorized, "用户名或密码错误")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	token, err := s.st.CreateSession(u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: "relais_session", Value: token, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 60 * 60 * 24 * 90,
	})
	writeJSON(w, http.StatusOK, api.Me{Username: u.Username, DisplayName: u.DisplayName, Avatar: u.Avatar, Key: "human"})
}

func (s *Server) handleMe(w http.ResponseWriter, _ *http.Request, p principal) {
	key := "human"
	if p.agent {
		key = "agent"
	}
	writeJSON(w, http.StatusOK, api.Me{Username: p.user.Username, DisplayName: p.user.DisplayName, Avatar: p.user.Avatar, Key: key})
}
