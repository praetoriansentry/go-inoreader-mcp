package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const briefingTemplate = `You are my chief of staff for information. Triage my Inoreader feed from the last %s and surface only what deserves attention.

Steps:
1. Call scan_recent_articles with hours=%s%s and titles_only=true (raise max_pages only if "truncated" is true and quota allows).
2. First pass: skim every title and feed. Pick 30-50 candidates that look relevant%s.
3. Second pass: call get_articles with the candidate IDs (up to 100 per call) and summary_chars=1500, then judge each on relevance, signal quality (not recycled takes or PR), timeliness, and depth.
4. Select 10-30 that clear the bar. Fewer, better picks beat a padded list. Drop duplicate coverage (keep the best source), paywalled stubs, and engagement bait.
5. Include exactly one wildcard: something outside my usual interests that might genuinely pique curiosity.
6. Rank by what I would most regret missing.

Output as Markdown:

# Daily Briefing — <date>

## Top Stories
### 1. <title>
**Source:** <feed/author>
**Link:** [<title>](<url>)
<1-2 sentences on why this matters to me. Be direct.>

(repeat)

## Also Worth a Skim
<3-5 items: title, source, markdown link, one sentence>

## Wildcard
### <title>
**Source:** ...
**Link:** ...
<why it is worth a look>

End with one line of stats: articles scanned, feeds, and remaining API quota from the tool output.%s`

func (s *Server) registerPrompts(srv *mcp.Server) {
	srv.AddPrompt(&mcp.Prompt{
		Name:        "daily_briefing",
		Title:       "Daily briefing",
		Description: "Triage recent articles against an interest profile and produce a ranked Markdown briefing.",
		Arguments: []*mcp.PromptArgument{
			{Name: "hours", Description: "Lookback window in hours (default 24)"},
			{Name: "stream_id", Description: "Restrict to a folder/feed stream ID (default: whole reading list)"},
			{Name: "interests", Description: "Free-text interest profile or path hint the client can read; used to judge relevance"},
			{Name: "unread_only", Description: "true to ignore articles already marked read"},
		},
	}, s.dailyBriefing)

	srv.AddPrompt(&mcp.Prompt{
		Name:        "feed_alert",
		Title:       "Feed alert",
		Description: "Check recent articles for specific topics or keywords and report only matches, for periodic notifications.",
		Arguments: []*mcp.PromptArgument{
			{Name: "topics", Description: "Comma-separated topics, keywords, companies, or questions to watch for", Required: true},
			{Name: "hours", Description: "Lookback window in hours (default 6)"},
			{Name: "stream_id", Description: "Restrict to a folder/feed stream ID"},
		},
	}, s.feedAlert)
}

func (s *Server) dailyBriefing(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := req.Params.Arguments
	hours := argOr(args, "hours", "24")
	extra := ""
	if sid := strings.TrimSpace(args["stream_id"]); sid != "" {
		extra += fmt.Sprintf(", stream_id=%q", sid)
	}
	if strings.EqualFold(strings.TrimSpace(args["unread_only"]), "true") {
		extra += ", unread_only=true"
	}
	relevance := ""
	interests := strings.TrimSpace(args["interests"])
	tail := ""
	if interests != "" {
		relevance = " to the interest profile below"
		tail = "\n\n## Interest profile\n" + interests
	}
	text := fmt.Sprintf(briefingTemplate, hours+" hours", hours, extra, relevance, tail)
	return &mcp.GetPromptResult{
		Description: "Daily briefing from Inoreader",
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
	}, nil
}

func (s *Server) feedAlert(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	args := req.Params.Arguments
	topics := strings.TrimSpace(args["topics"])
	if topics == "" {
		return nil, fmt.Errorf("topics argument is required")
	}
	hours := argOr(args, "hours", "6")
	extra := ""
	if sid := strings.TrimSpace(args["stream_id"]); sid != "" {
		extra = fmt.Sprintf(", stream_id=%q", sid)
	}
	text := fmt.Sprintf(`Watch my Inoreader feeds for these topics: %s

1. Call scan_recent_articles with hours=%s%s and summary_chars=300.
2. Find articles that substantively match any topic (not just a passing keyword mention). Read a candidate in full with get_articles if the summary is ambiguous.
3. If nothing matches, reply exactly: "No matches in the last %s hours." and stop.
4. Otherwise, for each match give: a one-line headline, the source, a markdown link, and one sentence on why it matters. Group by topic. Keep the whole reply short enough for a notification.`, topics, hours, extra, hours)
	return &mcp.GetPromptResult{
		Description: "Topic alert from Inoreader",
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
	}, nil
}

func argOr(args map[string]string, key, def string) string {
	if v := strings.TrimSpace(args[key]); v != "" {
		return v
	}
	return def
}
