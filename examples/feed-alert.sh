#!/usr/bin/env bash
# Periodic topic watch: prints matches (or a single "No matches" line) so the
# output can be piped to a notifier (ntfy, Pushover, email, Slack webhook...).
#
# Cron example (every 4 hours during the day):
#   0 8-20/4 * * * TOPICS="eBPF,Go runtime,Inoreader" /path/to/examples/feed-alert.sh | notify-send-or-similar
set -euo pipefail

cd "$(dirname "$0")"
: "${TOPICS:?set TOPICS to a comma-separated list}"
HOURS="${HOURS:-4}"

claude -p \
  --allowedTools "mcp__inoreader__scan_recent_articles,mcp__inoreader__get_articles" \
  "Use the inoreader MCP server's feed_alert prompt with topics=\"${TOPICS}\" and hours=${HOURS}. Reply with only the alert text."
