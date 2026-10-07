# Security

## Reporting

Please report vulnerabilities privately through GitHub's
"Report a vulnerability" form on this repository rather than opening a public
issue.

## Design notes

- **Secrets never touch the repo.** Credentials come only from environment
  variables or `*_FILE` paths (Docker/Kubernetes secrets). `.env`,
  `tokens.json`, and `config.json` are gitignored, and CI runs gitleaks.
- **Tokens on disk are `0600`** and written atomically (temp file + rename).
  Access tokens are refreshed five minutes before expiry; a `401` triggers one
  forced refresh and retry. Tokens are never logged.
- **Stdout is reserved for the MCP stdio transport.** All logging goes to
  stderr.
- **HTTP transport is opt-in** and refuses to start on a non-loopback address
  unless `INOREADER_MCP_HTTP_TOKEN` (at least 16 chars) is set. Requests must
  carry `Authorization: Bearer <token>`; comparison is constant-time. Browsers
  cannot attach that header cross-origin, which is what defeats DNS rebinding;
  for a tokenless loopback listener, the go-sdk additionally rejects requests
  whose `Host` is not localhost. Put TLS in front of it if you expose it
  beyond the host.
- **Read-only mode** (`INOREADER_MCP_READ_ONLY=true` or `serve --read-only`)
  removes every tool that modifies the account. Destructive tools
  (`mark_all_as_read`, `unsubscribe_feed`, `delete_folder_or_tag`) also
  require `confirm: true`.
- **Least-privilege OAuth.** Register the Inoreader app as "Read only" if you
  never want writes; Inoreader enforces the app's permission level on every
  request regardless of the scope string stored with the token. Pair that
  with read-only mode on the server for defense in depth.
- **Container hardening.** Static binary on `distroless/static:nonroot`
  (no shell, uid 65532), `read_only` root filesystem, all capabilities
  dropped, `no-new-privileges`, memory and pid limits in `docker-compose.yml`.
- **Bounded inputs.** API response bodies are capped at 32 MiB, HTTP request
  bodies at the SDK default (4 MiB), header size at 64 KiB, and pagination at
  `max_pages`/`max_items`.
- **Container.** Published ports in `docker-compose.yml` bind to
  `127.0.0.1` only. The token volume is the only writable path.
- **Prompt-injection surface.** Article content from feeds is untrusted. The
  server strips HTML and truncates summaries but cannot sanitize meaning;
  treat tool output as data, and prefer read-only mode for unattended
  automation that only needs to read.
