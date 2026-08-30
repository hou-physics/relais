package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

type Client struct {
	Server  string
	Token   string
	Session string // 人的钥匙（session cookie）；若设置则优先使用而不用 Token
	hc      *http.Client
}

func newClient() (*Client, *GlobalConfig, error) {
	cfg, err := loadGlobal()
	if err != nil {
		return nil, nil, err
	}
	return &Client{Server: strings.TrimRight(cfg.Server, "/"), Token: cfg.Token,
		hc: &http.Client{Timeout: 30 * time.Second}}, cfg, nil
}

// newHumanClient 创建使用人的钥匙（session）的客户端。仅供需要人身份的操作使用。
// 在测试中使用 SaveSessionForTest 注入 session token。
func newHumanClient() (*Client, *GlobalConfig, error) {
	cfg, err := loadGlobal()
	if err != nil {
		return nil, nil, err
	}
	session := GetTestSession()
	return &Client{Server: strings.TrimRight(cfg.Server, "/"), Token: cfg.Token, Session: session,
		hc: &http.Client{Timeout: 30 * time.Second}}, cfg, nil
}

func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Server+path, body)
	if err != nil {
		return err
	}
	if c.Session != "" {
		// 人的钥匙（session cookie）
		req.AddCookie(&http.Cookie{Name: "relais_session", Value: c.Session})
	} else {
		// agent token（Bearer）
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接服务器 %s: %w", c.Server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e api.ErrorResponse
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("服务器返回 %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) Me() (*api.Me, error) {
	var me api.Me
	err := c.do("GET", "/api/me", nil, &me)
	return &me, err
}

func (c *Client) Members(channel string) ([]api.Member, error) {
	var out []api.Member
	err := c.do("GET", "/api/channels/"+url.PathEscape(channel)+"/members", nil, &out)
	return out, err
}

func (c *Client) Envelopes(channel string, unreadOnly bool) ([]api.Message, error) {
	path := "/api/channels/" + url.PathEscape(channel) + "/messages"
	if unreadOnly {
		path += "?unread=1"
	}
	var out []api.Message
	err := c.do("GET", path, nil, &out)
	return out, err
}

func (c *Client) Message(id string) (*api.Message, error) {
	var m api.Message
	err := c.do("GET", "/api/messages/"+url.PathEscape(id), nil, &m)
	return &m, err
}

func (c *Client) Send(channel string, req api.SendRequest) (*api.Message, error) {
	var m api.Message
	err := c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/messages", req, &m)
	return &m, err
}

func (c *Client) MarkRead(id string) error {
	return c.do("POST", "/api/messages/"+url.PathEscape(id)+"/read", nil, nil)
}

func (c *Client) CreateDraft(channel string, req api.SendRequest) (*api.Draft, error) {
	var d api.Draft
	err := c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/drafts", req, &d)
	return &d, err
}

func (c *Client) Drafts(channel string) ([]api.Draft, error) {
	var out []api.Draft
	err := c.do("GET", "/api/channels/"+url.PathEscape(channel)+"/drafts", nil, &out)
	return out, err
}

func (c *Client) AutoGet(channel string) (*api.AutoState, error) {
	var st api.AutoState
	err := c.do("GET", "/api/channels/"+url.PathEscape(channel)+"/auto", nil, &st)
	return &st, err
}

func (c *Client) AutoConfig(channel string, enabled bool, cap int) error {
	return c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/auto", api.AutoConfigRequest{Enabled: enabled, Cap: cap}, nil)
}

func (c *Client) AutoPause(channel string) error {
	return c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/auto/pause", nil, nil)
}

func (c *Client) AutoResume(channel string) error {
	return c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/auto/resume", nil, nil)
}

func (c *Client) AutoTurn(channel string) (*api.TurnResponse, error) {
	var tr api.TurnResponse
	err := c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/auto/turn", nil, &tr)
	return &tr, err
}

func (c *Client) NeedsHuman(channel, q string) error {
	return c.do("POST", "/api/channels/"+url.PathEscape(channel)+"/auto/needs-human", api.NeedsHumanRequest{Question: q}, nil)
}

func (c *Client) GuidancePull(channel string) (string, error) {
	var g api.GuidanceResponse
	err := c.do("GET", "/api/channels/"+url.PathEscape(channel)+"/guidance", nil, &g)
	return g.Note, err
}

type AdminClient struct {
	Server  string
	Session string
	hc      *http.Client
}

func (c *AdminClient) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.Server+path, body)
	if err != nil {
		return err
	}
	req.AddCookie(&http.Cookie{Name: "relais_session", Value: c.Session})
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接服务器 %s: %w", c.Server, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e api.ErrorResponse
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return fmt.Errorf("%s", e.Error)
		}
		return fmt.Errorf("服务器返回 %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
