package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
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
	out := api.AutoState{Enabled: st.Enabled, RoundCount: st.RoundCount, Cap: st.Cap, Paused: st.Paused, NeedsHumanQ: st.NeedsHumanQ,
		Mode: st.Mode, Resolved: st.Resolved, ResolutionMsgID: st.ResolutionMsgID, KickedOff: st.KickedOff, Closed: st.Closed,
		InFlight: st.InFlight, Round: store.Round(st.RoundCount), RoundCap: store.Round(st.Cap)}
	if st.ResolutionMsgID != "" {
		// 按调用方钥匙读：agent 只能看自己是发件人/收件人的结论，否则（ErrForbidden）摘要与承接方留空，
		// 防止三人以上频道里第三方 agent 从信封层串台。
		if m, err := s.st.GetMessage(st.ResolutionMsgID, p.user.ID, p.agent); err == nil {
			out.ResolutionSummary, out.Owner = m.Summary, m.Owner
		}
	}
	writeJSON(w, http.StatusOK, out)
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

func (s *Server) autoKickoff(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "确认开工仅限人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	k, err := s.st.Kickoff(ch.ID, p.user.ID)
	if errors.Is(err, store.ErrNotResolved) {
		writeErr(w, http.StatusConflict, "频道尚未握手，没有可开工的结论")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	s.publish(ch.ID, toAPI(k, ch.Name, false))
	writeJSON(w, http.StatusOK, toAPI(k, ch.Name, true))
}

func (s *Server) autoReopen(w http.ResponseWriter, r *http.Request, p principal) {
	s.autoHumanMutate(w, r, p, func(chID int64) error { return s.st.Reopen(chID) })
}

func (s *Server) autoMode(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "切换模式仅限人的钥匙")
		return
	}
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.ModeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if err := s.st.SetMode(ch.ID, req.Mode); err != nil {
		writeErr(w, http.StatusBadRequest, "%s", err.Error())
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
