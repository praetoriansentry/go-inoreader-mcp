# go-inoreader-mcp

An [MCP](https://modelcontextprotocol.io) server for [Inoreader](https://www.inoreader.com),
written in Go with no dependencies beyond the official
[MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk). It lets an LLM
client such as Claude read your feeds, build daily briefings, watch for
topics, and (optionally) manage your subscriptions.

- Covers the documented Inoreader Reader API: user info, subscriptions,
  folders/tags, unread counts, stream contents, item IDs, articles by ID,
  stream preferences, edit-tag (read/star/custom tags), mark-all-as-read,
  subscribe/unsubscribe/edit, rename/delete tags, subscription ordering.
- `scan_recent_articles` pages through a time window and returns compact,
  HTML-stripped articles with per-feed counts, built for digests.
- Two prompts (`daily_briefing`, `feed_alert`) encode a triage workflow.
- OAuth 2.0 with automatic refresh, quota tracking, read-only mode,
  stdio or authenticated streamable-HTTP transport, hardened container.

## Quick start

### 1. Register an Inoreader app

Inoreader → Preferences → Developer → *Create new application*. Choose
"Read and write" (or "Read only" if you never want write tools) and set the
redirect URI to `http://localhost:8080/callback`. Note the App ID and
App Key.

### 2. Configure

```sh
cp .env.example .env   # .env is gitignored
# fill in INOREADER_CLIENT_ID and INOREADER_CLIENT_SECRET
```

### 3. Authorize

Native binary:

```sh
go install github.com/praetoriansentry/go-inoreader-mcp/cmd/inoreader-mcp@latest
set -a; . ./.env; set +a
inoreader-mcp auth login            # opens a local callback on :8080
```

Docker (tokens persist in the `inoreader-data` volume):

```sh
docker build -t inoreader-mcp .
docker run -it --rm --env-file .env -v inoreader-data:/data -p 8080:8080 inoreader-mcp auth login
# or, where port publishing is awkward (SSH, remote Docker):
docker run -it --rm --env-file .env -v inoreader-data:/data inoreader-mcp auth login --manual
```

`auth login` prints a URL; approve the app in your browser. `auth status`
shows the token state without using API quota.

If your app's registered redirect URI is not `http://localhost...` (for
example the public domain from the section below), `auth login` falls back
to manual mode automatically: paste the URL your browser lands on, even if
that page shows a 404.

In Docker, keep `INOREADER_TOKEN_FILE` unset or under `/data`. The image
defaults to `/data/tokens.json`; a value like `tokens.json` in `.env` would
override it with a path inside the throwaway container filesystem, and the
login would appear to succeed but not persist. The server warns when it
detects this.

If `auth login` reports `token directory /data is not writable`, the volume
was created root-owned by an older image. Fix it in place:

```sh
docker run --rm -v inoreader-data:/data alpine chown 65532:65532 /data
```

### 4. Connect a client

**Claude Code** (stdio, native binary):

```sh
claude mcp add inoreader -- env $(cat .env | xargs) inoreader-mcp serve
```

**Claude Desktop / any stdio client** (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "inoreader": {
      "command": "docker",
      "args": ["run", "-i", "--rm", "--env-file", "/absolute/path/.env",
               "-v", "inoreader-data:/data", "inoreader-mcp", "serve"]
    }
  }
}
```

**Streamable HTTP** (for remote or long-running use):

```sh
echo "INOREADER_MCP_HTTP_TOKEN=$(openssl rand -hex 32)" >> .env
docker compose up -d        # serves http://127.0.0.1:8765/mcp
claude mcp add --transport http inoreader http://127.0.0.1:8765/mcp \
  --header "Authorization: Bearer $INOREADER_MCP_HTTP_TOKEN"
```

The server refuses to bind a non-loopback address without a bearer token.

### Exposing it to Claude on the web

Claude.ai custom connectors need a public HTTPS URL and can send a fixed
`Authorization` header on every request. Never expose the plain-HTTP
listener directly: the bearer token would travel in clear text, and that
token is full access to your Inoreader account through the tools.

`examples/public/` has a compose file where [Caddy](https://caddyserver.com)
terminates TLS with automatic certificates and proxies to the server over a
private Docker network (the server publishes no host ports):

```sh
cd examples/public
cp ../../.env.example .env                       # client id/secret
echo "INOREADER_MCP_HTTP_TOKEN=$(openssl rand -hex 32)" >> .env
echo "MCP_DOMAIN=mcp.example.com" >> .env        # DNS must point here
docker compose run --rm -p 8080:8080 inoreader-mcp auth login
docker compose up -d
curl https://mcp.example.com/healthz             # ok
```

Then in Claude.ai: *Customize → Connectors → Add custom connector*, URL
`https://mcp.example.com/mcp`, authentication *No sign in*, and under
*Request headers* add `Authorization: Bearer <your INOREADER_MCP_HTTP_TOKEN>`.

Hardening checklist for a public endpoint:

- Treat the bearer token like a password: 32+ random bytes, rotate by
  changing `.env` and restarting, never paste it into chats or issues.
- Consider `INOREADER_MCP_READ_ONLY=true` for the internet-facing instance
  if briefings are all you need; run writes locally over stdio.
- Keep the Docker volume with `tokens.json` private; anyone with it owns your
  Inoreader session until you revoke the app in Inoreader's settings.
- The `/healthz` endpoint is unauthenticated and returns only `ok`.
- If you put your own reverse proxy on the same host instead, the server
  disables the SDK's localhost Host-header check whenever a bearer token is
  set, so forwarding the public `Host` header works.

## Tools

Read tools (Inoreader "Zone 1" quota):

| Tool | Purpose |
|------|---------|
| `get_server_status` | Version, read-only flag, token scope/expiry, last-seen quota. Free. |
| `get_user_info` | Account id, name, email. |
| `list_subscriptions` | Feeds with stream IDs, URLs, folders; filter by folder/query. |
| `list_folders_and_tags` | Folders, tags, active searches with unread counts. |
| `get_unread_counts` | Unread counts per feed/folder plus reading-list total. |
| `get_stream_contents` | One page (≤100) of a feed/folder/tag/system stream with time, unread, starred filters and pagination. |
| `get_item_ids` | Up to 1000 article IDs from a stream without content. |
| `get_articles` | Full content for specific IDs; raise `summary_chars` to read whole articles. |
| `scan_recent_articles` | Everything in a time window across the reading list or a folder, auto-paged, with plain-text summaries and feed counts. `titles_only` returns just headlines for cheap triage. |
| `get_stream_preferences` | Raw ordering preferences. |

Write tools (Zone 2 quota; absent in read-only mode; the Inoreader app must be registered with read/write permission):

| Tool | Purpose |
|------|---------|
| `mark_items_read` / `mark_items_unread` | Toggle read state. |
| `star_items` / `unstar_items` | Toggle stars. |
| `edit_item_tags` | Add/remove custom tags on articles. |
| `mark_all_as_read` | Mark a stream read, optionally only items older than a time. `confirm` required. |
| `subscribe_feed` | Add a feed by URL (site URLs auto-discovered), with optional title/folder. |
| `unsubscribe_feed` | Remove a feed. `confirm` required. |
| `edit_subscription` | Rename or move a feed between folders. |
| `rename_folder_or_tag` / `delete_folder_or_tag` | Manage folders and tags. `confirm` required for delete. |
| `set_subscription_ordering` | Save custom sort order. |

Article IDs are accepted in long (`tag:google.com,2005:reader/item/…`) or
short decimal form. Stream IDs follow Inoreader conventions; the
`inoreader://docs/stream-ids` resource is a cheat sheet.

## Prompts

- `daily_briefing` (`hours`, `stream_id`, `interests`, `unread_only`):
  scan the window, triage by title then summary, output a ranked Markdown
  briefing with a wildcard pick.
- `feed_alert` (`topics`, `hours`, `stream_id`): report only articles that
  substantively match the topics, or a fixed "No matches" line, sized for a
  notification.

## Automating a daily summary

`examples/daily-summary.sh` runs Claude Code headlessly with the
`daily_briefing` prompt and writes a dated Markdown file; `examples/feed-alert.sh`
reports topic matches for a notifier. Both are cron-friendly. Client
configuration samples for Claude Desktop (stdio via Docker) and Claude Code
(streamable HTTP) are in `examples/` too.

```sh
claude mcp add inoreader -- env $(cat .env | xargs) inoreader-mcp serve --read-only
HOURS=24 ./examples/daily-summary.sh
TOPICS="eBPF,Go runtime" ./examples/feed-alert.sh
```

Quota matters: free Inoreader plans allow roughly 100 read requests per day.
A 24-hour scan of a few hundred articles costs 3–5 requests. Every tool
response includes the current `rate_limit`, and `get_server_status` reports
it without spending a request.

## Configuration

All settings are environment variables (see `.env.example`):

| Variable | Default | Notes |
|----------|---------|-------|
| `INOREADER_CLIENT_ID` | | required |
| `INOREADER_CLIENT_SECRET` | | required; or `INOREADER_CLIENT_SECRET_FILE` |
| `INOREADER_REDIRECT_URI` | `http://localhost:8080/callback` | must match the app registration |
| `INOREADER_TOKEN_FILE` | `$XDG_CONFIG_HOME/inoreader-mcp/tokens.json` (`/data/tokens.json` in Docker) | written with mode 0600 |
| `INOREADER_REFRESH_TOKEN` | | optional seed when no token file exists |
| `INOREADER_MCP_READ_ONLY` | `false` | drop all write tools |
| `INOREADER_MCP_HTTP_ADDR` | | enable streamable HTTP, e.g. `127.0.0.1:8765` |
| `INOREADER_MCP_HTTP_TOKEN` | | bearer token; required off-loopback; or `_FILE` |
| `INOREADER_MCP_LOG_LEVEL` | `info` | logs go to stderr |

CLI flags `serve --http ADDR` and `serve --read-only` override the
environment.

## Development

```sh
make check      # gofmt, vet, race tests
make build      # bin/inoreader-mcp
make docker     # local image
make vuln       # govulncheck
make hooks      # install the pre-commit secret guard (.githooks/pre-commit)
```

Tests run against an in-process fake of the Inoreader API and an in-memory
MCP transport; no credentials are needed.

## Security

See [SECURITY.md](SECURITY.md).

## License

MIT
