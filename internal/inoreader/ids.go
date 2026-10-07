package inoreader

import (
	"fmt"
	"strconv"
	"strings"
)

// LongIDPrefix is the prefix of the long article ID form.
const LongIDPrefix = "tag:google.com,2005:reader/item/"

// Stream IDs for the system streams documented at
// https://www.inoreader.com/developers/stream-ids.
const (
	StreamReadingList   = "user/-/state/com.google/reading-list"
	StreamRead          = "user/-/state/com.google/read"
	StreamStarred       = "user/-/state/com.google/starred"
	StreamBroadcast     = "user/-/state/com.google/broadcast"
	StreamAnnotated     = "user/-/state/com.google/annotated"
	StreamLike          = "user/-/state/com.google/like"
	StreamSavedWebPages = "user/-/state/com.google/saved-web-pages"
	StreamRoot          = "user/-/state/com.google/root"
	LabelPrefix         = "user/-/label/"
	FeedPrefix          = "feed/"
)

// ShortID converts an article ID in either form to the short decimal form
// preferred by the edit-tag endpoint. Short IDs are returned unchanged.
func ShortID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("empty article id")
	}
	if !strings.HasPrefix(id, LongIDPrefix) {
		if _, err := strconv.ParseInt(id, 10, 64); err != nil {
			return "", fmt.Errorf("invalid article id %q", id)
		}
		return id, nil
	}
	hex := strings.TrimPrefix(id, LongIDPrefix)
	u, err := strconv.ParseUint(hex, 16, 64)
	if err != nil {
		return "", fmt.Errorf("invalid long article id %q: %w", id, err)
	}
	// The short form is a signed base-10 number of the same 64 bits.
	return strconv.FormatInt(int64(u), 10), nil
}

// LongID converts an article ID in either form to the long
// "tag:google.com,2005:reader/item/<16 hex digits>" form.
func LongID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if strings.HasPrefix(id, LongIDPrefix) {
		hex := strings.TrimPrefix(id, LongIDPrefix)
		if _, err := strconv.ParseUint(hex, 16, 64); err != nil {
			return "", fmt.Errorf("invalid long article id %q: %w", id, err)
		}
		return LongIDPrefix + fmt.Sprintf("%016s", strings.ToLower(hex)), nil
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid article id %q", id)
	}
	return fmt.Sprintf("%s%016x", LongIDPrefix, uint64(n)), nil
}

// LabelStream returns the stream ID for a user folder/tag name. Names that
// already look like a stream ID are returned unchanged.
func LabelStream(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, "user/") || strings.HasPrefix(name, FeedPrefix) {
		return name
	}
	return LabelPrefix + name
}

// FeedStream returns the stream ID for a feed URL. IDs that already carry the
// feed/ prefix are returned unchanged.
func FeedStream(feedURL string) string {
	feedURL = strings.TrimSpace(feedURL)
	if strings.HasPrefix(feedURL, FeedPrefix) {
		return feedURL
	}
	return FeedPrefix + feedURL
}

// IsSystemState reports whether a category/stream ID is one of Inoreader's
// built-in states (read, starred, ...), as opposed to a user label.
func IsSystemState(id string) bool {
	return strings.Contains(id, "/state/com.google/")
}

// LabelName extracts the human-readable name from a label stream ID such as
// "user/1234/label/Tech" or "user/-/label/Tech". Non-label IDs yield "".
func LabelName(id string) string {
	i := strings.Index(id, "/label/")
	if i < 0 {
		return ""
	}
	return id[i+len("/label/"):]
}
