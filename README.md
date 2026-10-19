# deepseek-mcp

MCP server exposing [DeepSeek Search and Fetch](https://api.deepseek.com) as tools for any MCP client — Claude Desktop, subconscious-core, Continue, Zed, Codex, or your own.

Single static binary. No database, no files on disk, no external services.

## Features

- Two tools: `deepseek_search` and `deepseek_fetch`
- stdio transport (HTTP in v0.2)
- SSRF protection: private IPs, loopback, link-local, metadata endpoints, CGNAT are rejected
- Retry with exponential backoff + jitter on 429/5xx/network; `Retry-After` honoured
- Prompt-injection mitigation: untrusted-content header on every fetched page, structured JSON results instead of free text
- Response truncation with explicit `_truncated` / `_results_dropped` markers
- Structured logs (JSON or text), query redaction via `--redact-queries`
- Cost visible in every response: `_meta.cost_usd`

## Install

**From source:**

```sh
go install github.com/subconscious-labs/deepseek-mcp/cmd/deepseek-mcp@latest
```

**From repo:**

```sh
git clone https://github.com/subconscious-labs/deepseek-mcp
cd deepseek-mcp
make build          # → bin/deepseek-mcp
```

Requires Go 1.22+.

## Quick start

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

For subconscious-core, either point it at the binary over stdio:

```sh
SUBCONSCIOUS_MCP_STDIO_DEEPSEEK=/usr/local/bin/deepseek-mcp
```

or inherit `DEEPSEEK_API_KEY` from `chatd`'s environment.

## Tools

| Tool | Purpose | Cost |
|---|---|---|
| `deepseek_search` | Web search, up to 50 results with title, url, snippet, optional summary and content | $0.00075 / request |
| `deepseek_fetch` | Fetch one URL, return title + Markdown content | $0.00075 / request |

`deepseek_fetch` is meant to be called **after** `deepseek_search` on URLs that look relevant — not on every result. See [docs/mcp.md](docs/mcp.md) for full schemas and error codes.

## Configuration

Everything is configurable via environment variables and CLI flags; **flags override env**. See [docs/config.md](docs/config.md) for the full reference.

Minimum:

```sh
DEEPSEEK_API_KEY=sk-...
```

Common:

```sh
LOG_LEVEL=info              # debug | info | warn | error
LOG_FORMAT=json             # json | text
SEARCH_QPS=4                # DeepSeek allows 5; we keep 1 QPS headroom
FETCH_QPS=4
ALLOW_PRIVATE_IPS=false     # never enable in production
```

## Security

- **SSRF**: URLs are validated before they reach DeepSeek. Loopback, RFC1918, link-local, CGNAT, multicast, and the AWS metadata endpoint are blocked. DNS is resolved and every returned IP is checked. `--allow-private` disables this for local testing.
- **Prompt injection**: three defense layers — tool description warning, untrusted-content header inside every fetched page, structured JSON results. Mitigation, not a guarantee. See §6 of the spec.
- **Logs**: API key is never logged. Queries and URLs are logged by default; `--redact-queries` logs SHA-256 hashes instead.
- **Known limitation**: DNS rebinding on DeepSeek's side is not preventable by us. See `docs/mcp.md`.

## Development

```sh
make build          # static binary with CGO_ENABLED=0
make test           # unit + integration + conformance
make test-race      # same, with -race
make integration    # test/integration only
make conformance    # wire-level MCP protocol tests
make e2e            # live API, needs DEEPSEEK_E2E=1 and DEEPSEEK_API_KEY
make cover-check    # fail if coverage < 50%
```

Test layout:

- `internal/*_test.go` — unit tests for client, handlers, SSRF
- `test/integration/` — `httptest` mock of DeepSeek API
- `test/conformance/` — real MCP wire protocol over stdio
- `test/e2e/` — real DeepSeek API, gated by `DEEPSEEK_E2E=1`, costs money

## Status

- **v0.1** (current): stdio transport, both tools, SSRF, retry, truncation, structured logs
- **v0.2** (next): HTTP transport with bearer auth, daily request limit, cost counter
- **v0.3**: LRU cache, Prometheus metrics
- **v1.0**: stable MCP interface, signed releases

## License

MIT
