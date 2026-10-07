// Package config loads server configuration from the environment.
//
// Secrets are only ever read from environment variables or from files named
// by *_FILE variables (for Docker/Kubernetes secrets). Nothing is read from
// the working directory implicitly.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the resolved server configuration.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	TokenFile    string
	// RefreshToken optionally seeds authentication when no token file exists.
	RefreshToken string
	BaseURL      string

	ReadOnly  bool
	HTTPAddr  string
	HTTPToken string
	LogLevel  slog.Level
	UserAgent string
}

const (
	defaultRedirectURI = "http://localhost:8080/callback"
	defaultLogLevel    = slog.LevelInfo
)

// Load reads configuration from the environment. It does not validate the
// presence of credentials; call Validate for that.
func Load(version string) (*Config, error) {
	c := &Config{
		ClientID:     strings.TrimSpace(os.Getenv("INOREADER_CLIENT_ID")),
		RedirectURI:  envDefault("INOREADER_REDIRECT_URI", defaultRedirectURI),
		RefreshToken: strings.TrimSpace(os.Getenv("INOREADER_REFRESH_TOKEN")),
		BaseURL:      strings.TrimSpace(os.Getenv("INOREADER_BASE_URL")),
		HTTPAddr:     strings.TrimSpace(os.Getenv("INOREADER_MCP_HTTP_ADDR")),
		UserAgent:    "go-inoreader-mcp/" + version + " (+https://github.com/praetoriansentry/go-inoreader-mcp)",
	}
	var err error
	if c.ClientSecret, err = envOrFile("INOREADER_CLIENT_SECRET"); err != nil {
		return nil, err
	}
	if c.HTTPToken, err = envOrFile("INOREADER_MCP_HTTP_TOKEN"); err != nil {
		return nil, err
	}
	if c.RefreshToken == "" {
		if c.RefreshToken, err = envOrFile("INOREADER_REFRESH_TOKEN"); err != nil {
			return nil, err
		}
	}
	if c.ReadOnly, err = envBool("INOREADER_MCP_READ_ONLY", false); err != nil {
		return nil, err
	}
	if c.LogLevel, err = parseLevel(envDefault("INOREADER_MCP_LOG_LEVEL", "info")); err != nil {
		return nil, err
	}
	c.TokenFile = strings.TrimSpace(os.Getenv("INOREADER_TOKEN_FILE"))
	if c.TokenFile == "" {
		c.TokenFile, err = defaultTokenFile()
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Validate checks that credentials needed to talk to Inoreader are present.
func (c *Config) Validate() error {
	var missing []string
	if c.ClientID == "" {
		missing = append(missing, "INOREADER_CLIENT_ID")
	}
	if c.ClientSecret == "" {
		missing = append(missing, "INOREADER_CLIENT_SECRET (or INOREADER_CLIENT_SECRET_FILE)")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}

// ValidateHTTP enforces that a non-loopback HTTP listener is protected by a
// bearer token, so the server is never accidentally exposed unauthenticated.
func (c *Config) ValidateHTTP() error {
	if c.HTTPAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return fmt.Errorf("INOREADER_MCP_HTTP_ADDR %q: %w", c.HTTPAddr, err)
	}
	if c.HTTPToken != "" {
		if len(c.HTTPToken) < 16 {
			return errors.New("INOREADER_MCP_HTTP_TOKEN must be at least 16 characters")
		}
		return nil
	}
	if isLoopback(host) {
		return nil
	}
	return fmt.Errorf("refusing to listen on non-loopback address %q without INOREADER_MCP_HTTP_TOKEN", c.HTTPAddr)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}

// envOrFile returns KEY, or the trimmed contents of the file named by
// KEY_FILE when KEY is unset.
func envOrFile(key string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v, nil
	}
	path := strings.TrimSpace(os.Getenv(key + "_FILE"))
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s_FILE: %w", key, err)
	}
	return strings.TrimSpace(string(b)), nil
}

func parseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return defaultLogLevel, fmt.Errorf("INOREADER_MCP_LOG_LEVEL: unknown level %q", s)
}

func defaultTokenFile() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return "", fmt.Errorf("cannot determine config directory; set INOREADER_TOKEN_FILE: %w", err)
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "inoreader-mcp", "tokens.json"), nil
}
