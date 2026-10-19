package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/subconscious-labs/deepseek-mcp/internal/config"
	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

// testLogger глушит вывод — иначе slog зальёт stderr.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// baseConfig — минимальный Config для handler-тестов. QPS огромный,
// AllowPrivateIPs=true — чтобы 127.0.0.1 проходил без DNS.
func baseConfig(baseURL string) config.Config {
	return config.Config{
		APIBase:            baseURL,
		APIKey:             "sk-test",
		SearchDefaultCount: 5,
		SearchMaxCount:     50,
		SearchTimeoutSec:   30,
		FetchTimeoutSec:    60,
		SearchQPS:          1000,
		FetchQPS:           1000,
		MaxResponseBytes:   102400,
		AllowPrivateIPs:    true,
	}
}

// mockDeepSeek — httptest, отвечающий фиксированным (status, body)
// на оба endpoint'а. Путь проверяем, чтобы ловить опечатки в URL клиента.
func mockDeepSeek(t *testing.T, wantPath string, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, wantPath, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// runSearch вызывает handleSearch и возвращает распарсенный output.
// Для error-кейсов использовать runSearchErr.
func runSearch(t *testing.T, body string, in searchInput) searchOutput {
	t.Helper()
	srv := mockDeepSeek(t, "/v1/tools/search", http.StatusOK, body)
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	client := deepseek.NewClient(cfg)

	res, _, err := handleSearch(context.Background(), client, cfg, testLogger(), in)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError, "не ожидали ошибку: %s", firstText(res))

	var out searchOutput
	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &out))
	return out
}

func runSearchErr(t *testing.T, in searchInput) (code string, payload map[string]any) {
	t.Helper()
	// Мок не нужен: валидация падает до сети. Но клиент требует APIBase.
	cfg := baseConfig("http://127.0.0.1:0")
	client := deepseek.NewClient(cfg)

	res, _, err := handleSearch(context.Background(), client, cfg, testLogger(), in)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, res.IsError)

	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &payload))
	code, _ = payload["error"].(string)
	return code, payload
}

func firstText(res *sdkmcp.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*sdkmcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func intPtr(i int) *int { return &i }

// --- Валидация входа ---

func TestHandleSearch_InvalidInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   searchInput
		code string
	}{
		{"empty query", searchInput{Query: ""}, "invalid_query"},
		{"whitespace query", searchInput{Query: "   "}, "invalid_query"},
		{"query too long", searchInput{Query: strings.Repeat("a", 129)}, "invalid_query"},
		{"count too low", searchInput{Query: "q", Count: intPtr(0)}, "invalid_count"},
		{"count too high", searchInput{Query: "q", Count: intPtr(51)}, "invalid_count"},
		{"count negative", searchInput{Query: "q", Count: intPtr(-1)}, "invalid_count"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			code, _ := runSearchErr(t, tc.in)
			assert.Equal(t, tc.code, code)
		})
	}
}

func TestHandleSearch_QueryAtLimit(t *testing.T) {
	t.Parallel()
	// Ровно 128 — валидно (граница maxQueryRunes).
	out := runSearch(t, `{"results":[]}`, searchInput{Query: strings.Repeat("a", 128)})
	assert.Equal(t, strings.Repeat("a", 128), out.Query)
}

// --- Happy path ---

func TestHandleSearch_HappyPath(t *testing.T) {
	t.Parallel()

	body := `{"results":[
		{"title":"A","url":"https://a.example/","snippet":"sa","summary":"sm"},
		{"title":"B","url":"https://b.example/","snippet":"sb"}
	],"request_id":"req-1"}`

	out := runSearch(t, body, searchInput{Query: "golang mcp", Count: intPtr(2)})

	assert.Equal(t, "golang mcp", out.Query)
	assert.Equal(t, 2, out.Count)
	require.Len(t, out.Results, 2)
	assert.Equal(t, "A", out.Results[0].Title)
	assert.Equal(t, "sm", out.Results[0].Summary)
	assert.Equal(t, "sb", out.Results[1].Snippet)
	assert.Equal(t, costPerRequestUSD, out.Meta.CostUSD)
	assert.Equal(t, "req-1", out.Meta.RequestID)
}

func TestHandleSearch_DefaultCount(t *testing.T) {
	t.Parallel()
	out := runSearch(t, `{"results":[]}`, searchInput{Query: "q"})
	assert.Equal(t, 5, out.Count, "должен примениться SearchDefaultCount")
}

// --- Truncation (§6.3) ---

func TestHandleSearch_Truncation(t *testing.T) {
	t.Parallel()

	longSnippet := strings.Repeat("s", 800)
	longSummary := strings.Repeat("m", 2500)
	longContent := strings.Repeat("c", 9000)

	body, _ := json.Marshal(deepseek.SearchResponse{
		Results: []deepseek.SearchResult{{
			Title: "T", URL: "https://x.example/",
			Snippet: longSnippet, Summary: longSummary, Content: longContent,
		}},
	})

	out := runSearch(t, string(body), searchInput{Query: "q", NeedContent: true})
	require.Len(t, out.Results, 1)

	r := out.Results[0]
	assert.Len(t, []rune(r.Snippet), 500)
	assert.Len(t, []rune(r.Summary), 2000)
	assert.Len(t, []rune(r.Content), 8000)
	assert.True(t, r.Truncated, "_truncated должен стоять, если что-то обрезано")
}

func TestHandleSearch_NoTruncation(t *testing.T) {
	t.Parallel()
	out := runSearch(t, `{"results":[{"title":"T","url":"https://x.example/","snippet":"short"}]}`,
		searchInput{Query: "q"})
	require.Len(t, out.Results, 1)
	assert.False(t, out.Results[0].Truncated)
	assert.Empty(t, out.Results[0].Content)
}

// --- _results_dropped ---

func TestHandleSearch_ResultsDropped(t *testing.T) {
	t.Parallel()

	// 10 результатов по 500 символов snippet ≈ 6 KB JSON. MaxResponseBytes=2000 → хвост отвалится.
	results := make([]map[string]any, 10)
	for i := range results {
		results[i] = map[string]any{
			"title":   strings.Repeat("t", 20),
			"url":     "https://x.example/",
			"snippet": strings.Repeat("s", 500),
		}
	}
	body, _ := json.Marshal(map[string]any{"results": results})

	srv := mockDeepSeek(t, "/v1/tools/search", http.StatusOK, string(body))
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	cfg.MaxResponseBytes = 2000
	client := deepseek.NewClient(cfg)

	res, _, err := handleSearch(context.Background(), client, cfg, testLogger(),
		searchInput{Query: "q", Count: intPtr(10)})
	require.NoError(t, err)
	require.False(t, res.IsError)

	var out searchOutput
	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &out))

	assert.Greater(t, out.ResultsDropped, 0)
	assert.Less(t, len(out.Results), 10)
	assert.LessOrEqual(t, len(firstText(res)), cfg.MaxResponseBytes)
}

// --- Фильтрация результатов с приватными URL ---

func TestHandleSearch_DropsPrivateURLResults(t *testing.T) {
	t.Parallel()

	body := `{"results":[
		{"title":"pub","url":"https://example.com/","snippet":"a"},
		{"title":"priv","url":"http://127.0.0.1/","snippet":"b"},
		{"title":"meta","url":"http://169.254.169.254/","snippet":"c"}
	]}`

	out := runSearch(t, body, searchInput{Query: "q"})
	require.Len(t, out.Results, 1)
	assert.Equal(t, "pub", out.Results[0].Title)
}

// --- Маппинг ошибок DeepSeek ---

func TestHandleSearch_DeepSeekErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		status   int
		body     string
		wantCode string
	}{
		{"401", 401, `unauthorized`, "unauthorized"},
		{"403", 403, `forbidden`, "forbidden"},
		{"500", 500, `boom`, "server_error"},
		{"400", 400, `bad query`, "invalid_query"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := mockDeepSeek(t, "/v1/tools/search", tc.status, tc.body)
			t.Cleanup(srv.Close)

			cfg := baseConfig(srv.URL)
			client := deepseek.NewClientWithOptions(cfg,
				deepseek.WithBackoffBase(time.Millisecond),
				deepseek.WithMaxBackoff(10*time.Millisecond),
			)

			res, _, err := handleSearch(context.Background(), client, cfg, testLogger(),
				searchInput{Query: "q"})
			require.NoError(t, err)
			require.True(t, res.IsError)

			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(firstText(res)), &payload))
			assert.Equal(t, tc.wantCode, payload["error"])
		})
	}
}

func TestHandleSearch_RateLimited_IncludesRetryAfter(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	client := deepseek.NewClientWithOptions(cfg,
		deepseek.WithBackoffBase(time.Millisecond),
		deepseek.WithMaxBackoff(10*time.Millisecond),
	)

	res, _, err := handleSearch(context.Background(), client, cfg, testLogger(),
		searchInput{Query: "q"})
	require.NoError(t, err)
	require.True(t, res.IsError)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &payload))
	assert.Equal(t, "rate_limited", payload["error"])
	assert.EqualValues(t, 7, payload["retry_after_sec"])
}
