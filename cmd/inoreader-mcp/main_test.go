package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerAuth(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := securityHeaders(bearerAuth("0123456789abcdef0123456789abcdef", ok))
	cases := []struct {
		header string
		want   int
	}{
		{"", http.StatusUnauthorized},
		{"Bearer", http.StatusUnauthorized},
		{"Bearer wrong", http.StatusUnauthorized},
		{"bearer 0123456789abcdef0123456789abcdef", http.StatusUnauthorized},
		{"Bearer 0123456789abcdef0123456789abcdef ", http.StatusUnauthorized},
		{"Bearer 0123456789abcdef0123456789abcdef", http.StatusNoContent},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != c.want {
			t.Errorf("Authorization %q: got %d, want %d", c.header, rr.Code, c.want)
		}
		if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("missing security header")
		}
		if c.want == http.StatusUnauthorized && !strings.Contains(rr.Header().Get("WWW-Authenticate"), "Bearer") {
			t.Errorf("missing WWW-Authenticate")
		}
	}
	// No token configured: pass-through.
	rr := httptest.NewRecorder()
	bearerAuth("", ok).ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rr.Code != http.StatusNoContent {
		t.Errorf("no-token mode should pass through, got %d", rr.Code)
	}
}

func TestRunUsage(t *testing.T) {
	if err := run(nil); err == nil {
		t.Error("no args should error")
	}
	if err := run([]string{"bogus"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("bogus command: %v", err)
	}
	if err := run([]string{"auth"}); err == nil {
		t.Error("auth without subcommand should error")
	}
	if err := run([]string{"auth", "nope"}); err == nil {
		t.Error("unknown auth subcommand should error")
	}
	if err := run([]string{"version"}); err != nil {
		t.Errorf("version: %v", err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Errorf("help: %v", err)
	}
}

func TestServeRequiresCredentials(t *testing.T) {
	t.Setenv("INOREADER_CLIENT_ID", "")
	t.Setenv("INOREADER_CLIENT_SECRET", "")
	t.Setenv("INOREADER_CLIENT_SECRET_FILE", "")
	t.Setenv("INOREADER_TOKEN_FILE", t.TempDir()+"/t.json")
	err := run([]string{"serve"})
	if err == nil || !strings.Contains(err.Error(), "INOREADER_CLIENT_ID") {
		t.Errorf("expected missing credential error, got %v", err)
	}
	t.Setenv("INOREADER_CLIENT_ID", "x")
	t.Setenv("INOREADER_CLIENT_SECRET", "y")
	err = run([]string{"serve", "--http", "0.0.0.0:0"})
	if err == nil || !strings.Contains(err.Error(), "INOREADER_MCP_HTTP_TOKEN") {
		t.Errorf("expected token requirement error, got %v", err)
	}
}
