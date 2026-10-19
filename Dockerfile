# syntax=docker/dockerfile:1.7

# ── Builder ─────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w \
      -X main.version=${VERSION} \
      -X main.commit=${COMMIT} \
      -X main.buildTime=${BUILD_TIME}" \
    -o /out/deepseek-mcp ./cmd/deepseek-mcp

# ── Runtime ─────────────────────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 65532 nonroot \
    && mkdir -p /var/lib/deepseek-mcp \
    && chown nonroot:nonroot /var/lib/deepseek-mcp

COPY --from=builder /out/deepseek-mcp /usr/local/bin/deepseek-mcp

USER nonroot:nonroot
ENV STATE_DIR=/var/lib/deepseek-mcp

EXPOSE 8765
VOLUME /var/lib/deepseek-mcp

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8765/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/deepseek-mcp"]
CMD ["--transport", "http", "--addr", ":8765"]
