package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/praetoriansentry/go-inoreader-mcp/internal/config"
	"github.com/praetoriansentry/go-inoreader-mcp/internal/inoreader"
)

func cmdAuthLogin(args []string) error {
	fs := flag.NewFlagSet("auth login", flag.ContinueOnError)
	manual := fs.Bool("manual", false, "do not start a local callback listener; paste the redirect URL instead (use inside containers or over SSH)")
	scope := fs.String("scope", inoreader.ScopeReadWrite, `OAuth scope: "read" or "read write"`)
	timeout := fs.Duration("timeout", 5*time.Minute, "how long to wait for the browser callback")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	// Fail before the browser round trip if the token cannot be saved.
	if err := (inoreader.FileTokenStore{Path: cfg.TokenFile}).CheckWritable(); err != nil {
		if config.InContainer() {
			return fmt.Errorf("%w\nInside Docker this usually means the /data volume is owned by root. Fix it with:\n  docker run --rm -v <volume>:/data alpine chown 65532:65532 /data\nor recreate the volume with an image built from this version or later", err)
		}
		return err
	}
	if w := cfg.TokenFileWarning(); w != "" {
		fmt.Fprintln(os.Stderr, "WARNING:", w)
	}
	oc := oauthConfig(cfg)
	state, err := inoreader.NewState()
	if err != nil {
		return err
	}
	authURL := oc.AuthCodeURL(state, *scope)

	fmt.Fprintln(os.Stderr, "Open this URL in your browser and authorize the app:")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  "+authURL)
	fmt.Fprintln(os.Stderr)

	ctx, stop := signalContext()
	defer stop()

	var code string
	if *manual {
		code, err = readCodeManually(state)
	} else {
		code, err = waitForCallback(ctx, cfg.RedirectURI, state, *timeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "callback listener failed:", err)
			fmt.Fprintln(os.Stderr, "Falling back to manual mode.")
			code, err = readCodeManually(state)
		}
	}
	if err != nil {
		return err
	}

	tok, err := oc.Exchange(ctx, code)
	if err != nil {
		return err
	}
	ts, err := tokenSource(cfg)
	if err != nil {
		return err
	}
	if err := ts.Set(tok); err != nil {
		return fmt.Errorf("save token: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Authorized. Scope %q, access token valid until %s.\nTokens saved to %s\n",
		tok.Scope, time.Unix(tok.ExpiresAt, 0).Format(time.RFC1123), cfg.TokenFile)
	if w := cfg.TokenFileWarning(); w != "" {
		fmt.Fprintln(os.Stderr, "WARNING:", w)
	}
	return nil
}

// waitForCallback serves the redirect URI locally and returns the auth code.
func waitForCallback(ctx context.Context, redirectURI, state string, timeout time.Duration) (string, error) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return "", fmt.Errorf("parse redirect uri: %w", err)
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("redirect uri %q is not http; use --manual", redirectURI)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return "", fmt.Errorf("redirect uri host %q is not local; use --manual", host)
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	// Listen on all interfaces of the chosen port so the flow also works when
	// the port is published from a container; the browser still targets localhost.
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", ":"+port)
	if err != nil {
		return "", err
	}
	defer func() { _ = ln.Close() }()

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)
	path := u.Path
	if path == "" {
		path = "/"
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			http.Error(w, "Authorization failed: "+e, http.StatusBadRequest)
			resCh <- result{err: fmt.Errorf("authorization failed: %s", e)}
			return
		}
		if q.Get("state") != state {
			http.Error(w, "State mismatch; please retry the login.", http.StatusBadRequest)
			resCh <- result{err: errors.New("state mismatch (possible CSRF); retry the login")}
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "Missing code.", http.StatusBadRequest)
			resCh <- result{err: errors.New("callback had no code")}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><title>inoreader-mcp</title><p>Authorized. You can close this tab.</p>"))
		resCh <- result{code: code}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	fmt.Fprintf(os.Stderr, "Waiting for the browser to redirect to %s ...\n", redirectURI)
	select {
	case r := <-resCh:
		return r.code, r.err
	case <-time.After(timeout):
		return "", errors.New("timed out waiting for the OAuth callback")
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func readCodeManually(state string) (string, error) {
	fmt.Fprintln(os.Stderr, "After authorizing, the browser will land on the redirect URI (it may show an error page; that is fine).")
	fmt.Fprint(os.Stderr, "Paste the full redirect URL here: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read input: %w", err)
	}
	line = strings.TrimSpace(line)
	u, err := url.Parse(line)
	if err != nil {
		return "", fmt.Errorf("parse pasted url: %w", err)
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		return "", fmt.Errorf("authorization failed: %s", e)
	}
	if q.Get("state") != state {
		return "", errors.New("state mismatch (possible CSRF); retry the login")
	}
	code := q.Get("code")
	if code == "" {
		return "", errors.New("no code found in the pasted URL")
	}
	return code, nil
}

func cmdAuthStatus(args []string) error {
	fs := flag.NewFlagSet("auth status", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ts, err := tokenSource(cfg)
	if err != nil {
		return err
	}
	tok := ts.Current()
	fmt.Println("token file:   ", cfg.TokenFile)
	if w := cfg.TokenFileWarning(); w != "" {
		fmt.Println("warning:      ", w)
	}
	if tok == nil {
		if _, err := os.Stat(cfg.TokenFile); err != nil {
			fmt.Println("status:        not authenticated; token file does not exist (run `inoreader-mcp auth login`)")
		} else {
			fmt.Println("status:        not authenticated; token file has no usable token (run `inoreader-mcp auth login`)")
		}
		return nil
	}
	fmt.Println("scope:        ", tok.Scope)
	exp := time.Unix(tok.ExpiresAt, 0)
	state := "valid"
	if tok.Expired(time.Now()) {
		state = "expired (will refresh on next use)"
	}
	fmt.Printf("access token:  %s, expires %s\n", state, exp.Format(time.RFC1123))
	fmt.Printf("refresh token: %s\n", map[bool]string{true: "present", false: "missing"}[tok.RefreshToken != ""])
	return nil
}

func cmdAuthRefresh(args []string) error {
	fs := flag.NewFlagSet("auth refresh", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	ts, err := tokenSource(cfg)
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	if err := ts.Refresh(ctx); err != nil {
		return err
	}
	tok := ts.Current()
	fmt.Printf("refreshed; scope %q, valid until %s\n", tok.Scope, time.Unix(tok.ExpiresAt, 0).Format(time.RFC1123))
	return nil
}
