package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/subconscious-labs/deepseek-mcp/internal/config"
	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

const (
	costPerRequestUSD      = 0.00075
	maxSnippetRunes        = 500
	maxSummaryRunes        = 2000
	maxResultContentRunes  = 8000
	maxFetchContentRunes   = 32000
)

func boolPtr(b bool) *bool { return &b }

// truncateRunes обрезает строку по количеству рун, не ломая UTF-8.
func truncateRunes(s string, max int) (string, bool) {
	if max <= 0 {
		return "", s != ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	r := []rune(s)
	return string(r[:max]), true
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// errorResult формирует tool-ответ с IsError=true и JSON-телом,
// как требует §10 ТЗ.
func errorResult(code, detail string, retryAfter int) *sdkmcp.CallToolResult {
	payload := map[string]any{"error": code, "detail": detail}
	if retryAfter > 0 {
		payload["retry_after_sec"] = retryAfter
	}
	b, _ := json.Marshal(payload)
	return &sdkmcp.CallToolResult{
		IsError: true,
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: string(b)}},
	}
}

// mapDeepSeekError переводит типизированную ошибку клиента в коды §10.
func mapDeepSeekError(err error, op string) (code, detail string, retryAfter int) {
	var apiErr *deepseek.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Kind {
		case deepseek.ErrorUnauthorized:
			return "unauthorized", "DeepSeek API key is invalid or missing", 0
		case deepseek.ErrorForbidden:
			return "forbidden", apiErr.Detail, 0
		case deepseek.ErrorRateLimited:
			return "rate_limited", apiErr.Detail, apiErr.RetryAfter
		case deepseek.ErrorServer:
			return "server_error", apiErr.Detail, 0
		case deepseek.ErrorNetwork:
			return "network_error", apiErr.Detail, 0
		case deepseek.ErrorTimeout:
			return "timeout", apiErr.Detail, 0
		case deepseek.ErrorInvalidResponse:
			return "internal_error", "invalid response from DeepSeek", 0
		case deepseek.ErrorInvalidRequest:
			if op == "search" {
				return "invalid_query", apiErr.Detail, 0
			}
			return "invalid_url", apiErr.Detail, 0
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "request deadline exceeded", 0
	}
	return "internal_error", err.Error(), 0
}

// logToolCall пишет structured log §9.1, не логируя API key и content.
func logToolCall(logger *slog.Logger, cfg config.Config, tool, inputKind, input string,
	start time.Time, status string, errCode string, requestID string, results int, cost float64) {

	attrs := []any{
		"tool", tool,
		"phase", "tool_call",
		"status", status,
		"duration_ms", time.Since(start).Milliseconds(),
		"cost_usd", cost,
		"results_returned", results,
	}
	if requestID != "" {
		attrs = append(attrs, "request_id", requestID)
	}
	if cfg.RedactQueries {
		attrs = append(attrs,
			inputKind+"_hash", sha256Hex(input),
			inputKind+"_len", utf8.RuneCountInString(input),
		)
	} else {
		attrs = append(attrs, inputKind, input, inputKind+"_len", utf8.RuneCountInString(input))
	}
	if errCode != "" {
		attrs = append(attrs, "error_code", errCode)
	}
	logger.Info("deepseek tool call", attrs...)
}

func fmtInt(n int) string { return fmt.Sprintf("%d", n) }
