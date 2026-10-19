package deepseek

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
)

type ErrorKind int

const (
	ErrorInvalidRequest ErrorKind = iota
	ErrorUnauthorized
	ErrorForbidden
	ErrorRateLimited
	ErrorServer
	ErrorNetwork
	ErrorTimeout
	ErrorInvalidResponse
)

// APIError is the single error type returned by Client. Use errors.As.
type APIError struct {
	Op         string
	Kind       ErrorKind
	StatusCode int
	RetryAfter int // seconds, from Retry-After header, 0 if absent
	Detail     string
	Err        error
}

func (e *APIError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Detail)
}

func (e *APIError) Unwrap() error { return e.Err }

func isRetryable(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Kind {
	case ErrorRateLimited, ErrorServer, ErrorNetwork, ErrorTimeout:
		return true
	default:
		return false
	}
}

func wrapNetworkError(op string, err error) *APIError {
	kind := ErrorNetwork
	detail := "network error"
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		kind = ErrorTimeout
		detail = "request timeout"
	}
	return &APIError{Op: op, Kind: kind, Err: err, Detail: detail}
}

func newAPIErrorFromResponse(op string, resp *http.Response) *APIError {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	detail := string(body)
	if detail == "" {
		detail = resp.Status
	}

	err := &APIError{
		Op:         op,
		StatusCode: resp.StatusCode,
		Detail:     detail,
	}

	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if sec, e := strconv.Atoi(ra); e == nil {
			err.RetryAfter = sec
		}
	}

	switch resp.StatusCode {
	case 400, 404, 422:
		err.Kind = ErrorInvalidRequest
	case 401:
		err.Kind = ErrorUnauthorized
	case 403:
		err.Kind = ErrorForbidden
	case 429:
		err.Kind = ErrorRateLimited
	case 500, 502, 503, 504:
		err.Kind = ErrorServer
	default:
		if resp.StatusCode >= 500 {
			err.Kind = ErrorServer
		} else {
			err.Kind = ErrorInvalidRequest
		}
	}

	return err
}
