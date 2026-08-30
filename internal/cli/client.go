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
	Server string
	Token  string
	hc     *http.Client
}

func newClient() (*Client, *GlobalConfig, error) {
	cfg, err := loadGlobal()
	if err != nil {
		return nil, nil, err
	}
	return &Client{Server: strings.TrimRight(cfg.Server, "/"), Token: cfg.Token,
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
	req.Header.Set("Authorization", "Bearer "+c.Token)
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
