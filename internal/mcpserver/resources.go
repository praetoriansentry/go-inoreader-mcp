package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const streamIDsDoc = `# Inoreader stream and article IDs

## Stream IDs (use as stream_id)
- (empty) or user/-/state/com.google/reading-list : every subscribed feed
- user/-/state/com.google/starred                 : starred articles
- user/-/state/com.google/read                    : read articles
- user/-/state/com.google/broadcast               : broadcasted articles
- user/-/state/com.google/annotated               : articles with highlights/notes
- user/-/state/com.google/like                    : liked articles
- user/-/state/com.google/saved-web-pages         : saved web pages
- user/-/label/<name>                             : a folder or tag
- feed/<feed url>                                 : one feed, e.g. feed/https://example.com/rss

list_subscriptions and list_folders_and_tags return the exact IDs for your account
(folder IDs there contain the numeric user ID instead of "-"; both forms work).

## Article IDs
- Long form:  tag:google.com,2005:reader/item/00000000148b9369
- Short form: 344691561 (signed decimal of the same 64-bit value)
Every tool accepts either form.

## Quotas
Inoreader counts requests per day in two zones: Zone 1 (reads) and Zone 2 (writes).
Each tool description notes its zone. get_server_status shows the latest counters
without spending quota.
`

func (s *Server) registerResources(srv *mcp.Server) {
	srv.AddResource(&mcp.Resource{
		URI:         "inoreader://docs/stream-ids",
		Name:        "stream-ids",
		Title:       "Stream and article ID reference",
		Description: "Cheat sheet for Inoreader stream IDs, article ID forms, and quota zones.",
		MIMEType:    "text/markdown",
	}, func(_ context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI: req.Params.URI, MIMEType: "text/markdown", Text: streamIDsDoc,
		}}}, nil
	})
}
