package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/praetoriansentry/go-inoreader-mcp/internal/inoreader"
)

type itemsInput struct {
	IDs []string `json:"ids" jsonschema:"Article IDs (long or short form)"`
}

type okOutput struct {
	OK        bool       `json:"ok"`
	Affected  int        `json:"affected,omitempty" jsonschema:"Number of items or objects the request targeted"`
	Message   string     `json:"message,omitempty"`
	RateLimit *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) ok(n int, msg string) okOutput {
	return okOutput{OK: true, Affected: n, Message: msg, RateLimit: rateLimitOf(s.client)}
}

func (s *Server) itemsOp(name string, fn func(context.Context, []string) error) func(context.Context, *mcp.CallToolRequest, itemsInput) (*mcp.CallToolResult, okOutput, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in itemsInput) (*mcp.CallToolResult, okOutput, error) {
		if len(in.IDs) == 0 {
			return nil, okOutput{}, fmt.Errorf("ids is required")
		}
		if err := fn(ctx, in.IDs); err != nil {
			return nil, okOutput{}, s.wrapErr(ctx, name, err)
		}
		return nil, s.ok(len(in.IDs), name+" applied"), nil
	}
}

// --- edit_item_tags ---

type editTagsInput struct {
	IDs        []string `json:"ids" jsonschema:"Article IDs (long or short form)"`
	AddTags    []string `json:"add_tags,omitempty" jsonschema:"Tag names to add (e.g. \"to-read\"); full user/-/label/... IDs and system states are also accepted"`
	RemoveTags []string `json:"remove_tags,omitempty" jsonschema:"Tag names to remove"`
}

func (s *Server) editTags(ctx context.Context, _ *mcp.CallToolRequest, in editTagsInput) (*mcp.CallToolResult, okOutput, error) {
	if len(in.IDs) == 0 {
		return nil, okOutput{}, fmt.Errorf("ids is required")
	}
	if len(in.AddTags) == 0 && len(in.RemoveTags) == 0 {
		return nil, okOutput{}, fmt.Errorf("add_tags or remove_tags is required")
	}
	add := make([]string, 0, len(in.AddTags))
	for _, t := range in.AddTags {
		add = append(add, inoreader.LabelStream(t))
	}
	rm := make([]string, 0, len(in.RemoveTags))
	for _, t := range in.RemoveTags {
		rm = append(rm, inoreader.LabelStream(t))
	}
	if err := s.client.EditTags(ctx, in.IDs, add, rm); err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "edit_item_tags", err)
	}
	return nil, s.ok(len(in.IDs), "tags updated"), nil
}

// --- mark_all_as_read ---

type markAllInput struct {
	StreamID  string `json:"stream_id" jsonschema:"Stream to mark read: feed/<url>, user/-/label/<folder>, or user/-/state/com.google/reading-list for everything"`
	OlderThan string `json:"older_than,omitempty" jsonschema:"Only mark items published before this RFC 3339 time (default: now). Protects articles that arrived after you last looked"`
	Confirm   bool   `json:"confirm,omitempty" jsonschema:"Must be true. This cannot be undone for items older than the feed's retention"`
}

func (s *Server) markAll(ctx context.Context, _ *mcp.CallToolRequest, in markAllInput) (*mcp.CallToolResult, okOutput, error) {
	if !in.Confirm {
		return nil, okOutput{}, fmt.Errorf("set confirm=true to mark all items in %q as read", in.StreamID)
	}
	if strings.TrimSpace(in.StreamID) == "" {
		return nil, okOutput{}, fmt.Errorf("stream_id is required")
	}
	var ts time.Time
	if in.OlderThan != "" {
		t, err := time.Parse(time.RFC3339, in.OlderThan)
		if err != nil {
			return nil, okOutput{}, fmt.Errorf("older_than must be RFC 3339: %w", err)
		}
		ts = t
	}
	if err := s.client.MarkAllAsRead(ctx, in.StreamID, ts); err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "mark_all_as_read", err)
	}
	return nil, s.ok(0, "stream marked read: "+in.StreamID), nil
}

// --- subscribe_feed ---

type subscribeInput struct {
	URL    string `json:"url" jsonschema:"Feed URL or website URL (Inoreader discovers the feed)"`
	Title  string `json:"title,omitempty" jsonschema:"Custom title for the subscription"`
	Folder string `json:"folder,omitempty" jsonschema:"Folder name to file the feed under (created if missing)"`
}

type subscribeOutput struct {
	OK        bool       `json:"ok"`
	StreamID  string     `json:"stream_id,omitempty" jsonschema:"Stream ID of the new subscription"`
	Title     string     `json:"title,omitempty"`
	RateLimit *RateLimit `json:"rate_limit,omitempty"`
}

func (s *Server) subscribe(ctx context.Context, _ *mcp.CallToolRequest, in subscribeInput) (*mcp.CallToolResult, subscribeOutput, error) {
	if strings.TrimSpace(in.URL) == "" {
		return nil, subscribeOutput{}, fmt.Errorf("url is required")
	}
	res, err := s.client.QuickAdd(ctx, in.URL)
	if err != nil {
		return nil, subscribeOutput{}, s.wrapErr(ctx, "subscribe_feed", err)
	}
	out := subscribeOutput{OK: true, StreamID: res.StreamID, Title: res.StreamName}
	if in.Title != "" || in.Folder != "" {
		if err := s.client.EditSubscription(ctx, inoreader.SubscriptionEdit{StreamID: res.StreamID, Title: in.Title, AddFolder: in.Folder}); err != nil {
			out.RateLimit = rateLimitOf(s.client)
			return nil, out, s.wrapErr(ctx, "subscribe_feed", fmt.Errorf("subscribed to %s but could not set title/folder: %w", res.StreamID, err))
		}
		if in.Title != "" {
			out.Title = in.Title
		}
	}
	out.RateLimit = rateLimitOf(s.client)
	return nil, out, nil
}

// --- unsubscribe_feed ---

type unsubscribeInput struct {
	StreamID string `json:"stream_id" jsonschema:"Feed stream ID (feed/<url>) from list_subscriptions"`
	Confirm  bool   `json:"confirm,omitempty" jsonschema:"Must be true"`
}

func (s *Server) unsubscribe(ctx context.Context, _ *mcp.CallToolRequest, in unsubscribeInput) (*mcp.CallToolResult, okOutput, error) {
	if !in.Confirm {
		return nil, okOutput{}, fmt.Errorf("set confirm=true to unsubscribe from %q", in.StreamID)
	}
	if err := s.client.Unsubscribe(ctx, in.StreamID); err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "unsubscribe_feed", err)
	}
	return nil, s.ok(1, "unsubscribed from "+in.StreamID), nil
}

// --- edit_subscription ---

type editSubInput struct {
	StreamID     string `json:"stream_id" jsonschema:"Feed stream ID (feed/<url>)"`
	Title        string `json:"title,omitempty" jsonschema:"New title (omit to keep)"`
	AddFolder    string `json:"add_folder,omitempty" jsonschema:"Folder name to add the feed to"`
	RemoveFolder string `json:"remove_folder,omitempty" jsonschema:"Folder name to remove the feed from"`
}

func (s *Server) editSubscription(ctx context.Context, _ *mcp.CallToolRequest, in editSubInput) (*mcp.CallToolResult, okOutput, error) {
	if in.Title == "" && in.AddFolder == "" && in.RemoveFolder == "" {
		return nil, okOutput{}, fmt.Errorf("nothing to change: give title, add_folder, or remove_folder")
	}
	err := s.client.EditSubscription(ctx, inoreader.SubscriptionEdit{
		StreamID: in.StreamID, Title: in.Title, AddFolder: in.AddFolder, RemoveFolder: in.RemoveFolder,
	})
	if err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "edit_subscription", err)
	}
	return nil, s.ok(1, "subscription updated"), nil
}

// --- rename_folder_or_tag / delete_folder_or_tag ---

type renameTagInput struct {
	Name    string `json:"name" jsonschema:"Current folder/tag name or user/-/label/... ID"`
	NewName string `json:"new_name" jsonschema:"New name (no slashes)"`
}

func (s *Server) renameTag(ctx context.Context, _ *mcp.CallToolRequest, in renameTagInput) (*mcp.CallToolResult, okOutput, error) {
	if err := s.client.RenameTag(ctx, in.Name, in.NewName); err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "rename_folder_or_tag", err)
	}
	return nil, s.ok(1, fmt.Sprintf("renamed %q to %q", in.Name, in.NewName)), nil
}

type deleteTagInput struct {
	Name    string `json:"name" jsonschema:"Folder/tag name or user/-/label/... ID"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"Must be true"`
}

func (s *Server) deleteTag(ctx context.Context, _ *mcp.CallToolRequest, in deleteTagInput) (*mcp.CallToolResult, okOutput, error) {
	if !in.Confirm {
		return nil, okOutput{}, fmt.Errorf("set confirm=true to delete folder/tag %q", in.Name)
	}
	if err := s.client.DeleteTag(ctx, in.Name); err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "delete_folder_or_tag", err)
	}
	return nil, s.ok(1, "deleted "+in.Name), nil
}

// --- set_subscription_ordering ---

type orderingInput struct {
	StreamID string `json:"stream_id,omitempty" jsonschema:"user/-/state/com.google/root for the top level, or a folder user/-/label/<name>"`
	SortIDs  string `json:"sort_ids" jsonschema:"Concatenated 8-character sort IDs (from get_stream_preferences / list_subscriptions) in the desired order"`
}

func (s *Server) setOrdering(ctx context.Context, _ *mcp.CallToolRequest, in orderingInput) (*mcp.CallToolResult, okOutput, error) {
	sid := in.StreamID
	if sid == "" {
		sid = inoreader.StreamRoot
	}
	if strings.TrimSpace(in.SortIDs) == "" {
		return nil, okOutput{}, fmt.Errorf("sort_ids is required")
	}
	if err := s.client.SetStreamOrdering(ctx, sid, in.SortIDs); err != nil {
		return nil, okOutput{}, s.wrapErr(ctx, "set_subscription_ordering", err)
	}
	return nil, s.ok(1, "ordering saved"), nil
}

func (s *Server) registerWriteTools(srv *mcp.Server) {
	const zone2 = " (Zone 2 quota; the Inoreader app must have read/write permission)"
	mcp.AddTool(srv, writeTool("mark_items_read", "Mark read",
		"Mark the given articles as read."+zone2, false), s.itemsOp("mark_items_read", s.client.MarkRead))
	mcp.AddTool(srv, writeTool("mark_items_unread", "Mark unread",
		"Mark the given articles as unread."+zone2, false), s.itemsOp("mark_items_unread", s.client.MarkUnread))
	mcp.AddTool(srv, writeTool("star_items", "Star",
		"Star (favorite) the given articles."+zone2, false), s.itemsOp("star_items", s.client.Star))
	mcp.AddTool(srv, writeTool("unstar_items", "Unstar",
		"Remove the star from the given articles."+zone2, false), s.itemsOp("unstar_items", s.client.Unstar))
	mcp.AddTool(srv, writeTool("edit_item_tags", "Edit tags",
		"Add and/or remove custom tags on articles. Useful for labeling items picked for a digest."+zone2, false), s.editTags)
	mcp.AddTool(srv, writeTool("mark_all_as_read", "Mark all read",
		"Mark every article in a stream as read, optionally only those older than a timestamp. Requires confirm=true."+zone2, true), s.markAll)
	mcp.AddTool(srv, writeTool("subscribe_feed", "Subscribe",
		"Subscribe to a feed by URL (website URLs are auto-discovered), optionally with a title and folder."+zone2, false), s.subscribe)
	mcp.AddTool(srv, writeTool("unsubscribe_feed", "Unsubscribe",
		"Unsubscribe from a feed. Requires confirm=true."+zone2, true), s.unsubscribe)
	mcp.AddTool(srv, writeTool("edit_subscription", "Edit subscription",
		"Rename a feed or move it between folders."+zone2, false), s.editSubscription)
	mcp.AddTool(srv, writeTool("rename_folder_or_tag", "Rename folder/tag",
		"Rename a folder or tag."+zone2, false), s.renameTag)
	mcp.AddTool(srv, writeTool("delete_folder_or_tag", "Delete folder/tag",
		"Delete a folder or tag (feeds inside a folder are kept). Requires confirm=true."+zone2, true), s.deleteTag)
	mcp.AddTool(srv, writeTool("set_subscription_ordering", "Set ordering",
		"Save a custom ordering of feeds/folders using sort IDs. Rarely needed."+zone2, false), s.setOrdering)
}
