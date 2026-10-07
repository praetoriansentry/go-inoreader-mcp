package inoreader

import "encoding/json"

// Item is a single article as returned by stream/contents.
type Item struct {
	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Author        string       `json:"author,omitempty"`
	Published     int64        `json:"published"`
	Updated       int64        `json:"updated,omitempty"`
	CrawlTimeMsec string       `json:"crawlTimeMsec,omitempty"`
	TimestampUsec string       `json:"timestampUsec,omitempty"`
	Categories    []string     `json:"categories"`
	Canonical     []Link       `json:"canonical,omitempty"`
	Alternate     []Link       `json:"alternate,omitempty"`
	Summary       Summary      `json:"summary"`
	Origin        Origin       `json:"origin"`
	CommentsNum   int          `json:"commentsNum,omitempty"`
	Annotations   []Annotation `json:"annotations,omitempty"`
	Summaries     []AISummary  `json:"summaries,omitempty"`
}

// URL returns the best link for the item.
func (it Item) URL() string {
	if len(it.Canonical) > 0 && it.Canonical[0].Href != "" {
		return it.Canonical[0].Href
	}
	if len(it.Alternate) > 0 {
		return it.Alternate[0].Href
	}
	return ""
}

// HasCategory reports whether the item carries the given state/label suffix,
// e.g. "/state/com.google/read" or "/label/Tech".
func (it Item) HasCategory(suffix string) bool {
	for _, c := range it.Categories {
		if len(c) >= len(suffix) && c[len(c)-len(suffix):] == suffix {
			return true
		}
	}
	return false
}

// IsRead reports whether the item is marked read.
func (it Item) IsRead() bool { return it.HasCategory("/state/com.google/read") }

// IsStarred reports whether the item is starred.
func (it Item) IsStarred() bool { return it.HasCategory("/state/com.google/starred") }

// Labels returns the user label names attached to the item.
func (it Item) Labels() []string {
	var out []string
	for _, c := range it.Categories {
		if n := LabelName(c); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Link is an href/type pair.
type Link struct {
	Href string `json:"href"`
	Type string `json:"type,omitempty"`
}

// Summary holds the item content.
type Summary struct {
	Direction string `json:"direction,omitempty"`
	Content   string `json:"content"`
}

// Origin identifies the feed an item came from.
type Origin struct {
	StreamID string `json:"streamId"`
	Title    string `json:"title"`
	HTMLURL  string `json:"htmlUrl,omitempty"`
}

// Annotation is a user highlight/note on an item.
type Annotation struct {
	ID      int64  `json:"id"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	AddedOn int64  `json:"added_on"`
	Text    string `json:"text"`
	Note    string `json:"note"`
}

// AISummary is an Inoreader Intelligence summary attached to an item.
type AISummary struct {
	ID         int64  `json:"id"`
	PromptName string `json:"prompt_name"`
	Summary    string `json:"summary"`
}

// StreamContents is the response of stream/contents.
type StreamContents struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description,omitempty"`
	Updated      int64  `json:"updated"`
	Items        []Item `json:"items"`
	Continuation string `json:"continuation,omitempty"`
}

// ItemRef is a lightweight item reference from stream/items/ids.
type ItemRef struct {
	ID              string   `json:"id"`
	DirectStreamIDs []string `json:"directStreamIds"`
	TimestampUsec   string   `json:"timestampUsec"`
}

// ItemIDs is the response of stream/items/ids.
type ItemIDs struct {
	ItemRefs     []ItemRef `json:"itemRefs"`
	Continuation string    `json:"continuation,omitempty"`
}

// Category is a folder a subscription belongs to.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Subscription is a feed the user follows.
type Subscription struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Categories    []Category  `json:"categories"`
	SortID        string      `json:"sortid"`
	FirstItemMsec json.Number `json:"firstitemmsec"`
	URL           string      `json:"url"`
	HTMLURL       string      `json:"htmlUrl"`
	IconURL       string      `json:"iconUrl"`
	FeedType      string      `json:"feedType,omitempty"`
	Description   string      `json:"description,omitempty"`
}

// SubscriptionList is the response of subscription/list.
type SubscriptionList struct {
	Subscriptions []Subscription `json:"subscriptions"`
}

// Tag is a folder, tag, or active search.
type Tag struct {
	ID          string `json:"id"`
	SortID      string `json:"sortid"`
	Type        string `json:"type,omitempty"`
	UnreadCount *int   `json:"unread_count,omitempty"`
	UnseenCount *int   `json:"unseen_count,omitempty"`
}

// TagList is the response of tag/list.
type TagList struct {
	Tags []Tag `json:"tags"`
}

// UnreadCount is one entry of the unread-count response.
type UnreadCount struct {
	ID                      string `json:"id"`
	Count                   int    `json:"count"`
	NewestItemTimestampUsec string `json:"newestItemTimestampUsec"`
}

// UnreadCounts is the response of unread-count.
type UnreadCounts struct {
	Max          int           `json:"max"`
	UnreadCounts []UnreadCount `json:"unreadcounts"`
}

// UserInfo is the response of user-info.
type UserInfo struct {
	UserID              string `json:"userId"`
	UserName            string `json:"userName"`
	UserProfileID       string `json:"userProfileId"`
	UserEmail           string `json:"userEmail"`
	IsBloggerUser       bool   `json:"isBloggerUser"`
	SignupTimeSec       int64  `json:"signupTimeSec"`
	IsMultiLoginEnabled bool   `json:"isMultiLoginEnabled"`
}

// QuickAddResult is the response of subscription/quickadd.
type QuickAddResult struct {
	Query      string `json:"query"`
	NumResults int    `json:"numResults"`
	StreamID   string `json:"streamId,omitempty"`
	StreamName string `json:"streamName,omitempty"`
	Error      string `json:"error,omitempty"`
}

// StreamPref is a single stream preference.
type StreamPref struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// StreamPrefs is the response of preference/stream/list.
type StreamPrefs struct {
	StreamPrefs map[string][]StreamPref `json:"streamprefs"`
}
