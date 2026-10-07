package inoreader

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// UserInfo returns basic account details.
func (c *Client) UserInfo(ctx context.Context) (*UserInfo, error) {
	var out UserInfo
	if err := c.get(ctx, "user-info", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Subscriptions lists the feeds the user follows.
func (c *Client) Subscriptions(ctx context.Context) ([]Subscription, error) {
	var out SubscriptionList
	if err := c.get(ctx, "subscription/list", nil, &out); err != nil {
		return nil, err
	}
	return out.Subscriptions, nil
}

// Tags lists folders, tags, and active searches with unread counts.
func (c *Client) Tags(ctx context.Context) ([]Tag, error) {
	q := url.Values{"types": {"1"}, "counts": {"1"}}
	var out TagList
	if err := c.get(ctx, "tag/list", q, &out); err != nil {
		return nil, err
	}
	return out.Tags, nil
}

// UnreadCounts returns unread counters for every feed and folder.
func (c *Client) UnreadCounts(ctx context.Context) (*UnreadCounts, error) {
	var out UnreadCounts
	if err := c.get(ctx, "unread-count", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamPreferences returns stream ordering and expansion preferences.
func (c *Client) StreamPreferences(ctx context.Context) (*StreamPrefs, error) {
	var out StreamPrefs
	if err := c.get(ctx, "preference/stream/list", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetStreamOrdering saves custom subscription ordering for a stream
// (Zone 2). value is the concatenation of sort IDs in the desired order.
func (c *Client) SetStreamOrdering(ctx context.Context, streamID, value string) error {
	form := url.Values{"s": {streamID}, "k": {"subscription-ordering"}, "v": {value}}
	return c.postForm(ctx, "preference/stream/set", form, nil)
}

// StreamOptions controls stream/contents and stream/items/ids queries.
type StreamOptions struct {
	// Count is the page size (1-100 for contents, up to 1000 for IDs).
	Count int
	// OldestFirst reverses the default newest-first order.
	OldestFirst bool
	// NewerThan limits results to items published after this time.
	NewerThan time.Time
	// ExcludeRead drops items already marked read.
	ExcludeRead bool
	// ExcludeTarget is an arbitrary stream to exclude (overrides ExcludeRead).
	ExcludeTarget string
	// IncludeTarget restricts to items also in this stream (e.g. starred).
	IncludeTarget string
	// Continuation resumes a previous page.
	Continuation string
	// Annotations includes user highlights.
	Annotations bool
	// AISummaries includes Inoreader Intelligence summaries.
	AISummaries bool
	// IncludeAllDirectStreamIDs defaults to true on the API; set
	// OnlyManualTags to receive just manually added tags.
	OnlyManualTags bool
}

func (o StreamOptions) values(maxCount int) url.Values {
	q := url.Values{}
	q.Set("output", "json")
	n := o.Count
	if n <= 0 {
		n = 20
	}
	if n > maxCount {
		n = maxCount
	}
	q.Set("n", strconv.Itoa(n))
	if o.OldestFirst {
		q.Set("r", "o")
	}
	if !o.NewerThan.IsZero() {
		q.Set("ot", strconv.FormatInt(o.NewerThan.Unix(), 10))
	}
	switch {
	case o.ExcludeTarget != "":
		q.Set("xt", o.ExcludeTarget)
	case o.ExcludeRead:
		q.Set("xt", StreamRead)
	}
	if o.IncludeTarget != "" {
		q.Set("it", o.IncludeTarget)
	}
	if o.Continuation != "" {
		q.Set("c", o.Continuation)
	}
	if o.Annotations {
		q.Set("annotations", "1")
	}
	if o.AISummaries {
		q.Set("summaries", "1")
	}
	if o.OnlyManualTags {
		q.Set("includeAllDirectStreamIds", "false")
	}
	return q
}

// StreamContents fetches a page of articles from a stream. An empty streamID
// means the reading list (all subscriptions).
func (c *Client) StreamContents(ctx context.Context, streamID string, opts StreamOptions) (*StreamContents, error) {
	if streamID == "" {
		streamID = StreamReadingList
	}
	var out StreamContents
	if err := c.get(ctx, "stream/contents/"+escapeStreamID(streamID), opts.values(100), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamItemIDs fetches a page of article IDs from a stream.
func (c *Client) StreamItemIDs(ctx context.Context, streamID string, opts StreamOptions) (*ItemIDs, error) {
	if streamID == "" {
		streamID = StreamReadingList
	}
	q := opts.values(1000)
	q.Set("s", streamID)
	var out ItemIDs
	if err := c.get(ctx, "stream/items/ids", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ItemsByID fetches full article content for specific item IDs (either form).
func (c *Client) ItemsByID(ctx context.Context, ids []string) ([]Item, error) {
	if len(ids) == 0 {
		return nil, errors.New("no item ids given")
	}
	form := url.Values{}
	for _, id := range ids {
		long, err := LongID(id)
		if err != nil {
			return nil, err
		}
		form.Add("i", long)
	}
	var out StreamContents
	if err := c.postForm(ctx, "stream/items/contents", form, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// EditTags adds and/or removes tags (states or labels) on items (Zone 2).
func (c *Client) EditTags(ctx context.Context, itemIDs []string, add, remove []string) error {
	if len(itemIDs) == 0 {
		return errors.New("no item ids given")
	}
	if len(add) == 0 && len(remove) == 0 {
		return errors.New("nothing to add or remove")
	}
	form := url.Values{}
	for _, id := range itemIDs {
		short, err := ShortID(id)
		if err != nil {
			return err
		}
		form.Add("i", short)
	}
	for _, a := range add {
		form.Add("a", a)
	}
	for _, r := range remove {
		form.Add("r", r)
	}
	return c.postForm(ctx, "edit-tag", form, nil)
}

// MarkRead marks items read.
func (c *Client) MarkRead(ctx context.Context, ids []string) error {
	return c.EditTags(ctx, ids, []string{StreamRead}, nil)
}

// MarkUnread marks items unread.
func (c *Client) MarkUnread(ctx context.Context, ids []string) error {
	return c.EditTags(ctx, ids, nil, []string{StreamRead})
}

// Star stars items.
func (c *Client) Star(ctx context.Context, ids []string) error {
	return c.EditTags(ctx, ids, []string{StreamStarred}, nil)
}

// Unstar removes the star from items.
func (c *Client) Unstar(ctx context.Context, ids []string) error {
	return c.EditTags(ctx, ids, nil, []string{StreamStarred})
}

// MarkAllAsRead marks every item in a stream older than ts as read (Zone 2).
// A zero ts means now.
func (c *Client) MarkAllAsRead(ctx context.Context, streamID string, ts time.Time) error {
	if streamID == "" {
		return errors.New("stream id is required")
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	form := url.Values{"s": {streamID}, "ts": {strconv.FormatInt(ts.Unix(), 10)}}
	return c.postForm(ctx, "mark-all-as-read", form, nil)
}

// QuickAdd subscribes to a feed by URL (Zone 2).
func (c *Client) QuickAdd(ctx context.Context, feedURL string) (*QuickAddResult, error) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return nil, errors.New("feed url is required")
	}
	form := url.Values{"quickadd": {FeedStream(feedURL)}}
	var out QuickAddResult
	if err := c.postForm(ctx, "subscription/quickadd", form, &out); err != nil {
		return nil, err
	}
	if out.NumResults == 0 {
		msg := out.Error
		if msg == "" {
			msg = "feed could not be added"
		}
		return &out, fmt.Errorf("quickadd %q: %s", feedURL, msg)
	}
	return &out, nil
}

// SubscriptionEdit describes a subscription/edit call.
type SubscriptionEdit struct {
	StreamID     string
	Title        string // optional new title
	AddFolder    string // folder name or user/-/label/... id
	RemoveFolder string
}

// EditSubscription renames a feed and/or moves it between folders (Zone 2).
func (c *Client) EditSubscription(ctx context.Context, e SubscriptionEdit) error {
	if e.StreamID == "" {
		return errors.New("stream id is required")
	}
	form := url.Values{"ac": {"edit"}, "s": {FeedStream(e.StreamID)}}
	if e.Title != "" {
		form.Set("t", e.Title)
	}
	if e.AddFolder != "" {
		form.Set("a", LabelStream(e.AddFolder))
	}
	if e.RemoveFolder != "" {
		form.Set("r", LabelStream(e.RemoveFolder))
	}
	return c.postForm(ctx, "subscription/edit", form, nil)
}

// Subscribe follows a feed via subscription/edit, optionally with a title and
// folder (Zone 2).
func (c *Client) Subscribe(ctx context.Context, feedURL, title, folder string) error {
	if strings.TrimSpace(feedURL) == "" {
		return errors.New("feed url is required")
	}
	form := url.Values{"ac": {"subscribe"}, "s": {FeedStream(feedURL)}}
	if title != "" {
		form.Set("t", title)
	}
	if folder != "" {
		form.Set("a", LabelStream(folder))
	}
	return c.postForm(ctx, "subscription/edit", form, nil)
}

// Unsubscribe removes a feed (Zone 2).
func (c *Client) Unsubscribe(ctx context.Context, streamID string) error {
	if strings.TrimSpace(streamID) == "" {
		return errors.New("stream id is required")
	}
	form := url.Values{"ac": {"unsubscribe"}, "s": {FeedStream(streamID)}}
	return c.postForm(ctx, "subscription/edit", form, nil)
}

// RenameTag renames a folder or tag (Zone 2). dest must not contain slashes.
func (c *Client) RenameTag(ctx context.Context, tag, dest string) error {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.Contains(dest, "/") {
		return errors.New("destination name is required and must not contain '/'")
	}
	form := url.Values{"s": {LabelStream(tag)}, "dest": {dest}}
	return c.postForm(ctx, "rename-tag", form, nil)
}

// DeleteTag deletes a folder or tag (Zone 2). Feeds inside a folder are kept.
func (c *Client) DeleteTag(ctx context.Context, tag string) error {
	if strings.TrimSpace(tag) == "" {
		return errors.New("tag is required")
	}
	form := url.Values{"s": {LabelStream(tag)}}
	return c.postForm(ctx, "disable-tag", form, nil)
}

// FetchRecent pages through a stream collecting every item newer than since,
// stopping after maxItems items or maxPages requests. It returns the items
// and whether the result was truncated by one of the limits.
func (c *Client) FetchRecent(ctx context.Context, streamID string, since time.Time, opts StreamOptions, maxItems, maxPages int) ([]Item, bool, error) {
	if maxItems <= 0 {
		maxItems = 500
	}
	if maxPages <= 0 {
		maxPages = 10
	}
	opts.NewerThan = since
	if opts.Count <= 0 || opts.Count > 100 {
		opts.Count = 100
	}
	var items []Item
	for page := 0; page < maxPages; page++ {
		sc, err := c.StreamContents(ctx, streamID, opts)
		if err != nil {
			return items, false, err
		}
		for _, it := range sc.Items {
			if !since.IsZero() && it.Published < since.Unix() {
				continue
			}
			items = append(items, it)
			if len(items) >= maxItems {
				return items, sc.Continuation != "" || len(sc.Items) > 0, nil
			}
		}
		if sc.Continuation == "" {
			return items, false, nil
		}
		opts.Continuation = sc.Continuation
	}
	return items, true, nil
}
