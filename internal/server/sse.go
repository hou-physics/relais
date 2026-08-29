package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/hou-physics/relais/internal/api"
)

type hub struct {
	mu   sync.Mutex
	subs map[int64]map[chan []byte]struct{} // channelID → 订阅者
}

func newHub() *hub { return &hub{subs: map[int64]map[chan []byte]struct{}{}} }

func (h *hub) subscribe(channelID int64) (chan []byte, func()) {
	ch := make(chan []byte, 16)
	h.mu.Lock()
	if h.subs[channelID] == nil {
		h.subs[channelID] = map[chan []byte]struct{}{}
	}
	h.subs[channelID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[channelID], ch)
		h.mu.Unlock()
	}
}

func (h *hub) publish(channelID int64, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs[channelID] {
		select {
		case ch <- payload:
		default: // 慢客户端直接丢帧，网页端全量刷新兜底
		}
	}
}

func (s *Server) publish(channelID int64, m api.Message) {
	payload, err := json.Marshal(m)
	if err != nil {
		return
	}
	s.hub.publish(channelID, payload)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "SSE 仅限网页（人的钥匙）订阅")
		return
	}
	ch, err := s.st.ChannelByName(r.URL.Query().Get("channel"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "频道不存在")
		return
	}
	ok, err := s.st.IsMember(ch.ID, p.user.ID)
	if err != nil || !ok {
		writeErr(w, http.StatusForbidden, "你不是该频道成员")
		return
	}
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		writeErr(w, http.StatusInternalServerError, "服务器不支持流式响应")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	events, cancel := s.hub.subscribe(ch.ID)
	defer cancel()
	for {
		select {
		case payload := <-events:
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
