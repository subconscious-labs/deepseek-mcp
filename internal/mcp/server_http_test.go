package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/subconscious-labs/deepseek-mcp/internal/deepseek"
)

func newHTTPTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()

	cfg := baseConfig("http://127.0.0.1:0")
	cfg.Transport = "http"
	cfg.HTTPToken = token

	client := deepseek.NewClient(cfg)
	s := NewServer(client, cfg, testLogger())
	return httptest.NewServer(s.HTTPHandler())
}

func mcpPost(t *testing.T, url, token string, body any) (*http.Response, []byte) {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, url+"/mcp", bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

func TestHTTP_RequiresToken(t *testing.T) {
	srv := newHTTPTestServer(t, "secret-token")
	defer srv.Close()

	resp, body := mcpPost(t, srv.URL, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "x", "version": "0"}},
	})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "Bearer")
	assert.Contains(t, string(body), "unauthorized")
}

func TestHTTP_RejectsWrongToken(t *testing.T) {
	srv := newHTTPTestServer(t, "secret-token")
	defer srv.Close()

	resp, _ := mcpPost(t, srv.URL, "wrong-token", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "x", "version": "0"}},
	})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestHTTP_InitializeWithToken(t *testing.T) {
	srv := newHTTPTestServer(t, "secret-token")
	defer srv.Close()

	resp, body := mcpPost(t, srv.URL, "secret-token", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", string(body))
	assert.Contains(t, string(body), "deepseek-mcp")
	assert.Contains(t, string(body), "protocolVersion")
}

func TestHTTP_AllowInsecureNoToken(t *testing.T) {
	cfg := baseConfig("http://127.0.0.1:0")
	cfg.Transport = "http"
	cfg.HTTPToken = ""
	cfg.AllowInsecureHTTP = true

	client := deepseek.NewClient(cfg)
	s := NewServer(client, cfg, testLogger())
	srv := httptest.NewServer(s.HTTPHandler())
	defer srv.Close()

	resp, body := mcpPost(t, srv.URL, "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{"protocolVersion": "2025-11-25", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "x", "version": "0"}},
	})
	require.Equal(t, http.StatusOK, resp.StatusCode, "body: %s", string(body))
	assert.Contains(t, string(body), "deepseek-mcp")
}

func TestHTTP_RunRefusesWithoutTokenOrInsecure(t *testing.T) {
	cfg := baseConfig("http://127.0.0.1:0")
	cfg.Transport = "http"
	cfg.HTTPToken = ""
	cfg.AllowInsecureHTTP = false

	client := deepseek.NewClient(cfg)
	s := NewServer(client, cfg, testLogger())

	err := s.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--http-token")
}

func TestHTTP_Healthz(t *testing.T) {
	srv := newHTTPTestServer(t, "secret-token")
	defer srv.Close()

	// Без токена — 200, потому что /healthz вне auth-обёртки.
	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	assert.Equal(t, "ok", string(body))
}
