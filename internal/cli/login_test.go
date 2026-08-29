package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hou-physics/relais/internal/api"
)

func TestRunLoginDocumentedOrder(t *testing.T) {
	t.Setenv("RELAIS_CONFIG_DIR", t.TempDir())

	// Create a stub server that responds to /api/me
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/me" && r.Method == "GET" {
			auth := r.Header.Get("Authorization")
			if auth != "Bearer tok123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(api.Me{
				Username:    "hou",
				DisplayName: "Hou",
				Key:         "agent",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	// Test successful login with documented order: server --token token
	err := RunLogin([]string{ts.URL, "--token", "tok123"})
	if err != nil {
		t.Fatalf("RunLogin failed: %v", err)
	}

	// Verify global config was saved correctly
	cfg, err := loadGlobal()
	if err != nil {
		t.Fatalf("loadGlobal failed: %v", err)
	}
	if cfg.Server != ts.URL || cfg.Token != "tok123" || cfg.Username != "hou" {
		t.Fatalf("config mismatch: got %+v, want Server=%s Token=tok123 Username=hou", cfg, ts.URL)
	}

	// Test missing --token error
	err = RunLogin([]string{ts.URL})
	if err == nil || !strings.Contains(err.Error(), "用法: relais login") {
		t.Fatalf("expected usage error without --token, got %v", err)
	}
}
