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
	"github.com/subconscious-labs/deepseek-mcp/internal/counter"
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

// --- Counter integration ---

// TestSearch_DailyLimitBlocksBeforeNetwork — при исчерпанном лимите клиент
// возвращает ErrorDailyLimit, НЕ делая HTTP-запрос к DeepSeek.
func TestSearch_DailyLimitBlocksBeforeNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("DeepSeek must not be called when daily limit is reached")
	}))
	defer srv.Close()

	ctr, err := counter.New(t.TempDir(), 1)
	require.NoError(t, err)
	require.NoError(t, ctr.Incr("search")) // исчерпали лимит: 1/1

	c := NewClientWithOptions(testConfig(srv.URL), WithCounter(ctr))

	_, err = c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorDailyLimit, apiErr.Kind)
	assert.Greater(t, apiErr.RetryAfter, 0, "retry_after должен указывать до UTC-полуночи")
	assert.EqualValues(t, 0, atomic.LoadInt32(&hits), "DeepSeek не должен был вызываться")
	assert.Equal(t, 1, ctr.Used(), "счётчик не должен меняться при отказе")
}

// TestFetch_DailyLimitBlocksBeforeNetwork — то же для fetch.
func TestFetch_DailyLimitBlocksBeforeNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		t.Errorf("DeepSeek must not be called when daily limit is reached")
	}))
	defer srv.Close()

	ctr, err := counter.New(t.TempDir(), 1)
	require.NoError(t, err)
	require.NoError(t, ctr.Incr("fetch"))

	c := NewClientWithOptions(testConfig(srv.URL), WithCounter(ctr))

	_, err = c.Fetch(context.Background(), FetchRequest{URL: "https://example.com"})
	require.Error(t, err)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, ErrorDailyLimit, apiErr.Kind)
	assert.EqualValues(t, 0, atomic.LoadInt32(&hits))
}

// TestSearch_CounterNotIncrementedOnError — 401 не должен съедать дневной лимит.
func TestSearch_CounterNotIncrementedOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ctr, err := counter.New(t.TempDir(), 0)
	require.NoError(t, err)

	c := NewClientWithOptions(testConfig(srv.URL), WithCounter(ctr))

	_, err = c.Search(context.Background(), SearchRequest{Query: "q"})
	require.Error(t, err)
	assert.Equal(t, 0, ctr.Used(), "неуспешный запрос не должен увеличивать счётчик")
}

// TestSearch_CounterIncrementedOnSuccess — успешный ответ даёт +1 в search.
func TestSearch_CounterIncrementedOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	ctr, err := counter.New(t.TempDir(), 0)
	require.NoError(t, err)

	c := NewClientWithOptions(testConfig(srv.URL), WithCounter(ctr))

	_, err = c.Search(context.Background(), SearchRequest{Query: "q"})
	require.NoError(t, err)

	snap := ctr.Snapshot()
	assert.Equal(t, 1, snap.SearchRequests)
	assert.Equal(t, 0, snap.FetchRequests)
	assert.InDelta(t, 0.00075, snap.EstimatedCostUSD, 1e-9)
}

// TestFetch_CounterIncrementedOnSuccess — успешный ответ даёт +1 в fetch.
func TestFetch_CounterIncrementedOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"t","content":"c"}`))
	}))
	defer srv.Close()

	ctr, err := counter.New(t.TempDir(), 0)
	require.NoError(t, err)

	c := NewClientWithOptions(testConfig(srv.URL), WithCounter(ctr))

	_, err = c.Fetch(context.Background(), FetchRequest{URL: "https://example.com"})
	require.NoError(t, err)

	snap := ctr.Snapshot()
	assert.Equal(t, 0, snap.SearchRequests)
	assert.Equal(t, 1, snap.FetchRequests)
}

// TestSearch_CounterIncrementedOnceAfterRetry — 429→429→200: счётчик +1,
// а не +3. Три HTTP-попытки — один биллинг-запрос.
func TestSearch_CounterIncrementedOnceAfterRetry(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	ctr, err := counter.New(t.TempDir(), 0)
	require.NoError(t, err)

	c := NewClientWithOptions(testConfig(srv.URL),
		WithCounter(ctr),
		WithBackoffBase(time.Millisecond),
		WithMaxBackoff(10*time.Millisecond),
	)

	_, err = c.Search(context.Background(), SearchRequest{Query: "q"})
	require.NoError(t, err)

	assert.EqualValues(t, 3, atomic.LoadInt32(&attempts), "должно быть 3 HTTP-попытки")
	assert.Equal(t, 1, ctr.Used(), "счётчик должен вырасти ровно на 1, несмотря на retry")
}
