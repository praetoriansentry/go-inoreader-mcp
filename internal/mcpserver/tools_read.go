package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/praetoriansentry/go-inoreader-mcp/internal/inoreader"
)

// --- get_server_status ---

type statusInput struct{}

type statusOutput struct {
	Version       string     `json:"version"`
	ReadOnly      bool       `json:"read_only" jsonschema:"True when write tools are disabled"`
	Authenticated bool       `json:"authenticated"`
	Scope         string     `json:"scope,omitempty" jsonschema:"Scope string recorded when the token was issued. Inoreader applies the app's current permission level, so writes may succeed even when this says read"`
	TokenExpires  string     `json:"token_expires,omitempty"`
	RateLimit     *RateLimit `json:"rate_limit,omitempty" jsonschema:"Quota as of the last API response, nil if no request made yet"`
}

func (s *Server) status(_ context.Context, _ *mcp.CallToolRequest, _ statusInput) (*mcp.CallToolResult, statusOutput, error) {
	out := statusOutput{Version: s.version, ReadOnly: s.readOnly, RateLimit: rateLimitOf(s.client)}
	if tok := s.client.Tokens().Current(); tok != nil && tok.RefreshToken != "" {
		out.Authenticated = true
		out.Scope = tok.Scope
		out.TokenExpires = time.Unix(tok.ExpiresAt, 0).UTC().Format(time.RFC3339)
	}
	return nil, out, nil
}

// --- get_user_info ---

type userInfoOutput struct {
	UserID    string     `json:"user_id"`
	UserName  string     `json:"user_name"`
	Email     string     `json:"email"`
	SignedUp  string     `json:"signed_up"`
	RateLimit *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) userInfo(ctx context.Context, _ *mcp.CallToolRequest, _ statusInput) (*mcp.CallToolResult, userInfoOutput, error) {
	ui, err := s.client.UserInfo(ctx)
	if err != nil {
		return nil, userInfoOutput{}, s.wrapErr(ctx, "get_user_info", err)
	}
	return nil, userInfoOutput{
		UserID: ui.UserID, UserName: ui.UserName, Email: ui.UserEmail,
		SignedUp:  time.Unix(ui.SignupTimeSec, 0).UTC().Format(time.RFC3339),
		RateLimit: rateLimitOf(s.client),
	}, nil
}

// --- list_subscriptions ---

type listSubscriptionsInput struct {
	Folder string `json:"folder,omitempty" jsonschema:"Only feeds in this folder (name or user/-/label/... ID)"`
	Query  string `json:"query,omitempty" jsonschema:"Case-insensitive substring to match against title or URL"`
}

type subscriptionOutput struct {
	ID       string   `json:"id" jsonschema:"Stream ID (feed/...) to use with other tools"`
	Title    string   `json:"title"`
	FeedURL  string   `json:"feed_url"`
	SiteURL  string   `json:"site_url,omitempty"`
	Folders  []string `json:"folders,omitempty"`
	FeedType string   `json:"feed_type,omitempty"`
}

type listSubscriptionsOutput struct {
	Count         int                  `json:"count"`
	Subscriptions []subscriptionOutput `json:"subscriptions"`
	RateLimit     *RateLimit           `json:"rate_limit,omitempty"`
}

func (s *Server) listSubscriptions(ctx context.Context, _ *mcp.CallToolRequest, in listSubscriptionsInput) (*mcp.CallToolResult, listSubscriptionsOutput, error) {
	subs, err := s.client.Subscriptions(ctx)
	if err != nil {
		return nil, listSubscriptionsOutput{}, s.wrapErr(ctx, "list_subscriptions", err)
	}
	folder := strings.TrimSpace(in.Folder)
	folderName := inoreader.LabelName(inoreader.LabelStream(folder))
	q := strings.ToLower(strings.TrimSpace(in.Query))
	out := listSubscriptionsOutput{Subscriptions: []subscriptionOutput{}}
	for _, sub := range subs {
		var folders []string
		inFolder := folder == ""
		for _, c := range sub.Categories {
			folders = append(folders, c.Label)
			if folder != "" && (strings.EqualFold(c.Label, folderName) || c.ID == folder) {
				inFolder = true
			}
		}
		if !inFolder {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(sub.Title), q) && !strings.Contains(strings.ToLower(sub.URL), q) && !strings.Contains(strings.ToLower(sub.HTMLURL), q) {
			continue
		}
		out.Subscriptions = append(out.Subscriptions, subscriptionOutput{
			ID: sub.ID, Title: sub.Title, FeedURL: sub.URL, SiteURL: sub.HTMLURL, Folders: folders, FeedType: sub.FeedType,
		})
	}
	sort.Slice(out.Subscriptions, func(i, j int) bool {
		return strings.ToLower(out.Subscriptions[i].Title) < strings.ToLower(out.Subscriptions[j].Title)
	})
	out.Count = len(out.Subscriptions)
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

// --- list_folders_and_tags ---

type tagOutput struct {
	ID          string `json:"id" jsonschema:"Stream ID (user/<uid>/label/<name>)"`
	Name        string `json:"name"`
	Type        string `json:"type" jsonschema:"folder, tag, active_search, or state (built-in stream)"`
	UnreadCount *int   `json:"unread_count,omitempty"`
	UnseenCount *int   `json:"unseen_count,omitempty"`
}

type listTagsOutput struct {
	Count     int         `json:"count"`
	Tags      []tagOutput `json:"tags"`
	RateLimit *RateLimit  `json:"rate_limit,omitempty"`
}

func (s *Server) listTags(ctx context.Context, _ *mcp.CallToolRequest, _ statusInput) (*mcp.CallToolResult, listTagsOutput, error) {
	tags, err := s.client.Tags(ctx)
	if err != nil {
		return nil, listTagsOutput{}, s.wrapErr(ctx, "list_folders_and_tags", err)
	}
	out := listTagsOutput{Tags: []tagOutput{}}
	for _, t := range tags {
		name := inoreader.LabelName(t.ID)
		typ := t.Type
		if name == "" {
			if i := strings.Index(t.ID, "/state/com.google/"); i >= 0 {
				name = t.ID[i+len("/state/com.google/"):]
				typ = "state"
			} else {
				name = t.ID
			}
		}
		out.Tags = append(out.Tags, tagOutput{ID: t.ID, Name: name, Type: typ, UnreadCount: t.UnreadCount, UnseenCount: t.UnseenCount})
	}
	out.Count = len(out.Tags)
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

// --- get_unread_counts ---

type unreadInput struct {
	IncludeZero bool `json:"include_zero,omitempty" jsonschema:"Include streams with zero unread items"`
}

type unreadEntry struct {
	StreamID string `json:"stream_id"`
	Count    int    `json:"count"`
	Newest   string `json:"newest,omitempty" jsonschema:"RFC 3339 time of the newest item"`
}

type unreadOutput struct {
	Max       int           `json:"max" jsonschema:"Counter ceiling; a count equal to max means 'max or more'"`
	Total     int           `json:"total_unread" jsonschema:"Unread count of the reading list, if reported"`
	Feeds     []unreadEntry `json:"feeds"`
	Folders   []unreadEntry `json:"folders"`
	RateLimit *RateLimit    `json:"rate_limit,omitempty"`
}

func (s *Server) unreadCounts(ctx context.Context, _ *mcp.CallToolRequest, in unreadInput) (*mcp.CallToolResult, unreadOutput, error) {
	uc, err := s.client.UnreadCounts(ctx)
	if err != nil {
		return nil, unreadOutput{}, s.wrapErr(ctx, "get_unread_counts", err)
	}
	out := unreadOutput{Max: uc.Max, Feeds: []unreadEntry{}, Folders: []unreadEntry{}}
	for _, u := range uc.UnreadCounts {
		if strings.HasSuffix(u.ID, "/state/com.google/reading-list") {
			out.Total = u.Count
			continue
		}
		if u.Count == 0 && !in.IncludeZero {
			continue
		}
		e := unreadEntry{StreamID: u.ID, Count: u.Count}
		if usec, ok := parseUsec(u.NewestItemTimestampUsec); ok {
			e.Newest = usec.UTC().Format(time.RFC3339)
		}
		switch {
		case strings.HasPrefix(u.ID, inoreader.FeedPrefix):
			out.Feeds = append(out.Feeds, e)
		default:
			out.Folders = append(out.Folders, e)
		}
	}
	sort.Slice(out.Feeds, func(i, j int) bool { return out.Feeds[i].Count > out.Feeds[j].Count })
	sort.Slice(out.Folders, func(i, j int) bool { return out.Folders[i].Count > out.Folders[j].Count })
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

func parseUsec(s string) (time.Time, bool) {
	var n int64
	if _, err := fmt.Sscan(s, &n); err != nil || n <= 0 {
		return time.Time{}, false
	}
	return time.UnixMicro(n), true
}

// --- get_stream_contents ---

type streamInput struct {
	StreamID     string  `json:"stream_id,omitempty" jsonschema:"Stream to read. Empty = reading list (all feeds). Also feed/<url>, user/-/label/<folder>, user/-/state/com.google/starred"`
	Limit        int     `json:"limit,omitempty" jsonschema:"Items per page, 1-100 (default 20)"`
	Continuation string  `json:"continuation,omitempty" jsonschema:"Token from a previous response to fetch the next page"`
	Hours        float64 `json:"hours,omitempty" jsonschema:"Only items published within the last N hours"`
	Since        string  `json:"since,omitempty" jsonschema:"Only items published after this RFC 3339 time (overrides hours)"`
	UnreadOnly   bool    `json:"unread_only,omitempty" jsonschema:"Exclude items already marked read"`
	StarredOnly  bool    `json:"starred_only,omitempty" jsonschema:"Only items that are also starred"`
	OldestFirst  bool    `json:"oldest_first,omitempty" jsonschema:"Return oldest items first (default newest first)"`
	SummaryChars int     `json:"summary_chars,omitempty" jsonschema:"Max characters of plain-text summary per item (default 600, -1 to omit summaries)"`
	IncludeHTML  bool    `json:"include_html,omitempty" jsonschema:"Include the raw HTML content (large)"`
	Annotations  bool    `json:"annotations,omitempty" jsonschema:"Include user highlights and notes"`
	AISummaries  bool    `json:"ai_summaries,omitempty" jsonschema:"Include Inoreader Intelligence summaries when available"`
}

type streamOutput struct {
	StreamID     string     `json:"stream_id"`
	Title        string     `json:"title,omitempty"`
	Count        int        `json:"count"`
	Items        []Article  `json:"items"`
	Continuation string     `json:"continuation,omitempty" jsonschema:"Pass back to fetch the next page; absent when no more items"`
	RateLimit    *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) streamOptions(in streamInput) (inoreader.StreamOptions, error) {
	since, err := s.parseSince(in.Hours, in.Since)
	if err != nil {
		return inoreader.StreamOptions{}, err
	}
	opts := inoreader.StreamOptions{
		Count:        in.Limit,
		OldestFirst:  in.OldestFirst,
		NewerThan:    since,
		ExcludeRead:  in.UnreadOnly,
		Continuation: in.Continuation,
		Annotations:  in.Annotations,
		AISummaries:  in.AISummaries,
	}
	if in.StarredOnly {
		opts.IncludeTarget = inoreader.StreamStarred
	}
	return opts, nil
}

func (s *Server) streamContents(ctx context.Context, _ *mcp.CallToolRequest, in streamInput) (*mcp.CallToolResult, streamOutput, error) {
	opts, err := s.streamOptions(in)
	if err != nil {
		return nil, streamOutput{}, err
	}
	sc, err := s.client.StreamContents(ctx, in.StreamID, opts)
	if err != nil {
		return nil, streamOutput{}, s.wrapErr(ctx, "get_stream_contents", err)
	}
	out := streamOutput{StreamID: sc.ID, Title: sc.Title, Items: []Article{}, Continuation: sc.Continuation}
	for _, it := range sc.Items {
		out.Items = append(out.Items, toArticle(it, summaryChars(in.SummaryChars), in.IncludeHTML))
	}
	out.Count = len(out.Items)
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

// --- get_item_ids ---

type itemIDsInput struct {
	StreamID     string  `json:"stream_id,omitempty" jsonschema:"Stream to read. Empty = reading list"`
	Limit        int     `json:"limit,omitempty" jsonschema:"IDs per page, 1-1000 (default 20)"`
	Continuation string  `json:"continuation,omitempty"`
	Hours        float64 `json:"hours,omitempty" jsonschema:"Only items published within the last N hours"`
	Since        string  `json:"since,omitempty" jsonschema:"Only items after this RFC 3339 time"`
	UnreadOnly   bool    `json:"unread_only,omitempty"`
	StarredOnly  bool    `json:"starred_only,omitempty"`
	OldestFirst  bool    `json:"oldest_first,omitempty"`
}

type itemIDsOutput struct {
	Count        int        `json:"count"`
	IDs          []string   `json:"ids" jsonschema:"Short-form article IDs"`
	Continuation string     `json:"continuation,omitempty"`
	RateLimit    *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) itemIDs(ctx context.Context, _ *mcp.CallToolRequest, in itemIDsInput) (*mcp.CallToolResult, itemIDsOutput, error) {
	opts, err := s.streamOptions(streamInput{
		StreamID: in.StreamID, Limit: in.Limit, Continuation: in.Continuation, Hours: in.Hours, Since: in.Since,
		UnreadOnly: in.UnreadOnly, StarredOnly: in.StarredOnly, OldestFirst: in.OldestFirst,
	})
	if err != nil {
		return nil, itemIDsOutput{}, err
	}
	res, err := s.client.StreamItemIDs(ctx, in.StreamID, opts)
	if err != nil {
		return nil, itemIDsOutput{}, s.wrapErr(ctx, "get_item_ids", err)
	}
	out := itemIDsOutput{IDs: []string{}, Continuation: res.Continuation}
	for _, r := range res.ItemRefs {
		out.IDs = append(out.IDs, r.ID)
	}
	out.Count = len(out.IDs)
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

// --- get_articles ---

type getArticlesInput struct {
	IDs          []string `json:"ids" jsonschema:"Article IDs (long or short form), at most 100"`
	SummaryChars int      `json:"summary_chars,omitempty" jsonschema:"Max characters of plain-text content (default 600, -1 to omit, large values return the whole article as text)"`
	IncludeHTML  bool     `json:"include_html,omitempty"`
}

type getArticlesOutput struct {
	Count     int        `json:"count"`
	Items     []Article  `json:"items"`
	RateLimit *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) getArticles(ctx context.Context, _ *mcp.CallToolRequest, in getArticlesInput) (*mcp.CallToolResult, getArticlesOutput, error) {
	if len(in.IDs) == 0 {
		return nil, getArticlesOutput{}, fmt.Errorf("ids is required")
	}
	if len(in.IDs) > 100 {
		return nil, getArticlesOutput{}, fmt.Errorf("at most 100 ids per call")
	}
	items, err := s.client.ItemsByID(ctx, in.IDs)
	if err != nil {
		return nil, getArticlesOutput{}, s.wrapErr(ctx, "get_articles", err)
	}
	out := getArticlesOutput{Items: []Article{}}
	for _, it := range items {
		out.Items = append(out.Items, toArticle(it, summaryChars(in.SummaryChars), in.IncludeHTML))
	}
	out.Count = len(out.Items)
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

// --- scan_recent_articles ---

type scanInput struct {
	Hours        float64  `json:"hours,omitempty" jsonschema:"Lookback window in hours (default 24)"`
	Since        string   `json:"since,omitempty" jsonschema:"Alternative to hours: RFC 3339 start time"`
	StreamID     string   `json:"stream_id,omitempty" jsonschema:"Stream to scan. Empty = reading list (all feeds); a folder is user/-/label/<name>"`
	UnreadOnly   bool     `json:"unread_only,omitempty" jsonschema:"Skip articles already marked read"`
	StarredOnly  bool     `json:"starred_only,omitempty"`
	MaxItems     int      `json:"max_items,omitempty" jsonschema:"Stop after this many articles (default 300, max 1000)"`
	MaxPages     int      `json:"max_pages,omitempty" jsonschema:"Max API requests to spend; each returns up to 100 articles (default 5, max 20)"`
	SummaryChars int      `json:"summary_chars,omitempty" jsonschema:"Max characters of plain-text summary per article (default 300, -1 to omit)"`
	TitlesOnly   bool     `json:"titles_only,omitempty" jsonschema:"Return only id, title, feed, url, published, read, starred per article: the cheapest way to triage a large window by headline, then fetch candidates with get_articles"`
	Folders      []string `json:"folders,omitempty" jsonschema:"Client-side filter: keep only articles in any of these folder names"`
	ExcludeFeeds []string `json:"exclude_feeds,omitempty" jsonschema:"Client-side filter: drop articles whose feed title or stream ID contains any of these substrings (case-insensitive)"`
	AISummaries  bool     `json:"ai_summaries,omitempty"`
}

type feedStat struct {
	Feed  string `json:"feed"`
	Count int    `json:"count"`
}

type scanOutput struct {
	Since      string     `json:"since" jsonschema:"Start of the window scanned (RFC 3339)"`
	Until      string     `json:"until"`
	StreamID   string     `json:"stream_id"`
	Count      int        `json:"count" jsonschema:"Articles returned after filtering"`
	Fetched    int        `json:"fetched" jsonschema:"Articles fetched from the API before client-side filters"`
	Truncated  bool       `json:"truncated" jsonschema:"True if max_items or max_pages stopped the scan before the window was exhausted"`
	FeedCounts []feedStat `json:"feed_counts" jsonschema:"Articles per feed, most active first"`
	Items      []Article  `json:"items" jsonschema:"Newest first"`
	RateLimit  *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) scanRecent(ctx context.Context, _ *mcp.CallToolRequest, in scanInput) (*mcp.CallToolResult, scanOutput, error) {
	if in.Hours <= 0 && in.Since == "" {
		in.Hours = 24
	}
	since, err := s.parseSince(in.Hours, in.Since)
	if err != nil {
		return nil, scanOutput{}, err
	}
	maxItems := in.MaxItems
	if maxItems <= 0 {
		maxItems = 300
	}
	if maxItems > 1000 {
		maxItems = 1000
	}
	maxPages := in.MaxPages
	if maxPages <= 0 {
		maxPages = 5
	}
	if maxPages > 20 {
		maxPages = 20
	}
	chars := in.SummaryChars
	if chars == 0 {
		chars = 300
	}
	opts := inoreader.StreamOptions{ExcludeRead: in.UnreadOnly, AISummaries: in.AISummaries}
	if in.StarredOnly {
		opts.IncludeTarget = inoreader.StreamStarred
	}
	streamID := in.StreamID
	if streamID == "" {
		streamID = inoreader.StreamReadingList
	}
	items, truncated, err := s.client.FetchRecent(ctx, streamID, since, opts, maxItems, maxPages)
	if err != nil && len(items) == 0 {
		return nil, scanOutput{}, s.wrapErr(ctx, "scan_recent_articles", err)
	}
	out := scanOutput{
		Since: since.UTC().Format(time.RFC3339), Until: s.now().UTC().Format(time.RFC3339),
		StreamID: streamID, Fetched: len(items), Truncated: truncated, Items: []Article{}, FeedCounts: []feedStat{},
	}
	counts := map[string]int{}
	for _, it := range items {
		if !matchesFolders(it, in.Folders) || excludedFeed(it, in.ExcludeFeeds) {
			continue
		}
		a := toArticle(it, summaryChars(chars), false)
		if in.TitlesOnly {
			a = Article{ID: a.ID, ShortID: a.ShortID, Title: a.Title, Feed: a.Feed, URL: a.URL, Published: a.Published, Read: a.Read, Starred: a.Starred}
		}
		out.Items = append(out.Items, a)
		counts[a.Feed]++
	}
	for f, n := range counts {
		out.FeedCounts = append(out.FeedCounts, feedStat{Feed: f, Count: n})
	}
	sort.Slice(out.FeedCounts, func(i, j int) bool {
		if out.FeedCounts[i].Count != out.FeedCounts[j].Count {
			return out.FeedCounts[i].Count > out.FeedCounts[j].Count
		}
		return out.FeedCounts[i].Feed < out.FeedCounts[j].Feed
	})
	out.Count = len(out.Items)
	out.RateLimit = rateLimitOf(s.client)
	if err != nil {
		// Partial result: surface the error text alongside what we got.
		s.logger.WarnContext(ctx, "scan returned partial results", "err", err)
		res := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "partial results; " + err.Error()}}}
		return res, out, nil
	}
	return nil, out, nil
}

func matchesFolders(it inoreader.Item, folders []string) bool {
	if len(folders) == 0 {
		return true
	}
	for _, have := range it.Labels() {
		for _, want := range folders {
			if strings.EqualFold(have, inoreader.LabelName(inoreader.LabelStream(want))) {
				return true
			}
		}
	}
	return false
}

func excludedFeed(it inoreader.Item, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	title := strings.ToLower(it.Origin.Title)
	id := strings.ToLower(it.Origin.StreamID)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && (strings.Contains(title, p) || strings.Contains(id, p)) {
			return true
		}
	}
	return false
}

// --- get_stream_preferences ---

type prefsOutput struct {
	Preferences map[string][]inoreader.StreamPref `json:"preferences"`
	RateLimit   *RateLimit                        `json:"rate_limit,omitempty"`
}

func (s *Server) streamPrefs(ctx context.Context, _ *mcp.CallToolRequest, _ statusInput) (*mcp.CallToolResult, prefsOutput, error) {
	p, err := s.client.StreamPreferences(ctx)
	if err != nil {
		return nil, prefsOutput{}, s.wrapErr(ctx, "get_stream_preferences", err)
	}
	return nil, prefsOutput{Preferences: p.StreamPrefs, RateLimit: rateLimitOf(s.client)}, nil
}

func (s *Server) registerReadTools(srv *mcp.Server) {
	mcp.AddTool(srv, readOnlyTool("get_server_status", "Server status",
		"Report server version, read-only mode, authentication/scope state, and the API quota observed on the last request. Uses no API quota."), s.status)
	mcp.AddTool(srv, readOnlyTool("get_user_info", "User info",
		"Fetch the authenticated Inoreader account's id, username, and email. (Zone 1 quota)"), s.userInfo)
	mcp.AddTool(srv, readOnlyTool("list_subscriptions", "List subscriptions",
		"List subscribed feeds with their stream IDs, URLs, and folders. Optionally filter by folder or a substring. (Zone 1 quota, one request)"), s.listSubscriptions)
	mcp.AddTool(srv, readOnlyTool("list_folders_and_tags", "List folders and tags",
		"List folders, tags, and active searches with unread counts. Use the returned IDs as stream_id elsewhere. (Zone 1 quota)"), s.listTags)
	mcp.AddTool(srv, readOnlyTool("get_unread_counts", "Unread counts",
		"Get unread article counts per feed and folder, plus the reading-list total. (Zone 1 quota)"), s.unreadCounts)
	mcp.AddTool(srv, readOnlyTool("get_stream_contents", "Read a stream",
		"Fetch one page of articles (up to 100) from a feed, folder, tag, system stream, or the whole reading list, newest first. Supports time window, unread/starred filters, and pagination via continuation. (Zone 1 quota, one request per page)"), s.streamContents)
	mcp.AddTool(srv, readOnlyTool("get_item_ids", "List article IDs",
		"Fetch up to 1000 article IDs from a stream without content; cheap way to count or diff items. (Zone 1 quota)"), s.itemIDs)
	mcp.AddTool(srv, readOnlyTool("get_articles", "Get articles by ID",
		"Fetch full content for specific article IDs (up to 100). Set summary_chars high (e.g. 20000) to read a whole article as plain text. (Zone 1 quota)"), s.getArticles)
	mcp.AddTool(srv, readOnlyTool("scan_recent_articles", "Scan recent articles",
		"Collect every article published in a time window (default last 24h) across the reading list or a folder/feed, paging automatically, with compact plain-text summaries and per-feed counts. This is the intended entry point for daily digests and feed monitoring. (Zone 1 quota: one request per 100 articles, bounded by max_pages)"), s.scanRecent)
	mcp.AddTool(srv, readOnlyTool("get_stream_preferences", "Stream preferences",
		"Return raw stream preferences such as custom subscription ordering (sort IDs). Rarely needed. (Zone 1 quota)"), s.streamPrefs)
}
