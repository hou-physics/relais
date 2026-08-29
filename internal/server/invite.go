package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/hou-physics/relais/internal/api"
	"github.com/hou-physics/relais/internal/guide"
	"github.com/hou-physics/relais/internal/store"
)

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request, p principal) {
	if p.agent {
		writeErr(w, http.StatusForbidden, "邀请仅限网页（人的钥匙）创建")
		return
	}
	chName := r.URL.Query().Get("channel")
	var chID int64
	if chName != "" {
		ch, err := s.st.ChannelByName(chName)
		if err != nil {
			writeErr(w, http.StatusNotFound, "频道不存在")
			return
		}
		ok, _ := s.st.IsMember(ch.ID, p.user.ID)
		if !ok {
			writeErr(w, http.StatusForbidden, "你不是该频道成员")
			return
		}
		chID = ch.ID
	}
	code, err := s.st.CreateInvite(chID, p.user.ID, 7*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": s.baseURL + "/join/" + code})
}

func (s *Server) listDownloads() []api.Download {
	labels := map[string]string{
		"relais-darwin-arm64":      "macOS (Apple Silicon)",
		"relais-darwin-amd64":      "macOS (Intel)",
		"relais-windows-amd64.exe": "Windows",
		"relais-linux-amd64":       "Linux",
	}
	var out []api.Download
	entries, err := os.ReadDir(filepath.Join(s.dataDir, "downloads"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		label := labels[e.Name()]
		if label == "" {
			label = e.Name()
		}
		out = append(out, api.Download{Label: label, URL: s.baseURL + "/download/" + e.Name()})
	}
	return out
}

func (s *Server) handleJoinInfo(w http.ResponseWriter, r *http.Request) {
	chName, err := s.st.InviteChannel(r.PathValue("code"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "邀请链接无效或已过期，请让邀请人重新生成")
		return
	}
	writeJSON(w, http.StatusOK, api.JoinInfo{Channel: chName, Server: s.baseURL, Downloads: s.listDownloads()})
}

var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,23}$`)

func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	var req api.JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求格式不对")
		return
	}
	if !usernameRe.MatchString(req.Username) {
		writeErr(w, http.StatusBadRequest, "用户名需为 2-24 位小写字母/数字/横线，且以字母开头")
		return
	}
	if len(req.Password) < 8 {
		writeErr(w, http.StatusBadRequest, "密码至少 8 位")
		return
	}
	// Pre-check username availability to prevent invite burning via duplicate username DoS
	if _, err := s.st.UserByName(req.Username); err == nil {
		writeErr(w, http.StatusBadRequest, "用户名 %q 已被占用，请换一个", req.Username)
		return
	}
	chID, err := s.st.ConsumeInvite(req.Code)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "邀请链接无效、已过期或已被使用")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "服务器内部错误")
		return
	}
	display := req.DisplayName
	if display == "" {
		display = req.Username
	}
	u, err := s.st.CreateUser(req.Username, display, req.Password)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "用户名 %q 已被占用，请换一个", req.Username)
		return
	}
	chName := ""
	if chID != 0 {
		if err := s.st.AddMember(chID, u.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, "服务器内部错误")
			return
		}
		chName, _ = s.st.ChannelNameByID(chID)
	}
	writeJSON(w, http.StatusOK, api.JoinResponse{
		Server: s.baseURL, Username: u.Username, AgentToken: u.AgentToken, Channel: chName,
		LoginCmd: fmt.Sprintf("relais login %s --token %s", s.baseURL, u.AgentToken),
		Guide:    guide.Text(u.Username, chName),
	})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("file")) // 防路径穿越
	http.ServeFile(w, r, filepath.Join(s.dataDir, "downloads", name))
}
