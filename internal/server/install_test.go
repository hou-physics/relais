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
		if resp.StatusCode != 200 || !strings.Contains(string(body), tc.must) {
			t.Fatalf("%s 应可下载且含 %q: %d", tc.path, tc.must, resp.StatusCode)
		}
	}
}
