package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

var adminChannelNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

func (s *Server) handleAdminChannels(w http.ResponseWriter, _ *http.Request, _ principal) {
	stats, err := s.st.AllChannels()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	out := make([]api.ChannelStat, 0, len(stats))
	for _, st := range stats {
		out = append(out, api.ChannelStat{Name: st.Name, Members: st.Members})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminCreateChannel(w http.ResponseWriter, r *http.Request, _ principal) {
	var req api.AdminChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if !adminChannelNameRe.MatchString(req.Name) {
		writeErr(w, http.StatusBadRequest, "频道名需为 2-32 位小写字母/数字/横线，且以字母开头")
		return
	}
	if _, err := s.st.ChannelByName(req.Name); err == nil {
		writeErr(w, http.StatusBadRequest, "频道 %q 已存在", req.Name)
		return
	}
	ch, err := s.st.CreateChannel(req.Name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, api.ChannelStat{Name: ch.Name, Members: 0})
}

// adminChannel 解析路径频道（管理员无需是成员）。
func (s *Server) adminChannel(w http.ResponseWriter, r *http.Request) (*store.Channel, bool) {
	ch, err := s.st.ChannelByName(r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "频道不存在")
		return nil, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return nil, false
	}
	return ch, true
}

func (s *Server) handleAdminMembers(w http.ResponseWriter, r *http.Request, _ principal) {
	ch, ok := s.adminChannel(w, r)
	if !ok {
		return
	}
	ms, err := s.st.ListMembers(ch.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	out := make([]api.Member, 0, len(ms))
	for _, m := range ms {
		out = append(out, api.Member{Username: m.Username, DisplayName: m.DisplayName, Avatar: m.Avatar})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAdminAddMember(w http.ResponseWriter, r *http.Request, _ principal) {
	ch, ok := s.adminChannel(w, r)
	if !ok {
		return
	}
	var req api.AdminMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	u, err := s.st.UserByName(req.Username)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "用户 %q 不存在（对方需先注册）", req.Username)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if err := s.st.AddMember(ch.ID, u.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminRemoveMember(w http.ResponseWriter, r *http.Request, _ principal) {
	ch, ok := s.adminChannel(w, r)
	if !ok {
		return
	}
	u, err := s.st.UserByName(r.PathValue("username"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if err := s.st.RemoveMember(ch.ID, u.ID); errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "该用户不是频道成员")
		return
	} else if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminInvite(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.adminChannel(w, r)
	if !ok {
		return
	}
	code, err := s.st.CreateInvite(ch.ID, p.user.ID, 7*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.baseURL + "/join/" + code})
}
