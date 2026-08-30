package server

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInstallScriptsServed(t *testing.T) {
	ts, _, _ := newTestServer(t)
	for _, tc := range []struct{ path, must string }{
		{"/install.sh", "relais"},
		{"/install.ps1", "relais"},
	} {
		resp, err := http.Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		s := string(body)
		if resp.StatusCode != 200 || !strings.Contains(s, tc.must) {
			t.Fatalf("%s 应可下载且含 %q: %d", tc.path, tc.must, resp.StatusCode)
		}
		// 替换必须彻底：不能残留占位符，且要出现真实 baseURL（newTestServer 用 http://relais.test）
		if strings.Contains(s, "__BASE_URL__") {
			t.Fatalf("%s 残留未替换的 __BASE_URL__ 占位符", tc.path)
		}
		if !strings.Contains(s, "http://relais.test") {
			t.Fatalf("%s 未替换出真实 baseURL", tc.path)
		}
	}
}
