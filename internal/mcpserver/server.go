// Package mcpserver exposes the Inoreader API as MCP tools, prompts, and
// resources.
package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/praetoriansentry/go-inoreader-mcp/internal/inoreader"
)

// Options configures the MCP server.
type Options struct {
	// ReadOnly omits every tool that modifies the Inoreader account.
	ReadOnly bool
	Version  string
	Logger   *slog.Logger
}

// Server wires an Inoreader client to MCP.
type Server struct {
	client   *inoreader.Client
	readOnly bool
	version  string
	logger   *slog.Logger
	now      func() time.Time
}

const instructions = `This server gives access to the user's Inoreader RSS account.

Key concepts:
- Stream IDs identify what to read. Common values: "" or "user/-/state/com.google/reading-list" (everything),
  "user/-/state/com.google/starred" (starred), "user/-/label/<Folder or tag name>" (a folder/tag),
  "feed/<feed url>" (one feed). list_subscriptions and list_folders_and_tags return exact IDs.
- Article IDs come in a long form ("tag:google.com,2005:reader/item/<hex>") and a short decimal form; both are accepted everywhere.
- Inoreader enforces DAILY quotas: Zone 1 (reads) and Zone 2 (writes), often ~100 requests/day each on free plans.
  Every tool call that reaches the API consumes quota, and paging consumes one request per page.
  Prefer scan_recent_articles for digests (one call fetches up to 100 articles per page) and avoid
  repeated small calls. get_server_status shows remaining quota without using any.
- Summaries are returned as plain text (HTML stripped) and truncated unless you ask otherwise.`

// New builds an MCP server backed by the given client.
func New(client *inoreader.Client, opts Options) *mcp.Server {
	s := &Server{
		client:   client,
		readOnly: opts.ReadOnly,
		version:  opts.Version,
		logger:   opts.Logger,
		now:      time.Now,
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.version == "" {
		s.version = "dev"
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "inoreader",
		Title:   "Inoreader",
		Version: s.version,
	}, &mcp.ServerOptions{
		Instructions: instructions,
		Logger:       s.logger,
	})
	s.registerReadTools(srv)
	if !s.readOnly {
		s.registerWriteTools(srv)
	}
	s.registerPrompts(srv)
	s.registerResources(srv)
	return srv
}

func readOnlyTool(name, title, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, Title: title},
	}
}

func writeTool(name, title, desc string, destructive bool) *mcp.Tool {
	d := destructive
	return &mcp.Tool{
		Name:        name,
		Title:       title,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &d, IdempotentHint: true, Title: title},
	}
}

// Article is the compact article representation returned by tools.
type Article struct {
	ID          string   `json:"id" jsonschema:"Long-form article ID"`
	ShortID     string   `json:"short_id,omitempty" jsonschema:"Short decimal article ID"`
	Title       string   `json:"title"`
	Author      string   `json:"author,omitempty"`
	Published   string   `json:"published" jsonschema:"RFC 3339 publication time (UTC)"`
	URL         string   `json:"url,omitempty"`
	Feed        string   `json:"feed" jsonschema:"Feed title"`
	FeedID      string   `json:"feed_id,omitempty" jsonschema:"Feed stream ID"`
	FeedURL     string   `json:"feed_url,omitempty" jsonschema:"Feed website URL"`
	Folders     []string `json:"folders,omitempty" jsonschema:"Folders/tags containing the article"`
	Read        bool     `json:"read"`
	Starred     bool     `json:"starred"`
	Summary     string   `json:"summary,omitempty" jsonschema:"Plain-text content, truncated to summary_chars"`
	HTML        string   `json:"html,omitempty" jsonschema:"Raw HTML content (only when include_html is true)"`
	Annotations []string `json:"annotations,omitempty" jsonschema:"User highlights/notes"`
	AISummary   string   `json:"ai_summary,omitempty" jsonschema:"Inoreader Intelligence summary, if requested and present"`
}

func toArticle(it inoreader.Item, summaryChars int, includeHTML bool) Article {
	a := Article{
		ID:        it.ID,
		Title:     strings.TrimSpace(it.Title),
		Author:    strings.TrimSpace(it.Author),
		Published: time.Unix(it.Published, 0).UTC().Format(time.RFC3339),
		URL:       it.URL(),
		Feed:      it.Origin.Title,
		FeedID:    it.Origin.StreamID,
		FeedURL:   it.Origin.HTMLURL,
		Folders:   it.Labels(),
		Read:      it.IsRead(),
		Starred:   it.IsStarred(),
	}
	if sid, err := inoreader.ShortID(it.ID); err == nil {
		a.ShortID = sid
	}
	if summaryChars != 0 {
		a.Summary = inoreader.Truncate(inoreader.StripHTML(it.Summary.Content), summaryChars)
	}
	if includeHTML {
		a.HTML = it.Summary.Content
	}
	for _, an := range it.Annotations {
		s := strings.TrimSpace(an.Text)
		if n := strings.TrimSpace(an.Note); n != "" {
			s += " — note: " + n
		}
		if s != "" {
			a.Annotations = append(a.Annotations, s)
		}
	}
	if len(it.Summaries) > 0 {
		a.AISummary = inoreader.StripHTML(it.Summaries[0].Summary)
	}
	return a
}

// defaultSummaryChars is used when a tool input leaves summary_chars at 0.
const defaultSummaryChars = 600

func summaryChars(v int) int {
	if v == 0 {
		return defaultSummaryChars
	}
	if v < 0 {
		return 0 // explicitly disabled
	}
	return v
}

// parseSince interprets a lookback as either hours or an RFC 3339 time.
func (s *Server) parseSince(hours float64, since string) (time.Time, error) {
	if since != "" {
		t, err := time.Parse(time.RFC3339, since)
		if err != nil {
			return time.Time{}, fmt.Errorf("since must be RFC 3339 (e.g. 2026-01-02T15:04:05Z): %w", err)
		}
		return t, nil
	}
	if hours > 0 {
		return s.now().Add(-time.Duration(hours * float64(time.Hour))), nil
	}
	return time.Time{}, nil
}

func rateLimitOf(c *inoreader.Client) *RateLimit {
	rl := c.RateLimit()
	if !rl.HasObservations {
		return nil
	}
	return &RateLimit{
		Zone1Reads:  fmt.Sprintf("%d/%d", rl.Zone1Usage, rl.Zone1Limit),
		Zone2Writes: fmt.Sprintf("%d/%d", rl.Zone2Usage, rl.Zone2Limit),
		ResetsIn:    (time.Duration(rl.ResetAfterSec) * time.Second).Round(time.Minute).String(),
	}
}

// RateLimit summarizes API quota usage.
type RateLimit struct {
	Zone1Reads  string `json:"zone1_reads" jsonschema:"Read requests used today / daily limit"`
	Zone2Writes string `json:"zone2_writes" jsonschema:"Write requests used today / daily limit"`
	ResetsIn    string `json:"resets_in" jsonschema:"Time until the daily counters reset"`
}

func (s *Server) wrapErr(ctx context.Context, op string, err error) error {
	if err == nil {
		return nil
	}
	s.logger.WarnContext(ctx, "tool failed", "tool", op, "err", err)
	return err
}
