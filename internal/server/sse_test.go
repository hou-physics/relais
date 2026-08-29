package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hou-physics/relais/internal/api"
)

func TestSSEDeliversNewMessage(t *testing.T) {
	ts, _, users := newTestServer(t)
	c := loginCookie(t, ts, "wu", "pw-wu")
	req, _ := http.NewRequest("GET", ts.URL+"/api/events?channel=deutschapp", nil)
	req.AddCookie(c)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type 应为 event-stream, got %s", ct)
	}
	got := make(chan api.Message, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				var m api.Message
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m) == nil {
					got <- m
					return
				}
			}
		}
	}()
	time.Sleep(100 * time.Millisecond) // 等订阅建立
	agentSend(t, ts, users["hou"].AgentToken, "deutschapp",
		api.SendRequest{To: []string{"wu"}, Summary: "实时来了", Body: "b"})
	select {
	case m := <-got:
		if m.Summary != "实时来了" || m.From != "hou" {
			t.Fatalf("收到的事件不对: %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("3 秒内未收到 SSE 事件")
	}
}

func TestSSEAgentKeyRejected(t *testing.T) {
	ts, _, users := newTestServer(t)
	r := agentGet(t, ts, users["hou"].AgentToken, "/api/events?channel=deutschapp")
	if r.StatusCode != 403 {
		t.Fatalf("agent 钥匙订阅 SSE 应 403, got %d", r.StatusCode)
	}
}
