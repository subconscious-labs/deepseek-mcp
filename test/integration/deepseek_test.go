package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/subconscious-labs/deepseek-mcp/internal/config"
	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

func testConfig(baseURL string) config.Config {
	return config.Config{
		APIBase:   baseURL,
		APIKey:    "sk-integration-key",
		SearchQPS: 1000,
		FetchQPS:  1000,
	}
}

func fastClient(t *testing.T, baseURL string) *deepseek.Client {
	t.Helper()
	return deepseek.NewClientWithOptions(testConfig(baseURL),
		deepseek.WithBackoffBase(time.Millisecond),
		deepseek.WithMaxBackoff(10*time.Millisecond),
	)
}

// --- Search: happy path ---

func TestIntegration_Search_HappyPath(t *testing.T) {
	var (
		gotMethod, gotPath, gotAuth string
		gotBody                     map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"results":[
				{"title":"T1","url":"https://e1.example/","snippet":"s1","summary":"m1"},
				{"title":"T2","url":"https://e2.example/","snippet":"s2"}
			],
			"request_id":"req-1"
		}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	resp, err := c.Search(context.Background(), deepseek.SearchRequest{
		Query:       "golang mcp",
		Count:       2,
		NeedContent: false,
	})
	require.NoError(t, err)
	require.Len(t, resp.Results, 2)
	assert.Equal(t, "T1", resp.Results[0].Title)
	assert.Equal(t, "https://e1.example/", resp.Results[0].URL)
	assert.Equal(t, "m1", resp.Results[0].Summary)
	assert.Equal(t, "req-1", resp.RequestID)

	// Wire-level
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/v1/tools/search", gotPath)
	assert.Equal(t, "Bearer sk-integration-key", gotAuth)
	assert.Equal(t, "golang mcp", gotBody["query"])
	assert.EqualValues(t, 2, gotBody["count"])
	// need_content=false — omitempty, поле не должно уйти в тело
	_, hasNeedContent := gotBody["need_content"]
	assert.False(t, hasNeedContent)
}

// --- Search: retry on 429 ---

func TestIntegration_Search_Retry429ThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"T","url":"https://e.example/","snippet":"s"}]}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	resp, err := c.Search(context.Background(), deepseek.SearchRequest{Query: "q"})
	require.NoError(t, err)
	require.Len(t, resp.Results, 1)
	assert.EqualValues(t, 3, atomic.LoadInt32(&attempts))
}

// --- Search: 5xx × 3 → fail ---

func TestIntegration_Search_ServerErrorsExhausted(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	_, err := c.Search(context.Background(), deepseek.SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *deepseek.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, deepseek.ErrorServer, apiErr.Kind)
	assert.EqualValues(t, 3, atomic.LoadInt32(&attempts))
}

// --- Search: 401 без retry ---

func TestIntegration_Search_NoRetryOn401(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	_, err := c.Search(context.Background(), deepseek.SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *deepseek.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, deepseek.ErrorUnauthorized, apiErr.Kind)
	assert.EqualValues(t, 1, atomic.LoadInt32(&attempts))
}

// --- Search: битый JSON ---

func TestIntegration_Search_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	_, err := c.Search(context.Background(), deepseek.SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *deepseek.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, deepseek.ErrorInvalidResponse, apiErr.Kind)
}

// --- Search: пустой results — валидный ответ ---

func TestIntegration_Search_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	resp, err := c.Search(context.Background(), deepseek.SearchRequest{Query: "q"})
	require.NoError(t, err)
	assert.Empty(t, resp.Results)
}

// --- Fetch: happy path ---

func TestIntegration_Fetch_HappyPath(t *testing.T) {
	var (
		gotPath, gotAuth string
		gotBody          map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Page","content":"# Hello","request_id":"req-f"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	resp, err := c.Fetch(context.Background(), deepseek.FetchRequest{URL: "https://example.com/x"})
	require.NoError(t, err)
	assert.Equal(t, "Page", resp.Title)
	assert.Equal(t, "# Hello", resp.Content)
	assert.Equal(t, "req-f", resp.RequestID)

	// Wire-level
	assert.Equal(t, "/v1/tools/fetch", gotPath)
	assert.Equal(t, "Bearer sk-integration-key", gotAuth)
	assert.Equal(t, "https://example.com/x", gotBody["url"])
}

// --- Fetch: retry on 503 ---

func TestIntegration_Fetch_Retry503ThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"T","content":"c"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	_, err := c.Fetch(context.Background(), deepseek.FetchRequest{URL: "https://example.com"})
	require.NoError(t, err)
	assert.EqualValues(t, 2, atomic.LoadInt32(&attempts))
}
