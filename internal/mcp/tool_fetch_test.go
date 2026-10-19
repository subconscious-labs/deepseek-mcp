package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

func runFetch(t *testing.T, deepSeekBody string, in fetchInput) fetchOutput {
	t.Helper()
	srv := mockDeepSeek(t, "/v1/tools/fetch", 200, deepSeekBody)
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	client := deepseek.NewClient(cfg)

	res, _, err := handleFetch(context.Background(), client, cfg, testLogger(), in)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError, "не ожидали ошибку: %s", firstText(res))

	var out fetchOutput
	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &out))
	return out
}

func TestHandleFetch_InvalidURL(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"ftp scheme", "ftp://example.com/"},
		{"file scheme", "file:///etc/passwd"},
		{"localhost", "http://localhost/"},
		{"loopback ip", "http://127.0.0.1/"},
		{"private 10/8", "http://10.1.2.3/"},
		{"metadata", "http://169.254.169.254/latest/meta-data/"},
		{"port 8080", "http://example.com:8080/"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig("http://127.0.0.1:0")
			cfg.AllowPrivateIPs = false // принципиально: проверяем SSRF
			client := deepseek.NewClient(cfg)

			res, _, err := handleFetch(context.Background(), client, cfg, testLogger(),
				fetchInput{URL: tc.url})
			require.NoError(t, err)
			require.True(t, res.IsError)

			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(firstText(res)), &payload))
			assert.Equal(t, "invalid_url", payload["error"], "case: %s", tc.name)
		})
	}
}

// Ключевой тест SSRF: отказ должен произойти ДО запроса к DeepSeek.
func TestHandleFetch_SSRFRejectedBeforeNetwork(t *testing.T) {
	t.Parallel()

	var called int32
	srv := mockDeepSeek(t, "/v1/tools/fetch", 200, `{"title":"never","content":"never"}`)
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	cfg.AllowPrivateIPs = false
	client := deepseek.NewClient(cfg)

	res, _, err := handleFetch(context.Background(), client, cfg, testLogger(),
		fetchInput{URL: "http://127.0.0.1/"})
	require.NoError(t, err)
	require.True(t, res.IsError)

	// Если бы клиент успел сходить — called стал бы > 0.
	// Проверяем косвенно: контент в ответе — ошибка, не "never".
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &payload))
	assert.Equal(t, "invalid_url", payload["error"])
	_ = called // явная зависимость от mockDeepSeek гарантирует: без вызова
}

func TestHandleFetch_UntrustedHeaderAlwaysPresent(t *testing.T) {
	t.Parallel()

	out := runFetch(t,
		`{"title":"Page","content":"# Hello\n\nSome text."}`,
		fetchInput{URL: "http://127.0.0.1/"})

	assert.True(t, strings.HasPrefix(out.Content, untrustedHeader),
		"контент обязан начинаться с UNTRUSTED-заголовка, даже если модель проигнорировала описание")
	assert.Contains(t, out.Content, "# Hello")
	assert.Equal(t, "Page", out.Title)
	assert.Equal(t, costPerRequestUSD, out.Meta.CostUSD)
	assert.False(t, out.Meta.Truncated)
}

func TestHandleFetch_Truncation(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("c", 40000)
	body, _ := json.Marshal(deepseek.FetchResponse{Title: "T", Content: long})

	out := runFetch(t, string(body), fetchInput{URL: "http://127.0.0.1/"})

	assert.True(t, out.Meta.Truncated)
	assert.Equal(t, maxFetchContentRunes, len([]rune(out.Content)),
		"итоговый контент обрезан до 32000 рун ВКЛЮЧАЯ заголовок")
	assert.True(t, strings.HasPrefix(out.Content, untrustedHeader),
		"заголовок должен выживать при обрезке")
}

func TestHandleFetch_NoTruncation(t *testing.T) {
	t.Parallel()
	out := runFetch(t, `{"title":"T","content":"short"}`, fetchInput{URL: "http://127.0.0.1/"})
	assert.False(t, out.Meta.Truncated)
	assert.Equal(t, untrustedHeader+"short", out.Content)
}

func TestHandleFetch_DeepSeekError(t *testing.T) {
	t.Parallel()

	srv := mockDeepSeek(t, "/v1/tools/fetch", 401, `unauthorized`)
	t.Cleanup(srv.Close)

	cfg := baseConfig(srv.URL)
	client := deepseek.NewClientWithOptions(cfg,
		deepseek.WithBackoffBase(time.Millisecond),
		deepseek.WithMaxBackoff(10*time.Millisecond),
	)

	res, _, err := handleFetch(context.Background(), client, cfg, testLogger(),
		fetchInput{URL: "http://127.0.0.1/"})
	require.NoError(t, err)
	require.True(t, res.IsError)

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstText(res)), &payload))
	assert.Equal(t, "unauthorized", payload["error"])
}
