![release](https://github.com/subconscious-labs/deepseek-mcp/actions/workflows/release.yml/badge.svg)

# deepseek-mcp

MCP server exposing [DeepSeek Search and Fetch](https://api.deepseek.com) as tools for any MCP client — Claude Desktop, subconscious-core, Continue, Zed, Codex, or your own.

Single static binary. No database, no external services.

## Features

- Two tools: `deepseek_search` and `deepseek_fetch`
- **stdio** and **streamable HTTP** transports (HTTP with bearer auth)
- SSRF protection: private IPs, loopback, link-local, metadata endpoints, CGNAT are rejected
- Retry with exponential backoff + jitter on 429/5xx/network; `Retry-After` honoured
- Prompt-injection mitigation: untrusted-content header on every fetched page, structured JSON results instead of free text
- Response truncation with explicit `_truncated` / `_results_dropped` markers
- Daily request cap with cost tracking in `$STATE_DIR/counter.json`
- Structured logs (JSON or text), query redaction via `--redact-queries`
- Cost visible in every response: `_meta.cost_usd`

## Install

**From source:**

```sh
go install github.com/subconscious-labs/deepseek-mcp/cmd/deepseek-mcp@latest
```

**From a release:**

```sh
# linux/amd64 — swap in your os/arch
curl -L https://github.com/subconscious-labs/deepseek-mcp/releases/latest/download/deepseek-mcp_latest_linux_amd64.tar.gz | tar xz
sudo install deepseek-mcp /usr/local/bin/
```

**Docker:**

```sh
docker run -d --name deepseek-mcp \
  -p 127.0.0.1:8765:8765 \
  -e DEEPSEEK_API_KEY=sk-... \
  -e MCP_HTTP_TOKEN=$(openssl rand -hex 32) \
  -v deepseek-state:/var/lib/deepseek-mcp \
  ghcr.io/subconscious-labs/deepseek-mcp:latest
```

A ready-to-use `docker compose` example with secrets and volume is in
[`docs/examples/`](docs/examples/).

## Quick start (stdio)

```sh
export DEEPSEEK_API_KEY=sk-...
deepseek-mcp --check              # validate config; does not spend money
deepseek-mcp                      # run stdio MCP server
```

`--print-mcp-config` prints a ready-to-paste JSON fragment for Claude Desktop:

```sh
deepseek-mcp --print-mcp-config
```

```json
{
  "mcpServers": {
    "deepseek": {
      "command": "/usr/local/bin/deepseek-mcp",
      "env": {
        "DEEPSEEK_API_KEY": "sk-..."
      }
    }
  }
}
```

## Quick start (HTTP)

```sh
export DEEPSEEK_API_KEY=sk-...
export MCP_HTTP_TOKEN=$(openssl rand -hex 32)

deepseek-mcp --transport http --addr 127.0.0.1:8765 --http-token "$MCP_HTTP_TOKEN"
```

Endpoint: `POST http://127.0.0.1:8765/mcp`, header `Authorization: Bearer $MCP_HTTP_TOKEN`.
Liveness: `GET /healthz` → `200 ok` without auth.

The server refuses to start with HTTP transport unless a token is set, or
`--http-allow-insecure` is explicitly passed (not recommended, logs a warning).
**TLS is not terminated by this binary** — use a reverse proxy for non-loopback
deployments.

For subconscious-core over HTTP:

```sh
SUBCONSCIOUS_MCP_HTTP_DEEPSEEK_URL=http://127.0.0.1:8765/mcp
SUBCONSCIOUS_MCP_HTTP_DEEPSEEK_TOKEN=$MCP_HTTP_TOKEN
```

## Tools

| Tool | Purpose | Cost |
|---|---|---|
| `deepseek_search` | Web search, up to 50 results with title, url, snippet, optional summary and content | $0.00075 / request |
| `deepseek_fetch` | Fetch one URL, return title + Markdown content | $0.00075 / request |

Call `deepseek_fetch` **after** `deepseek_search` on URLs that look relevant —
not on every result. See [docs/mcp.md](docs/mcp.md) for full schemas and error
codes.

## Configuration

Everything is configurable via environment variables and CLI flags; **flags
override env**. See [docs/config.md](docs/config.md) for the full reference.

Minimum:

```sh
DEEPSEEK_API_KEY=sk-...
```

Common:

```sh
LOG_LEVEL=info                    # debug | info | warn | error
LOG_FORMAT=json                   # json | text
SEARCH_QPS=4                      # DeepSeek allows 5; keep 1 QPS headroom
FETCH_QPS=4
DAILY_REQUEST_LIMIT=200           # hard cap per UTC day; 0 = unlimited
STATE_DIR=/var/lib/deepseek-mcp   # where counter.json lives
ALLOW_PRIVATE_IPS=false           # never enable in production
```

## Security

See [SECURITY.md](SECURITY.md) for the full threat model. Summary:

- **SSRF**: URLs validated before they reach DeepSeek. Private IPs, loopback,
  link-local, CGNAT, metadata endpoints rejected. DNS resolved for fetch,
  every IP checked.
- **Prompt injection**: three defense layers — tool description, untrusted
  header inside content, structured JSON. Mitigation, not a guarantee.
- **Secrets**: API key and HTTP token are never logged. Queries redacted with
  `--redact-queries`.
- **HTTP transport**: bearer auth required; refuses to start without token
  unless `--http-allow-insecure`.
- **Daily cap**: `DAILY_REQUEST_LIMIT` bounds worst-case spend.
- **Known limitation**: DNS rebinding on DeepSeek's side is not preventable by
  us. Documented, not hidden.

## Development

```sh
make build          # static binary with CGO_ENABLED=0
make test           # unit + integration + conformance
make test-race      # same, with -race
make integration    # test/integration only
make conformance    # wire-level MCP protocol tests
make e2e            # live API, needs DEEPSEEK_E2E=1 and DEEPSEEK_API_KEY
make cover-check    # fail if coverage < 50%
make docker         # build local image
```

Test layout:

- `internal/*_test.go` — unit tests for client, handlers, SSRF, counter
- `test/integration/` — `httptest` mock of DeepSeek API
- `test/conformance/` — real MCP wire protocol over stdio
- `test/e2e/` — real DeepSeek API, gated by `DEEPSEEK_E2E=1`, costs money

## Status

- **v0.1**: stdio transport, both tools, SSRF, retry, truncation, logs
- **v0.2** (current): HTTP transport with bearer auth, daily request cap,
  cost counter, Docker image, `SECURITY.md`
- **v0.3**: LRU cache, Prometheus metrics
- **v1.0**: stable MCP interface, signed releases

## License

MIT
