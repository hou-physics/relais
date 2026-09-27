package local

import "testing"

func TestDetectSide(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		want string
		err  bool
	}{
		{"claude", map[string]string{"CLAUDECODE": "1"}, "claude", false},
		{"codex home", map[string]string{"CODEX_HOME": "/x"}, "codex", false},
		{"codex sandbox", map[string]string{"CODEX_SANDBOX_NETWORK_DISABLED": "1"}, "codex", false},
		{"none", map[string]string{}, "", true},
		{"both", map[string]string{"CLAUDECODE": "1", "CODEX_HOME": "/x"}, "", true},
	}
	for _, c := range cases {
		got, err := DetectSide(env(c.env))
		if (err != nil) != c.err || got != c.want {
			t.Fatalf("%s: got %q err=%v", c.name, got, err)
		}
	}
}
