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
	"github.com/subconscious-labs/deepseek-mcp/internal/counter"
)

type Client struct {
	baseURL     string
	apiKey      string
	httpClient  *http.Client
	searchRL    *rate.Limiter
	fetchRL     *rate.Limiter
	backoffBase time.Duration
	maxBackoff  time.Duration
	counter     *counter.Counter
}

type Option func(*Client)

func WithBackoffBase(d time.Duration) Option {
	return func(c *Client) { c.backoffBase = d }
}

func WithMaxBackoff(d time.Duration) Option {
	return func(c *Client) { c.maxBackoff = d }
}

func WithCounter(c *counter.Counter) Option {
	return func(cl *Client) { cl.counter = c }
}

func NewClient(cfg config.Config) *Client {
	return NewClientWithOptions(cfg)
}

func NewClientWithOptions(cfg config.Config, opts ...Option) *Client {
	transport := &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}
	c := &Client{
		baseURL:     cfg.APIBase,
		apiKey:      cfg.APIKey,
		httpClient:  &http.Client{Transport: transport, Timeout: 90 * time.Second},
		searchRL:    rate.NewLimiter(rate.Limit(cfg.SearchQPS), int(cfg.SearchQPS)),
		fetchRL:     rate.NewLimiter(rate.Limit(cfg.FetchQPS), int(cfg.FetchQPS)),
		backoffBase: time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	if c.counter != nil {
		if retryAfter, ok := c.counter.Check(); !ok {
			return nil, &APIError{
				Op: "search", Kind: ErrorDailyLimit,
				Detail:     "daily request limit reached",
				RetryAfter: retryAfter,
			}
		}
	}
	if err := c.searchRL.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	resp, err := retry(ctx, c, func() (*SearchResponse, error) {
		return c.doSearch(ctx, req)
	}, 3)
	if err == nil && c.counter != nil {
		_ = c.counter.Incr("search")
	}
	return resp, err
}

func (c *Client) Fetch(ctx context.Context, req FetchRequest) (*FetchResponse, error) {
	if c.counter != nil {
		if retryAfter, ok := c.counter.Check(); !ok {
			return nil, &APIError{
				Op: "fetch", Kind: ErrorDailyLimit,
				Detail:     "daily request limit reached",
				RetryAfter: retryAfter,
			}
		}
	}
	if err := c.fetchRL.Wait(ctx); err != nil {
		return nil, fmt.Errorf("rate limiter: %w", err)
	}
	resp, err := retry(ctx, c, func() (*FetchResponse, error) {
		return c.doFetch(ctx, req)
	}, 3)
	if err == nil && c.counter != nil {
		_ = c.counter.Incr("fetch")
	}
	return resp, err
}

func retry[T any](ctx context.Context, c *Client, fn func() (*T, error), maxAttempts int) (*T, error) {
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
		sleep := c.backoffDuration(attempt, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(sleep):
		}
	}
	return nil, lastErr
}

func (c *Client) backoffDuration(attempt int, err error) time.Duration {
	base := c.backoffBase * time.Duration(1<<uint(attempt-1))
	jitter := 0.5 + rand.Float64()*0.5
	d := time.Duration(float64(base) * jitter)

	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		if ra := time.Duration(apiErr.RetryAfter) * time.Second; ra > d {
			d = ra
		}
	}
	if c.maxBackoff > 0 && d > c.maxBackoff {
		d = c.maxBackoff
	}
	return d
}
