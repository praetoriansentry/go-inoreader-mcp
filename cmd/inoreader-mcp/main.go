// Command inoreader-mcp is an MCP server for the Inoreader API.
//
// Usage:
//
//	inoreader-mcp serve [--http ADDR] [--read-only]
//	inoreader-mcp auth login [--manual] [--scope "read write"]
//	inoreader-mcp auth status
//	inoreader-mcp auth refresh
//	inoreader-mcp version
package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/praetoriansentry/go-inoreader-mcp/internal/config"
	"github.com/praetoriansentry/go-inoreader-mcp/internal/inoreader"
	"github.com/praetoriansentry/go-inoreader-mcp/internal/mcpserver"
)

// version is set with -ldflags "-X main.version=..." at build time.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func resolvedVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func usage() {
	fmt.Fprint(os.Stderr, `inoreader-mcp - MCP server for Inoreader

Commands:
  serve          Run the MCP server (stdio by default)
  auth login     Authorize with Inoreader via OAuth 2.0 and save tokens
  auth status    Show token state without calling the API
  auth refresh   Force a token refresh
  version        Print the version

Configuration is read from the environment; see .env.example.
`)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a command is required")
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "auth":
		if len(args) < 2 {
			return errors.New("auth requires a subcommand: login, status, refresh")
		}
		switch args[1] {
		case "login":
			return cmdAuthLogin(args[2:])
		case "status":
			return cmdAuthStatus(args[2:])
		case "refresh":
			return cmdAuthRefresh(args[2:])
		}
		return fmt.Errorf("unknown auth subcommand %q", args[1])
	case "version", "--version", "-v":
		fmt.Println("inoreader-mcp", resolvedVersion())
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	}
	usage()
	return fmt.Errorf("unknown command %q", args[0])
}

func newLogger(level slog.Level) *slog.Logger {
	// Logs go to stderr: stdout is reserved for the stdio MCP transport.
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(resolvedVersion())
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func oauthConfig(cfg *config.Config) *inoreader.OAuthConfig {
	oc := &inoreader.OAuthConfig{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURI:  cfg.RedirectURI,
		UserAgent:    cfg.UserAgent,
	}
	if cfg.BaseURL != "" {
		oc.AuthURL = cfg.BaseURL + "/oauth2/auth"
		oc.TokenURL = cfg.BaseURL + "/oauth2/token"
	}
	return oc
}

func tokenSource(cfg *config.Config) (*inoreader.TokenSource, error) {
	var seed *inoreader.Token
	if cfg.RefreshToken != "" {
		seed = &inoreader.Token{RefreshToken: cfg.RefreshToken}
	}
	return inoreader.NewTokenSource(oauthConfig(cfg), inoreader.FileTokenStore{Path: cfg.TokenFile}, seed)
}

func newClient(cfg *config.Config, logger *slog.Logger) (*inoreader.Client, error) {
	ts, err := tokenSource(cfg)
	if err != nil {
		return nil, err
	}
	opts := []inoreader.Option{inoreader.WithUserAgent(cfg.UserAgent), inoreader.WithLogger(logger)}
	if cfg.BaseURL != "" {
		opts = append(opts, inoreader.WithBaseURL(cfg.BaseURL))
	}
	return inoreader.NewClient(ts, opts...), nil
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// --- serve ---

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	httpAddr := fs.String("http", "", "listen address for the streamable HTTP transport (default: stdio). Overrides INOREADER_MCP_HTTP_ADDR")
	readOnly := fs.Bool("read-only", false, "disable all tools that modify the account (also INOREADER_MCP_READ_ONLY=true)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *httpAddr != "" {
		cfg.HTTPAddr = *httpAddr
	}
	if *readOnly {
		cfg.ReadOnly = true
	}
	if err := cfg.ValidateHTTP(); err != nil {
		return err
	}
	logger := newLogger(cfg.LogLevel)
	client, err := newClient(cfg, logger)
	if err != nil {
		return err
	}
	if client.Tokens().Current() == nil {
		logger.Warn("no saved token; run `inoreader-mcp auth login` first. Tools will fail until then.", "token_file", cfg.TokenFile)
	}
	if w := cfg.TokenFileWarning(); w != "" {
		logger.Warn(w)
	}
	server := mcpserver.New(client, mcpserver.Options{ReadOnly: cfg.ReadOnly, Version: resolvedVersion(), Logger: logger})

	ctx, stop := signalContext()
	defer stop()

	if cfg.HTTPAddr == "" {
		logger.Info("starting MCP server on stdio", "version", resolvedVersion(), "read_only", cfg.ReadOnly)
		return server.Run(ctx, &mcp.StdioTransport{})
	}
	return serveHTTP(ctx, cfg, server, logger)
}

func serveHTTP(ctx context.Context, cfg *config.Config, server *mcp.Server, logger *slog.Logger) error {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Logger:         logger,
		SessionTimeout: 30 * time.Minute,
		// The SDK rejects loopback-sourced requests whose Host header is not
		// localhost, to defeat DNS rebinding from a browser. A reverse proxy on
		// the same host forwards the public Host header and would be blocked.
		// Browsers cannot attach an Authorization header cross-origin, so the
		// bearer token already closes the rebinding hole; keep the SDK check
		// only for tokenless (loopback-only) listeners.
		DisableLocalhostProtection: cfg.HTTPToken != "",
	})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/mcp", bearerAuth(cfg.HTTPToken, handler))
	mux.Handle("/mcp/", bearerAuth(cfg.HTTPToken, handler))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting MCP server over HTTP", "addr", cfg.HTTPAddr, "path", "/mcp",
			"auth", cfg.HTTPToken != "", "read_only", cfg.ReadOnly, "version", resolvedVersion())
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// bearerAuth requires "Authorization: Bearer <token>" when token is set.
func bearerAuth(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if len(h) <= len(prefix) || h[:len(prefix)] != prefix ||
			subtle.ConstantTimeCompare([]byte(h[len(prefix):]), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="inoreader-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
