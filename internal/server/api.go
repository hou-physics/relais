package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

func toAPI(m *store.Message, channelName string, withBody bool) api.Message {
	out := api.Message{
		ID: m.ID, Channel: channelName, From: m.Sender, FromDisplay: m.SenderDisplay, FromAvatar: m.SenderAvatar,
		To: m.To, Summary: m.Summary, InReplyTo: m.InReplyTo, CreatedAt: m.CreatedAt, Unread: m.Unread,
	}
	if withBody {
		out.Body = m.Body
	}
	return out
}

// channelForMember 解析路径里的频道并要求 p 是成员。
func (s *Server) channelForMember(w http.ResponseWriter, r *http.Request, p principal) (*store.Channel, bool) {
	ch, err := s.st.ChannelByName(r.PathValue("name"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "频道不存在")
		return nil, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return nil, false
	}
	ok, err := s.st.IsMember(ch.ID, p.user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return nil, false
	}
	if !ok {
		writeErr(w, http.StatusForbidden, "你不是频道 %q 的成员", ch.Name)
		return nil, false
	}
	return ch, true
}

func (s *Server) handleChannels(w http.ResponseWriter, _ *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "频道列表仅限网页（人的钥匙）访问")
		return
	}
	infos, err := s.st.ChannelsForUser(p.user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	out := make([]api.ChannelInfo, 0, len(infos))
	for _, ci := range infos {
		out = append(out, api.ChannelInfo{Name: ci.Name, Unread: ci.Unread})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
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

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	unreadOnly := r.URL.Query().Get("unread") == "1"
	msgs, err := s.st.ListEnvelopes(ch.ID, p.user.ID, p.agent, unreadOnly)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	out := make([]api.Message, 0, len(msgs))
	for i := range msgs {
		out = append(out, toAPI(&msgs[i], ch.Name, false))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	_, toIDs, ok := s.validateOutgoing(w, ch, &req)
	if !ok {
		return
	}
	m, err := s.st.SaveMessage(ch.ID, p.user.ID, toIDs, req.Summary, req.Body, req.InReplyTo)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	s.publish(ch.ID, toAPI(m, ch.Name, false)) // Task 9 实现；本任务先加空方法占位
	writeJSON(w, http.StatusOK, toAPI(m, ch.Name, true))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, p principal) {
	m, err := s.st.GetMessage(r.PathValue("id"), p.user.ID, p.agent)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "消息不存在")
		return
	}
	if errors.Is(err, store.ErrForbidden) {
		writeErr(w, http.StatusForbidden, "这条消息不是发给你的（设计行为：agent 只能读自己主人相关的消息）")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	ch := ""
	if c, err := s.st.ChannelNameByID(m.ChannelID); err == nil {
		ch = c
	}
	writeJSON(w, http.StatusOK, toAPI(m, ch, true))
}

func (s *Server) handleRead(w http.ResponseWriter, r *http.Request, p principal) {
	err := s.st.MarkRead(r.PathValue("id"), p.user.ID)
	if errors.Is(err, store.ErrForbidden) {
		writeErr(w, http.StatusForbidden, "你不是这条消息的收件人")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
