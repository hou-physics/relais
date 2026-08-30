package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

// humanOnly 包装：设置类操作只允许人的钥匙。
func (s *Server) humanOnly(h func(http.ResponseWriter, *http.Request, principal)) func(http.ResponseWriter, *http.Request, principal) {
	return func(w http.ResponseWriter, r *http.Request, p principal) {
		if p.agent {
			writeErr(w, http.StatusForbidden, "该操作仅限网页（人的钥匙）")
			return
		}
		h(w, r, p)
	}
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request, p principal) {
	var req api.PasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if len(req.New) < 8 {
		writeErr(w, http.StatusBadRequest, "新密码至少 8 位")
		return
	}
	err := s.st.UpdatePassword(p.user.ID, req.Old, req.New)
	if errors.Is(err, store.ErrAuth) {
		writeErr(w, http.StatusUnauthorized, "旧密码不正确")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request, p principal) {
	var req api.ProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if strings.TrimSpace(req.DisplayName) == "" {
		writeErr(w, http.StatusBadRequest, "显示名不能为空")
		return
	}
	if len([]rune(req.DisplayName)) > 64 {
		writeErr(w, http.StatusBadRequest, "显示名过长")
		return
	}
	if len([]rune(req.Avatar)) > 16 {
		writeErr(w, http.StatusBadRequest, "头像过长")
		return
	}
	if err := s.st.UpdateProfile(p.user.ID, req.DisplayName, req.Avatar); err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, api.Me{Username: p.user.Username, DisplayName: req.DisplayName,
		Avatar: req.Avatar, Key: "human"})
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request, p principal) {
	token, err := s.st.RegenerateToken(p.user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, api.TokenResponse{AgentToken: token})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, p principal) {
	if c, err := r.Cookie("relais_session"); err == nil {
		if err := s.st.DeleteSession(c.Value); err != nil {
			writeErr(w, http.StatusInternalServerError, "服务器内部错误")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: "relais_session", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}
