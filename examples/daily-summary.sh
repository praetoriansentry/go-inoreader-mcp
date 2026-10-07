#!/usr/bin/env bash
# Produce a daily briefing from Inoreader with Claude Code and the MCP server.
#
# Prerequisites:
#   - `claude mcp add inoreader ...` (see README) or examples/mcp.json copied
#     to .mcp.json in this directory
#   - an INTERESTS.md next to this script describing what you care about
#
# Cron example (07:00 every day):
#   0 7 * * * /path/to/examples/daily-summary.sh >> /path/to/briefings/cron.log 2>&1
set -euo pipefail

cd "$(dirname "$0")"
HOURS="${HOURS:-24}"
OUT="${OUT:-briefings/$(date +%F).md}"
mkdir -p "$(dirname "$OUT")"

claude -p \
  --allowedTools "mcp__inoreader__scan_recent_articles,mcp__inoreader__get_articles,mcp__inoreader__get_server_status,Read,Write" \
  "Use the inoreader MCP server's daily_briefing prompt with hours=${HOURS}.
Read INTERESTS.md for my interest profile and use it to judge relevance.
Write the finished briefing to ${OUT} and reply with only the file path." \
  > /dev/null

echo "briefing written to ${OUT}"
