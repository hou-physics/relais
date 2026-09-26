package cli

import "testing"

func TestSessionRegistry(t *testing.T) {
	dir := t.TempDir()
	if id, err := sessionGet(dir, "m1"); err != nil || id != "" {
		t.Fatalf("空登记应返回空: %q %v", id, err)
	}
	if err := sessionSet(dir, "m1", "uuid-1"); err != nil {
		t.Fatal(err)
	}
	if err := sessionSet(dir, "m2", "uuid-2"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(dir, "m1"); id != "uuid-1" {
		t.Fatalf("读回错: %q", id)
	}
	if err := sessionSet(dir, "m1", "uuid-1b"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(dir, "m1"); id != "uuid-1b" {
		t.Fatal("应覆盖")
	}
	if err := sessionClear(dir, "m1"); err != nil {
		t.Fatal(err)
	}
	if id, _ := sessionGet(dir, "m1"); id != "" {
		t.Fatal("clear 后应空")
	}
	if id, _ := sessionGet(dir, "m2"); id != "uuid-2" {
		t.Fatal("clear 不应影响其他频道")
	}
	if err := sessionClear(dir, "nope"); err != nil {
		t.Fatalf("clear 不存在的应幂等: %v", err)
	}
}
