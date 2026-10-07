package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadFromEnvAndFiles(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("  s3cret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("INOREADER_CLIENT_ID", "cid")
	t.Setenv("INOREADER_CLIENT_SECRET", "")
	t.Setenv("INOREADER_CLIENT_SECRET_FILE", secret)
	t.Setenv("INOREADER_TOKEN_FILE", filepath.Join(dir, "t.json"))
	t.Setenv("INOREADER_MCP_READ_ONLY", "true")
	t.Setenv("INOREADER_MCP_LOG_LEVEL", "debug")
	c, err := Load("test")
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientSecret != "s3cret" || !c.ReadOnly || c.LogLevel.String() != "DEBUG" || c.RedirectURI != defaultRedirectURI {
		t.Errorf("unexpected config: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Error(err)
	}
	if !strings.Contains(c.UserAgent, "test") {
		t.Error("user agent missing version")
	}
}

func TestTokenFileResolvedAbsolute(t *testing.T) {
	t.Setenv("INOREADER_TOKEN_FILE", "tokens.json")
	c, err := Load("test")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(c.TokenFile) || filepath.Base(c.TokenFile) != "tokens.json" {
		t.Errorf("token file not resolved: %q", c.TokenFile)
	}
	if w := (&Config{TokenFile: "/data/tokens.json"}).TokenFileWarning(); w != "" && !InContainer() {
		t.Errorf("unexpected warning: %s", w)
	}
}

func TestValidateMissing(t *testing.T) {
	t.Setenv("INOREADER_CLIENT_ID", "")
	t.Setenv("INOREADER_CLIENT_SECRET", "")
	t.Setenv("INOREADER_CLIENT_SECRET_FILE", "")
	t.Setenv("INOREADER_TOKEN_FILE", filepath.Join(t.TempDir(), "t.json"))
	c, err := Load("test")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "INOREADER_CLIENT_ID") {
		t.Errorf("expected missing error, got %v", err)
	}
}

func TestBadValues(t *testing.T) {
	t.Setenv("INOREADER_TOKEN_FILE", filepath.Join(t.TempDir(), "t.json"))
	t.Setenv("INOREADER_MCP_READ_ONLY", "maybe")
	if _, err := Load("x"); err == nil {
		t.Error("expected bool parse error")
	}
	t.Setenv("INOREADER_MCP_READ_ONLY", "")
	t.Setenv("INOREADER_MCP_LOG_LEVEL", "loud")
	if _, err := Load("x"); err == nil {
		t.Error("expected level parse error")
	}
	t.Setenv("INOREADER_MCP_LOG_LEVEL", "")
	t.Setenv("INOREADER_CLIENT_SECRET", "")
	t.Setenv("INOREADER_CLIENT_SECRET_FILE", "/nonexistent/secret")
	if _, err := Load("x"); err == nil {
		t.Error("expected missing secret file error")
	}
}

func TestValidateHTTP(t *testing.T) {
	cases := []struct {
		addr, token string
		ok          bool
	}{
		{"", "", true},
		{"127.0.0.1:8765", "", true},
		{"localhost:8765", "", true},
		{"[::1]:8765", "", true},
		{"0.0.0.0:8765", "", false},
		{":8765", "", false},
		{"0.0.0.0:8765", "short", false},
		{"0.0.0.0:8765", "0123456789abcdef0123456789abcdef", true},
		{"nonsense", "", false},
	}
	for _, c := range cases {
		cfg := &Config{HTTPAddr: c.addr, HTTPToken: c.token}
		err := cfg.ValidateHTTP()
		if (err == nil) != c.ok {
			t.Errorf("ValidateHTTP(%q, token=%q) err=%v, want ok=%v", c.addr, c.token, err, c.ok)
		}
	}
}
