package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStaticPages(t *testing.T) {
	ts, st, users := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Relais") {
		t.Fatalf("首页应含 Relais: %d", resp.StatusCode)
	}
	ch, _ := st.ChannelByName("deutschapp")
	code, _ := st.CreateInvite(ch.ID, users["hou"].ID, time.Hour)
	resp, _ = http.Get(ts.URL + "/join/" + code)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "邀请") {
		t.Fatalf("join 页应渲染向导: %d", resp.StatusCode)
	}
	resp, _ = http.Get(ts.URL + "/vendor/marked.min.js")
	if resp.StatusCode != 200 {
		t.Fatalf("vendor 资源应可达: %d", resp.StatusCode)
	}
}
