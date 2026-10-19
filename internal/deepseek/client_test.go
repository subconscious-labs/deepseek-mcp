package deepseek

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/subconscious-labs/deepseek-mcp/internal/config"
)

func testConfig(baseURL string) config.Config {
	return config.Config{
		APIBase:   baseURL,
		APIKey:    "sk-test",
		SearchQPS: 1000,
		FetchQPS:  1000,
	}
}

func fastClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClientWithOptions(testConfig(baseURL),
		WithBackoffBase(time.Millisecond),
		WithMaxBackoff(10*time.Millisecond),
	)
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient(testConfig(baseURL))
}

func TestSearch_RetryOn429ThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://e.com","snippet":"s"}]}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	resp, err := c.Search(context.Background(), SearchRequest{Query: "q", Count: 1})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Results, 1)
	assert.EqualValues(t, 3, atomic.LoadInt32(&attempts))
}

func TestSearch_RetryOn5xxExhausted(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`bad gateway`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	_, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorServer, apiErr.Kind)
	assert.EqualValues(t, 3, atomic.LoadInt32(&attempts))
}

func TestSearch_NoRetryOn401(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorUnauthorized, apiErr.Kind)
	assert.EqualValues(t, 1, atomic.LoadInt32(&attempts))
}

func TestSearch_NoRetryOn400(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad query"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorInvalidRequest, apiErr.Kind)
	assert.EqualValues(t, 1, atomic.LoadInt32(&attempts))
}

// newTestClient без WithMaxBackoff — Retry-After=1 должен реально ждаться,
// иначе тест теряет смысл.
func TestSearch_RetryAfterHonored(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	start := time.Now()
	_, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, time.Second)
	assert.EqualValues(t, 2, atomic.LoadInt32(&attempts))
}

func TestSearch_NetworkErrorRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	_, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorNetwork, apiErr.Kind)
	assert.EqualValues(t, 3, atomic.LoadInt32(&attempts))
}

func TestSearch_InvalidJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorInvalidResponse, apiErr.Kind)
}

func TestFetch_RetrySameAsSearch(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"title":"t","content":"# hi"}`))
	}))
	defer srv.Close()

	c := fastClient(t, srv.URL)
	resp, err := c.Fetch(context.Background(), FetchRequest{URL: "https://example.com"})
	require.NoError(t, err)
	assert.Equal(t, "t", resp.Title)
	assert.EqualValues(t, 2, atomic.LoadInt32(&attempts))
}
