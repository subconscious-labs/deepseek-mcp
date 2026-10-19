# Configuration Reference

All settings are configurable via environment variables and CLI flags. **Flags override env vars**; env vars override defaults.

## Environment variables

| Variable | Default | Description |
|---|---|---|
| `DEEPSEEK_API_KEY` | — | **Required for real runs.** Bearer token for DeepSeek tools API. Not required for `--version`, `--check`, `--print-mcp-config`. |
| `DEEPSEEK_API_BASE` | `https://api.deepseek.com` | Base URL for the DeepSeek API. Useful for local mocks. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` | `json` or `text` |
| `LOG_REDACT_QUERIES` | `false` | If true, log SHA-256 hash + length of query/URL instead of the raw value. Use for private installs. |
| `SEARCH_DEFAULT_COUNT` | `5` | Default `count` when the model omits it. Must be between 1 and `SEARCH_MAX_COUNT`. |
| `SEARCH_MAX_COUNT` | `50` | Hard upper bound on `count`. |
| `SEARCH_TIMEOUT_SEC` | `30` | Per-request context deadline for `deepseek_search`. |
| `FETCH_TIMEOUT_SEC` | `60` | Per-request context deadline for `deepseek_fetch`. |
| `SEARCH_QPS` | `4` | Local rate limiter for search. DeepSeek allows 5; headroom left for retries. |
| `FETCH_QPS` | `4` | Local rate limiter for fetch. |
| `ALLOW_PRIVATE_IPS` | `false` | Disables SSRF protection. Local testing only. |
| `MAX_RESPONSE_BYTES` | `102400` | Hard cap on tool response size (bytes). Responses larger than this are truncated with `_results_dropped` / `_truncated` markers. |

### Implemented in v0.2

| Variable | Default | Description |
|---|---|---|
| `MCP_TRANSPORT` | `stdio` | `stdio` or `http`. |
| `MCP_HTTP_ADDR` | `:8765` | Listen address for HTTP transport. |
| `MCP_HTTP_TOKEN` | — | Bearer token for HTTP transport. Required unless `MCP_HTTP_ALLOW_INSECURE=true`. |
| `MCP_HTTP_ALLOW_INSECURE` | `false` | Allow HTTP transport without a token. Logs a warning. |
| `DAILY_REQUEST_LIMIT` | `0` | Hard cap on successful requests per UTC day (search + fetch combined). 0 = unlimited. Exceeding the limit returns `daily_limit_exceeded` with `retry_after_sec` until UTC midnight. |
| `STATE_DIR` | `$XDG_STATE_HOME/deepseek-mcp` | Where `counter.json` and `counter.archive.jsonl` live. Must be writable. |

Setting them today is harmless: `--check` will echo them back, but they have no runtime effect until v0.2.

### Counter state

`deepseek-mcp` writes `$STATE_DIR/counter.json`:

```json
{
  "date": "2026-10-19",
  "search_requests": 42,
  "fetch_requests": 18,
  "estimated_cost_usd": 0.045
}
```

At UTC midnight the file is rotated: the old snapshot is appended to
`counter.archive.jsonl`, and `counter.json` starts fresh. If the file is
corrupted (kill -9 mid-write), the server silently starts from zero — the
service does not go down because of one bad file.

`--check` prints the current snapshot without making network calls.

## CLI flags

Every env var has a kebab-case flag counterpart:

| Flag | Env var |
|---|---|
| `--deepseek-api-key` | `DEEPSEEK_API_KEY` |
| `--deepseek-api-base` | `DEEPSEEK_API_BASE` |
| `--log-level` | `LOG_LEVEL` |
| `--log-format` | `LOG_FORMAT` |
| `--redact-queries` | `LOG_REDACT_QUERIES` |
| `--search-default-count` | `SEARCH_DEFAULT_COUNT` |
| `--search-max-count` | `SEARCH_MAX_COUNT` |
| `--search-timeout-sec` | `SEARCH_TIMEOUT_SEC` |
| `--fetch-timeout-sec` | `FETCH_TIMEOUT_SEC` |
| `--search-qps` | `SEARCH_QPS` |
| `--fetch-qps` | `FETCH_QPS` |
| `--allow-private` | `ALLOW_PRIVATE_IPS` |
| `--max-response-bytes` | `MAX_RESPONSE_BYTES` |
| `--transport` | `MCP_TRANSPORT` (reserved) |
| `--addr` | `MCP_HTTP_ADDR` (reserved) |
| `--http-token` | `MCP_HTTP_TOKEN` (reserved) |
| `--http-allow-insecure` | `MCP_HTTP_ALLOW_INSECURE` (reserved) |
| `--daily-request-limit` | `DAILY_REQUEST_LIMIT` (reserved) |
| `--state-dir` | `STATE_DIR` (reserved) |

Informational flags:

| Flag | Behaviour |
|---|---|
| `--version` | Print version, commit, build time, exit. No API key required. |
| `--check` | Validate config, exit. No API key required — warns if absent. |
| `--print-mcp-config` | Print JSON fragment for MCP client configs. No API key required. |

## Precedence

```
CLI flag > environment variable > built-in default
```

Example: `SEARCH_QPS=2 deepseek-mcp --search-qps 8` runs with `8`.

## Examples

### `.env` for local development

```sh
DEEPSEEK_API_KEY=sk-...
LOG_LEVEL=debug
LOG_FORMAT=text
SEARCH_QPS=4
FETCH_QPS=4
```

### Claude Desktop

Use `deepseek-mcp --print-mcp-config` — it fills in the absolute binary path and the API key from env:

```sh
deepseek-mcp --print-mcp-config > /tmp/entry.json
```

Then merge the `mcpServers` object into `~/Library/Application Support/Claude/claude_desktop_config.json` on macOS, or `%APPDATA%\Claude\claude_desktop_config.json` on Windows.

### subconscious-core

Via stdio:

```sh
# /etc/subconscious/subconscious.env
SUBCONSCIOUS_MCP_STDIO_DEEPSEEK=/usr/local/bin/deepseek-mcp
```

`deepseek-mcp` inherits `DEEPSEEK_API_KEY` from `chatd`'s environment.

Via HTTP (v0.2+):

```sh
SUBCONSCIOUS_MCP_HTTP_DEEPSEEK_URL=http://127.0.0.1:8765/mcp
SUBCONSCIOUS_MCP_HTTP_DEEPSEEK_TOKEN=<bearer>
```

### Private install with query redaction

```sh
DEEPSEEK_API_KEY=sk-...
LOG_REDACT_QUERIES=true
LOG_LEVEL=info
```

Every log line will contain `query_hash` and `query_len` instead of the query text.

## Validation

`deepseek-mcp --check` prints the effective config and exits non-zero on:

- `SEARCH_DEFAULT_COUNT < 1` or `> SEARCH_MAX_COUNT`
- `MCP_TRANSPORT` not in `{stdio, http}`
- `DEEPSEEK_API_KEY` empty **and** not in an informational mode

It does not make network calls. To test the API key itself, use `make e2e`.
