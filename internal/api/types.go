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
	Seq         int       `json:"seq"`
	Round       int       `json:"round"`
	Kind        string    `json:"kind,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	OwnerReason string    `json:"owner_reason,omitempty"`
	AckOf       string    `json:"ack_of,omitempty"`
}

type SendRequest struct {
	To          []string `json:"to"`
	Summary     string   `json:"summary"`
	Body        string   `json:"body_md"`
	InReplyTo   string   `json:"in_reply_to,omitempty"`
	Kind        string   `json:"kind,omitempty"`
	Owner       string   `json:"owner,omitempty"`
	OwnerReason string   `json:"owner_reason,omitempty"`
	AckOf       string   `json:"ack_of,omitempty"`
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

type AutoState struct {
	Enabled           bool   `json:"enabled"`
	RoundCount        int    `json:"round_count"`
	Cap               int    `json:"cap"`
	Paused            bool   `json:"paused"`
	NeedsHumanQ       string `json:"needs_human_q"`
	Mode              string `json:"mode"`
	Resolved          bool   `json:"resolved"`
	ResolutionMsgID   string `json:"resolution_msg_id,omitempty"`
	ResolutionSummary string `json:"resolution_summary,omitempty"`
	Owner             string `json:"owner,omitempty"`
	KickedOff         bool   `json:"kicked_off"`
	Closed            bool   `json:"closed"`
	InFlight          bool   `json:"in_flight"`
	Round             int    `json:"round"`
	RoundCap          int    `json:"round_cap"`
}

type AutoConfigRequest struct {
	Enabled bool `json:"enabled"`
	Cap     int  `json:"cap"`
}

type ModeRequest struct {
	Mode string `json:"mode"`
}

type GuidanceRequest struct {
	Note string `json:"note"`
}

type NeedsHumanRequest struct {
	Question string `json:"question"`
}

type TurnResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
}

type GuidanceResponse struct {
	Note string `json:"note"`
}

// 本地控制台（M8）DTO。

type LocalRepo struct {
	Dir        string    `json:"dir"`
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
}

// LocalSide：模块里某一侧（claude/codex）的接入与投递状态（M9）。
type LocalSide struct {
	Waiting       bool      `json:"waiting"` // claude
	WaitSince     time.Time `json:"wait_since,omitempty"`
	SessionName   string    `json:"session_name,omitempty"`
	Cursor        int       `json:"cursor"`   // claude：wait 读到的最后一封的 seq
	Working       bool      `json:"working"`  // claude：没在 wait，但已读到最后一封给它的信（wait 收信即退出，正在回）
	Attached      bool      `json:"attached"` // codex
	ThreadName    string    `json:"thread_name,omitempty"`
	AttachedAt    time.Time `json:"attached_at,omitempty"`
	LastDelivery  string    `json:"last_delivery,omitempty"` // ok|error|""
	DeliveryError string    `json:"delivery_error,omitempty"`
	DeliveryAt    time.Time `json:"delivery_at,omitempty"`
}

type LocalModule struct {
	Name              string           `json:"name"`
	Dir               string           `json:"dir"`
	Mode              string           `json:"mode"`
	Round             int              `json:"round"`
	RoundCap          int              `json:"round_cap"`
	State             string           `json:"state"` // 未接入|讨论中|等你|已握手|已开工|已关闭
	LastSeq           int              `json:"last_seq"`
	LastFrom          string           `json:"last_from,omitempty"`
	LastAt            time.Time        `json:"last_at,omitempty"`
	WaitingFor        string           `json:"waiting_for,omitempty"` // claude|codex|user|""
	Claude            LocalSide        `json:"claude"`
	Codex             LocalSide        `json:"codex"`
	NeedsHumanQ       string           `json:"needs_human_q,omitempty"`
	PendingConclusion *LocalConclusion `json:"pending_conclusion,omitempty"`
	Rejected          []string         `json:"rejected,omitempty"` // outbox 里 .rejected 文件名
	Closed            bool             `json:"closed"`
	MailboxMissing    bool             `json:"mailbox_missing"` // 守卫发现 relais/mail/<模块>/ 不见了（项目被移动或删除？）
}

type LocalConclusion struct {
	Seq             int    `json:"seq"`
	Owner           string `json:"owner"`
	Summary         string `json:"summary"`
	AwaitingConfirm bool   `json:"awaiting_confirm"`
	Path            string `json:"path"`
}

type LocalModuleRequest struct {
	Name        string `json:"name"`
	Dir         string `json:"dir"`
	CodexThread string `json:"codex_thread,omitempty"`
}

type LocalModulePatch struct {
	Name     string `json:"name,omitempty"`
	Mode     string `json:"mode,omitempty"`
	RoundCap int    `json:"round_cap,omitempty"`
}

type LocalSettings struct {
	CodexPath         string `json:"codex_path"`
	CodexOK           bool   `json:"codex_ok"`
	DefaultMode       string `json:"default_mode"`
	DefaultCap        int    `json:"default_cap"`
	NotifyEveryLetter bool   `json:"notify_every_letter"`
}

type LocalConversation struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Title     string    `json:"title,omitempty"`
	Cwd       string    `json:"cwd"`
	Status    string    `json:"status,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type LocalAttachRequest struct {
	Side   string `json:"side"`
	Thread string `json:"thread"`
}

type LocalState struct {
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	CodexOK   bool      `json:"codex_ok"`
}
