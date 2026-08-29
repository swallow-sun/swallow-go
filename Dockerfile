# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder

WORKDIR /src

# sqlite3 driver uses CGO, so keep CGO enabled and build on Debian.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/swallow-go ./cmd/server

FROM debian:bookworm-slim AS runtime

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --system --gid 10001 swallow \
    && useradd --system --uid 10001 --gid swallow --home-dir /app swallow

WORKDIR /app

COPY --from=builder /out/swallow-go /app/swallow-go
COPY config.toml /app/config.toml
COPY prompts /app/prompts
COPY script/migrations /app/script/migrations

RUN mkdir -p /app/data /app/logs \
    && chown -R swallow:swallow /app

USER swallow

EXPOSE 8888 9881 9100 6060

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD curl --fail --silent --show-error http://127.0.0.1:8888/ping || exit 1

ENTRYPOINT ["/app/swallow-go"]
