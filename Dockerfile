# syntax=docker/dockerfile:1.7

# ---- build stage -----------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/inoreader-mcp ./cmd/inoreader-mcp

# ---- runtime stage ---------------------------------------------------------
# Distroless static: no shell, no package manager, CA certs included,
# runs as the unprivileged "nonroot" user (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.title="inoreader-mcp" \
      org.opencontainers.image.description="MCP server for the Inoreader API" \
      org.opencontainers.image.source="https://github.com/praetoriansentry/go-inoreader-mcp" \
      org.opencontainers.image.licenses="MIT"

COPY --from=build /out/inoreader-mcp /inoreader-mcp

# Token persistence lives here; mount a volume so refreshed tokens survive.
ENV INOREADER_TOKEN_FILE=/data/tokens.json
VOLUME ["/data"]

# Only used by `serve --http` and `auth login`; harmless otherwise.
EXPOSE 8765 8080

USER nonroot:nonroot
ENTRYPOINT ["/inoreader-mcp"]
CMD ["serve"]
