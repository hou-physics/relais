// Package api 定义 server 与 cli 共享的 JSON DTO。字段名以此为准，两侧不得另造。
package api

import "time"

type Me struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
	Key         string `json:"key"` // "human" | "agent"
	IsAdmin     bool   `json:"is_admin"`
}

type Member struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

type ChannelInfo struct {
	Name   string `json:"name"`
	Unread int    `json:"unread"`
}

type Message struct {
	ID          string    `json:"id"`
	Channel     string    `json:"channel"`
	From        string    `json:"from"`
	FromDisplay string    `json:"from_display"`
	FromAvatar  string    `json:"from_avatar,omitempty"`
	To          []string  `json:"to"`
	Summary     string    `json:"summary"`
	Body        string    `json:"body_md,omitempty"`
	InReplyTo   string    `json:"in_reply_to,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Unread      bool      `json:"unread"`
}

type SendRequest struct {
	To        []string `json:"to"`
	Summary   string   `json:"summary"`
	Body      string   `json:"body_md"`
	InReplyTo string   `json:"in_reply_to,omitempty"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type JoinInfo struct {
	Channel   string     `json:"channel,omitempty"`
	Server    string     `json:"server"`
	Downloads []Download `json:"downloads"`
}

type Download struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type JoinRequest struct {
	Code        string `json:"code"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type JoinResponse struct {
	Server     string `json:"server"`
	Username   string `json:"username"`
	AgentToken string `json:"agent_token"`
	Channel    string `json:"channel,omitempty"`
	LoginCmd   string `json:"login_cmd"`
	Guide      string `json:"guide"`
}

type Draft struct {
	ID        string    `json:"id"`
	Channel   string    `json:"channel"`
	To        []string  `json:"to"`
	Summary   string    `json:"summary"`
	Body      string    `json:"body_md"`
	InReplyTo string    `json:"in_reply_to,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type PasswordRequest struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type ProfileRequest struct {
	DisplayName string `json:"display_name"`
	Avatar      string `json:"avatar"`
}

type TokenResponse struct {
	AgentToken string `json:"agent_token"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type ChannelStat struct {
	Name    string `json:"name"`
	Members int    `json:"members"`
}

type AdminChannelRequest struct {
	Name string `json:"name"`
}

type AdminMemberRequest struct {
	Username string `json:"username"`
}
