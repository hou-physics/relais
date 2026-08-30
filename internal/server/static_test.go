package server

import (
	"net/http"
	"testing"
)

func TestStaticCacheHeaders(t *testing.T) {
	ts, _, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("app.js 应带 ETag")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control 应为 no-cache, got %q", cc)
	}
	// If-None-Match 命中 → 304
	req, _ := http.NewRequest("GET", ts.URL+"/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, _ := http.DefaultClient.Do(req)
	if resp2.StatusCode != 304 {
		t.Fatalf("相同 ETag 应 304, got %d", resp2.StatusCode)
	}
	// join 页也带缓存头
	resp3, _ := http.Get(ts.URL + "/")
	if resp3.Header.Get("ETag") == "" {
		t.Fatal("首页应带 ETag")
	}
}
