package deepseek

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"golang.org/x/time/rate"

	"github.com/subconscious-labs/deepseek-mcp/internal/config"
)

type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	searchRL   *rate.Limiter
	fetchRL    *rate.Limiter
}

func NewClient(cfg config.Config) *Client {
	transport := &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		baseURL: cfg.APIBase,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   90 * time.Second,
		},
		searchRL: rate.NewLimiter(rate.Limit(cfg.SearchQPS), int(cfg.SearchQPS)),
		fetchRL:  rate.NewLimiter(rate.Limit(cfg.FetchQPS), int(cfg.FetchQPS)),
	}
}

func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if err := c.searchRL.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	return retry(ctx, func() (*SearchResponse, error) {
		return c.doSearch(ctx, req)
	}, 3)
}

func (c *Client) Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error) {
	if err := c.fetchRL.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	return retry(ctx, func() (*FetchResponse, error) {
		return c.doFetch(ctx, req)
	}, 3)
}

// retry — generic retry с exponential backoff + jitter.
// Honours Retry-After when present on an APIError.
func retry[T any](ctx context.Context, fn func() (*T, error), maxAttempts int) (*T, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxAttempts {
			break
		}
		sleep := backoffDuration(attempt, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
	}
	return nil, lastErr
}

func backoffDuration(attempt int, err error) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	jitter := 0.5 + rand.Float64()*0.5
	d := time.Duration(float64(base) * jitter)

	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		if ra := time.Duration(apiErr.RetryAfter) * time.Second; ra > d {
			d = ra
		}
	}
	return d
}
