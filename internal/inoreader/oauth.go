package inoreader

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OAuth endpoints documented at https://www.inoreader.com/developers/oauth.
const (
	DefaultAuthURL  = "https://www.inoreader.com/oauth2/auth"
	DefaultTokenURL = "https://www.inoreader.com/oauth2/token"

	// ScopeRead grants read-only access; ScopeReadWrite also allows edits.
	ScopeRead      = "read"
	ScopeReadWrite = "read write"
)

// refreshSkew refreshes the access token this long before it expires.
const refreshSkew = 5 * time.Minute

// Token is a persisted OAuth token. The JSON layout is intentionally
// compatible with simple scripts that store "expires_at" as a Unix time.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	ExpiresAt    int64  `json:"expires_at"`
}

// Expired reports whether the access token is expired or about to expire.
func (t *Token) Expired(now time.Time) bool {
	if t == nil || t.AccessToken == "" {
		return true
	}
	return now.Add(refreshSkew).Unix() >= t.ExpiresAt
}

// HasScope reports whether the token's scope includes the given scope word.
func (t *Token) HasScope(scope string) bool {
	if t == nil {
		return false
	}
	for _, s := range strings.Fields(t.Scope) {
		if s == scope {
			return true
		}
	}
	return false
}

// OAuthConfig describes a registered Inoreader application.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	// AuthURL and TokenURL default to the Inoreader endpoints.
	AuthURL  string
	TokenURL string
	// HTTPClient defaults to a client with a sane timeout.
	HTTPClient *http.Client
	UserAgent  string
}

func (c *OAuthConfig) authURL() string {
	if c.AuthURL != "" {
		return c.AuthURL
	}
	return DefaultAuthURL
}

func (c *OAuthConfig) tokenURL() string {
	if c.TokenURL != "" {
		return c.TokenURL
	}
	return DefaultTokenURL
}

func (c *OAuthConfig) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// NewState returns a random, URL-safe CSRF state value.
func NewState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthCodeURL builds the URL the user must visit to authorize the app.
func (c *OAuthConfig) AuthCodeURL(state, scope string) string {
	q := url.Values{}
	q.Set("client_id", c.ClientID)
	q.Set("redirect_uri", c.RedirectURI)
	q.Set("response_type", "code")
	if scope != "" {
		q.Set("scope", scope)
	}
	q.Set("state", state)
	return c.authURL() + "?" + q.Encode()
}

// Exchange trades an authorization code for tokens.
func (c *OAuthConfig) Exchange(ctx context.Context, code string) (*Token, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("redirect_uri", c.RedirectURI)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("scope", "")
	form.Set("grant_type", "authorization_code")
	return c.tokenRequest(ctx, form, "")
}

// Refresh obtains a new access token using a refresh token.
func (c *OAuthConfig) Refresh(ctx context.Context, refreshToken string) (*Token, error) {
	if refreshToken == "" {
		return nil, errors.New("no refresh token available; run `inoreader-mcp auth login`")
	}
	form := url.Values{}
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	return c.tokenRequest(ctx, form, refreshToken)
}

type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	TokenType        string `json:"token_type"`
	Scope            string `json:"scope"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func (c *OAuthConfig) tokenRequest(ctx context.Context, form url.Values, prevRefresh string) (*Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("oauth token request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("oauth token response: %w", err)
	}
	var tr tokenResponse
	if jerr := json.Unmarshal(body, &tr); jerr != nil && resp.StatusCode == http.StatusOK {
		return nil, fmt.Errorf("oauth token response: invalid JSON: %w", jerr)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		msg := tr.Error
		if tr.ErrorDescription != "" {
			msg += ": " + tr.ErrorDescription
		}
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		return nil, &OAuthError{StatusCode: resp.StatusCode, Message: msg}
	}
	tok := &Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    tr.TokenType,
		Scope:        tr.Scope,
		ExpiresAt:    time.Now().Unix() + tr.ExpiresIn,
	}
	if tok.RefreshToken == "" {
		tok.RefreshToken = prevRefresh
	}
	if tok.TokenType == "" {
		tok.TokenType = "Bearer"
	}
	return tok, nil
}

// OAuthError is returned for failed token endpoint calls.
type OAuthError struct {
	StatusCode int
	Message    string
}

func (e *OAuthError) Error() string {
	return fmt.Sprintf("oauth: HTTP %d: %s", e.StatusCode, e.Message)
}

// TokenStore persists tokens between runs.
type TokenStore interface {
	Load() (*Token, error)
	Save(*Token) error
}

// ErrNoToken is returned by a store that has nothing saved yet.
var ErrNoToken = errors.New("no saved token")

// FileTokenStore stores the token as JSON in a file with 0600 permissions.
type FileTokenStore struct {
	Path string
}

// Load reads the token file.
func (s FileTokenStore) Load() (*Token, error) {
	b, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoToken
		}
		return nil, err
	}
	var t Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("parse token file %s: %w", s.Path, err)
	}
	if t.AccessToken == "" && t.RefreshToken == "" {
		return nil, ErrNoToken
	}
	return &t, nil
}

// Save atomically writes the token file with mode 0600.
func (s FileTokenStore) Save(t *Token) error {
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tokens-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, s.Path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// MemoryTokenStore keeps the token in memory only.
type MemoryTokenStore struct {
	mu  sync.Mutex
	tok *Token
}

// Load returns the in-memory token.
func (s *MemoryTokenStore) Load() (*Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tok == nil {
		return nil, ErrNoToken
	}
	cp := *s.tok
	return &cp, nil
}

// Save stores the token in memory.
func (s *MemoryTokenStore) Save(t *Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *t
	s.tok = &cp
	return nil
}

// TokenSource yields valid access tokens, refreshing and persisting them as
// needed. It is safe for concurrent use.
type TokenSource struct {
	cfg   *OAuthConfig
	store TokenStore
	now   func() time.Time

	mu  sync.Mutex
	tok *Token
}

// NewTokenSource creates a TokenSource backed by the given store. If the
// store is empty and seed is non-nil, seed is used as the initial token.
func NewTokenSource(cfg *OAuthConfig, store TokenStore, seed *Token) (*TokenSource, error) {
	if store == nil {
		store = &MemoryTokenStore{}
	}
	ts := &TokenSource{cfg: cfg, store: store, now: time.Now}
	tok, err := store.Load()
	switch {
	case err == nil:
		ts.tok = tok
	case errors.Is(err, ErrNoToken):
		if seed != nil {
			ts.tok = seed
		}
	default:
		return nil, err
	}
	return ts, nil
}

// Current returns a copy of the current token without refreshing.
func (ts *TokenSource) Current() *Token {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tok == nil {
		return nil
	}
	cp := *ts.tok
	return &cp
}

// AccessToken returns a valid access token, refreshing if necessary.
func (ts *TokenSource) AccessToken(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tok == nil {
		return "", errors.New("not authenticated; run `inoreader-mcp auth login`")
	}
	if !ts.tok.Expired(ts.now()) {
		return ts.tok.AccessToken, nil
	}
	if err := ts.refreshLocked(ctx); err != nil {
		return "", err
	}
	return ts.tok.AccessToken, nil
}

// Refresh forces a token refresh, e.g. after a 401.
func (ts *TokenSource) Refresh(ctx context.Context) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.tok == nil {
		return errors.New("not authenticated; run `inoreader-mcp auth login`")
	}
	return ts.refreshLocked(ctx)
}

// Set replaces the token and persists it (used after an interactive login).
func (ts *TokenSource) Set(t *Token) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	cp := *t
	ts.tok = &cp
	return ts.store.Save(ts.tok)
}

func (ts *TokenSource) refreshLocked(ctx context.Context) error {
	nt, err := ts.cfg.Refresh(ctx, ts.tok.RefreshToken)
	if err != nil {
		return fmt.Errorf("refresh access token: %w", err)
	}
	if nt.Scope == "" {
		nt.Scope = ts.tok.Scope
	}
	ts.tok = nt
	if err := ts.store.Save(nt); err != nil {
		return fmt.Errorf("persist refreshed token: %w", err)
	}
	return nil
}
