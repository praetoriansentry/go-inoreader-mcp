package inoreader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the Inoreader API host.
const DefaultBaseURL = "https://www.inoreader.com"

const apiPrefix = "/reader/api/0/"

// maxResponseBytes bounds how much of a response body is read.
const maxResponseBytes = 32 << 20

// RateLimit is the quota state reported by the most recent API response.
// Zone 1 covers reads, Zone 2 covers writes. Limits reset daily.
type RateLimit struct {
	Zone1Limit      int       `json:"zone1_limit"`
	Zone1Usage      int       `json:"zone1_usage"`
	Zone2Limit      int       `json:"zone2_limit"`
	Zone2Usage      int       `json:"zone2_usage"`
	ResetAfterSec   int       `json:"reset_after_seconds"`
	ObservedAt      time.Time `json:"observed_at"`
	HasObservations bool      `json:"has_observations"`
}

// APIError is a non-2xx response from the API.
type APIError struct {
	StatusCode int
	Message    string
	Endpoint   string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("inoreader %s: HTTP %d: %s", e.Endpoint, e.StatusCode, msg)
}

// ErrRateLimited is wrapped by errors for HTTP 429 responses.
var ErrRateLimited = errors.New("daily API quota exhausted")

// ErrUnauthorized is wrapped by errors for HTTP 401 responses that persist
// after a token refresh.
var ErrUnauthorized = errors.New("unauthorized")

// ErrForbidden is wrapped by errors for HTTP 403 responses (wrong app
// credentials, missing write scope, or unimplemented method).
var ErrForbidden = errors.New("forbidden")

// Is lets callers match sentinel errors with errors.Is.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrRateLimited:
		return e.StatusCode == http.StatusTooManyRequests
	case ErrUnauthorized:
		return e.StatusCode == http.StatusUnauthorized
	case ErrForbidden:
		return e.StatusCode == http.StatusForbidden
	}
	return false
}

// Client talks to the Inoreader API.
type Client struct {
	baseURL   string
	http      *http.Client
	tokens    *TokenSource
	userAgent string
	logger    *slog.Logger

	mu   sync.Mutex
	rate RateLimit
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API host (useful for tests).
func WithBaseURL(u string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") }
}

// WithHTTPClient overrides the HTTP client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// WithLogger sets a logger. Tokens are never logged.
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) { c.logger = l }
}

// NewClient creates a client that authenticates with the given token source.
func NewClient(ts *TokenSource, opts ...Option) *Client {
	c := &Client{
		baseURL:   DefaultBaseURL,
		http:      &http.Client{Timeout: 60 * time.Second},
		tokens:    ts,
		userAgent: "go-inoreader-mcp",
		logger:    slog.New(slog.DiscardHandler),
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// RateLimit returns the most recently observed quota state.
func (c *Client) RateLimit() RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rate
}

// Tokens exposes the token source (for status reporting).
func (c *Client) Tokens() *TokenSource { return c.tokens }

func (c *Client) updateRate(h http.Header) {
	get := func(k string) (int, bool) {
		v := h.Get(k)
		if v == "" {
			return 0, false
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	}
	z1l, ok1 := get("X-Reader-Zone1-Limit")
	if !ok1 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rate.Zone1Limit = z1l
	c.rate.Zone1Usage, _ = get("X-Reader-Zone1-Usage")
	c.rate.Zone2Limit, _ = get("X-Reader-Zone2-Limit")
	c.rate.Zone2Usage, _ = get("X-Reader-Zone2-Usage")
	c.rate.ResetAfterSec, _ = get("X-Reader-Limits-Reset-After")
	c.rate.ObservedAt = time.Now()
	c.rate.HasObservations = true
}

// get performs an authenticated GET against an API path (relative to
// /reader/api/0/) and decodes JSON into out.
func (c *Client) get(ctx context.Context, endpoint string, q url.Values, out any) error {
	return c.do(ctx, http.MethodGet, endpoint, q, nil, out)
}

// postForm performs an authenticated form POST. If out is nil the body is
// expected to be the literal "OK".
func (c *Client) postForm(ctx context.Context, endpoint string, form url.Values, out any) error {
	return c.do(ctx, http.MethodPost, endpoint, nil, form, out)
}

func (c *Client) do(ctx context.Context, method, endpoint string, q, form url.Values, out any) error {
	body, status, err := c.roundTrip(ctx, method, endpoint, q, form, true)
	if err != nil {
		return err
	}
	_ = status
	if out == nil {
		if s := strings.TrimSpace(string(body)); s != "OK" && s != "" {
			// Some endpoints return JSON even on success; only flag obvious errors.
			if strings.HasPrefix(s, "Error=") {
				return &APIError{StatusCode: status, Message: strings.TrimPrefix(s, "Error="), Endpoint: endpoint}
			}
		}
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("inoreader %s: decode response: %w", endpoint, err)
	}
	return nil
}

func (c *Client) roundTrip(ctx context.Context, method, endpoint string, q, form url.Values, retryAuth bool) ([]byte, int, error) {
	tok, err := c.tokens.AccessToken(ctx)
	if err != nil {
		return nil, 0, err
	}
	u := c.baseURL + apiPrefix + endpoint
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var bodyReader io.Reader
	if form != nil {
		bodyReader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("inoreader %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	c.updateRate(resp.Header)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("inoreader %s: read body: %w", endpoint, err)
	}
	c.logger.Debug("inoreader request",
		"method", method, "endpoint", endpoint, "status", resp.StatusCode,
		"bytes", len(body), "duration", time.Since(start).Round(time.Millisecond))

	if resp.StatusCode == http.StatusUnauthorized && retryAuth {
		c.logger.Info("access token rejected; refreshing")
		if rerr := c.tokens.Refresh(ctx); rerr != nil {
			return nil, resp.StatusCode, &APIError{StatusCode: resp.StatusCode, Message: "token refresh failed: " + rerr.Error(), Endpoint: endpoint}
		}
		return c.roundTrip(ctx, method, endpoint, q, form, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(body))
		msg = strings.TrimPrefix(msg, "Error=")
		if len(msg) > 512 {
			msg = msg[:512] + "..."
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			rl := c.RateLimit()
			msg = fmt.Sprintf("%s (zone1 %d/%d, zone2 %d/%d, resets in %s)",
				ErrRateLimited.Error(), rl.Zone1Usage, rl.Zone1Limit, rl.Zone2Usage, rl.Zone2Limit,
				(time.Duration(rl.ResetAfterSec) * time.Second).Round(time.Minute))
		}
		return nil, resp.StatusCode, &APIError{StatusCode: resp.StatusCode, Message: msg, Endpoint: endpoint}
	}
	return body, resp.StatusCode, nil
}

// escapeStreamID encodes a stream ID for use as a path segment. Slashes must
// be percent-encoded, which url.PathEscape does not do.
func escapeStreamID(id string) string {
	return strings.ReplaceAll(url.PathEscape(id), "/", "%2F")
}
