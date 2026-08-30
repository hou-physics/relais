package server

import (
	"encoding/json"
	"net/http"

	"github.com/hou-physics/relais/internal/api"
)

func (s *Server) autoState(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	st, err := s.st.GetAuto(ch.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, api.AutoState{Enabled: st.Enabled, RoundCount: st.RoundCount,
		Cap: st.Cap, Paused: st.Paused, NeedsHumanQ: st.NeedsHumanQ})
}

func (s *Server) autoConfig(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "开关自动模式仅限网页/命令行的人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.AutoConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if err := s.st.SetAutoEnabled(ch.ID, req.Enabled, req.Cap); err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) autoPause(w http.ResponseWriter, r *http.Request, p principal) {
	s.autoHumanMutate(w, r, p, func(chID int64) error { return s.st.PauseAuto(chID) })
}

func (s *Server) autoResume(w http.ResponseWriter, r *http.Request, p principal) {
	s.autoHumanMutate(w, r, p, func(chID int64) error { return s.st.ResumeAuto(chID) })
}

func (s *Server) autoHumanMutate(w http.ResponseWriter, r *http.Request, p principal, fn func(int64) error) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "该操作仅限人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	if err := fn(ch.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) autoTurn(w http.ResponseWriter, r *http.Request, p principal) {
	if !p.agent {
		writeErr(w, http.StatusForbidden, "请求发言权仅限 agent 钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	allowed, reason, err := s.st.RequestTurn(ch.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, api.TurnResponse{Allowed: allowed, Reason: reason})
}

func (s *Server) autoNeedsHuman(w http.ResponseWriter, r *http.Request, p principal) {
	if !p.agent {
		writeErr(w, http.StatusForbidden, "标记需要人仅限 agent 钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.NeedsHumanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if err := s.st.SetNeedsHuman(ch.ID, req.Question); err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) guidancePost(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "写引导仅限人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.GuidanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if err := s.st.SetGuidance(ch.ID, p.user.ID, req.Note); err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) guidancePull(w http.ResponseWriter, r *http.Request, p principal) {
	if !p.agent {
		writeErr(w, http.StatusForbidden, "取引导仅限 agent 钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	note, err := s.st.PullGuidance(ch.ID, p.user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, api.GuidanceResponse{Note: note})
}
