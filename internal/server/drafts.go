package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/store"
)

func draftToAPI(d *store.Draft, channelName string) api.Draft {
	return api.Draft{ID: d.ID, Channel: channelName, To: d.To, Summary: d.Summary,
		Body: d.Body, InReplyTo: d.InReplyTo, CreatedAt: d.CreatedAt}
}

// validateOutgoing 复用 send 的校验语义：摘要必填、收件人∈成员且非空。
// 返回收件人 username 列表（供草稿存储）与 id 列表（供转正）。
func (s *Server) validateOutgoing(w http.ResponseWriter, ch *store.Channel, req *api.SendRequest) (names []string, ids []int64, ok bool) {
	if strings.TrimSpace(req.Summary) == "" {
		writeErr(w, http.StatusBadRequest, "摘要（summary）必填：给人看的一两句话")
		return nil, nil, false
	}
	members, err := s.st.ListMembers(ch.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return nil, nil, false
	}
	byName := map[string]int64{}
	var all []string
	for _, m := range members {
		byName[m.Username] = m.ID
		all = append(all, m.Username)
	}
	seen := map[string]bool{}
	for _, name := range req.To {
		if seen[name] {
			continue
		}
		id, exists := byName[name]
		if !exists {
			writeErr(w, http.StatusBadRequest, "收件人 %q 不是频道成员；有效成员：%s", name, strings.Join(all, ", "))
			return nil, nil, false
		}
		seen[name] = true
		names = append(names, name)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, "收件人不能为空；有效成员：%s", strings.Join(all, ", "))
		return nil, nil, false
	}
	return names, ids, true
}

func (s *Server) handleListDrafts(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	ds, err := s.st.ListDrafts(ch.ID, p.user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	out := make([]api.Draft, 0, len(ds))
	for i := range ds {
		out = append(out, draftToAPI(&ds[i], ch.Name))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateDraft(w http.ResponseWriter, r *http.Request, p principal) {
	ch, ok := s.channelForMember(w, r, p)
	if !ok {
		return
	}
	var req api.SendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	names, _, ok := s.validateOutgoing(w, ch, &req)
	if !ok {
		return
	}
	d, err := s.st.CreateDraft(ch.ID, p.user.ID, names, req.Summary, req.Body, req.InReplyTo)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, draftToAPI(d, ch.Name))
}

func (s *Server) handleSendDraft(w http.ResponseWriter, r *http.Request, p principal) {
	d, err := s.st.GetDraft(r.PathValue("id"), p.user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "草稿不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	chName, err := s.st.ChannelNameByID(d.ChannelID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	// 发送时重验：收件人此刻仍须是成员
	ch := &store.Channel{ID: d.ChannelID, Name: chName}
	req := api.SendRequest{To: d.To, Summary: d.Summary, Body: d.Body, InReplyTo: d.InReplyTo}
	_, ids, ok := s.validateOutgoing(w, ch, &req)
	if !ok {
		return
	}
	m, err := s.st.SaveMessage(d.ChannelID, p.user.ID, ids, d.Summary, d.Body, d.InReplyTo)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	if err := s.st.DeleteDraft(d.ID, p.user.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	s.publish(d.ChannelID, toAPI(m, chName, false))
	writeJSON(w, http.StatusOK, toAPI(m, chName, true))
}

func (s *Server) handleDeleteDraft(w http.ResponseWriter, r *http.Request, p principal) {
	err := s.st.DeleteDraft(r.PathValue("id"), p.user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "草稿不存在")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
