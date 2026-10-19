package e2e_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/subconscious-labs/deepseek-mcp/internal/config"
	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

// skipUnlessE2E пропускает тест, если не выставлены DEEPSEEK_E2E=1 и ключ.
// Эти тесты стоят денег (~$0.00075 за запрос) и не должны идти в CI.
func skipUnlessE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("DEEPSEEK_E2E") != "1" {
		t.Skip("set DEEPSEEK_E2E=1 to run live tests")
	}
	if os.Getenv("DEEPSEEK_API_KEY") == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
}

func liveClient() *deepseek.Client {
	cfg := config.Config{
		APIKey:           os.Getenv("DEEPSEEK_API_KEY"),
		APIBase:          "https://api.deepseek.com",
		SearchQPS:        1,
		FetchQPS:         1,
		SearchTimeoutSec: 30,
		FetchTimeoutSec:  60,
	}
	return deepseek.NewClient(cfg)
}

// TestLive_Search бьёт по реальному DeepSeek Search. Один запрос.
func TestLive_Search(t *testing.T) {
	skipUnlessE2E(t)

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	client := liveClient()
	resp, err := client.Search(ctx, deepseek.SearchRequest{
		Query: "model context protocol go sdk",
		Count: 2,
	})
	require.NoError(t, err, "live search failed — проверь ключ и сеть")
	require.NotEmpty(t, resp.Results, "DeepSeek вернул пустой results")

	for i, r := range resp.Results {
		assert.NotEmpty(t, r.Title, "result[%d].title", i)
		assert.NotEmpty(t, r.URL, "result[%d].url", i)
		assert.NotEmpty(t, r.Snippet, "result[%d].snippet", i)
		t.Logf("result[%d]: %s — %s", i, r.Title, r.URL)
	}
}

// TestLive_Fetch бьёт по реальному DeepSeek Fetch. Один запрос.
// Не привязываемся к тексту страницы: контент любого URL может меняться.
// example.com сам просит не использовать его для тестов, поэтому go.dev.
func TestLive_Fetch(t *testing.T) {
	skipUnlessE2E(t)

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()

	client := liveClient()
	resp, err := client.Fetch(ctx, deepseek.FetchRequest{URL: "https://go.dev/"})
	require.NoError(t, err, "live fetch failed — проверь ключ и сеть")
	assert.NotEmpty(t, resp.Title)
	assert.NotEmpty(t, resp.Content)
	assert.Greater(t, len(resp.Content), 100, "content is suspiciously short for a real page")
}

// TestLive_SearchThenFetch — сквозной сценарий, который делает subconscious-core:
// search → выбрать первый URL → fetch. Два запроса.
func TestLive_SearchThenFetch(t *testing.T) {
	skipUnlessE2E(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	client := liveClient()

	searchResp, err := client.Search(ctx, deepseek.SearchRequest{
		Query: "golang official website",
		Count: 1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, searchResp.Results)

	target := searchResp.Results[0].URL
	t.Logf("search picked: %s", target)

	fetchResp, err := client.Fetch(ctx, deepseek.FetchRequest{URL: target})
	require.NoError(t, err, "fetch выбранного URL не удался")
	assert.NotEmpty(t, fetchResp.Title)
	assert.NotEmpty(t, fetchResp.Content)
}
