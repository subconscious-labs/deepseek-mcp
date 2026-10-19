package conformance_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// protocolVersion — ревизия, которую мы заявляем в initialize.
// Сервер может ответить своей (SDK решает), тест принимает любую непустую.
const protocolVersion = "2025-11-25"

// startServer запускает bin/deepseek-mcp как MCP-сервер по stdio.
// Возвращает stdin для записи, сканер stdout и cleanup.
func startServer(t *testing.T) (io.WriteCloser, *bufio.Scanner) {
	t.Helper()

	binary := "../../bin/deepseek-mcp"
	if _, err := os.Stat(binary); err != nil {
		t.Skip("bin/deepseek-mcp not built; run `make build` first")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, binary)
	// API-ключ фиктивный: tools/list и валидация аргументов не ходят в сеть.
	cmd.Env = append(os.Environ(), "DEEPSEEK_API_KEY=sk-conformance-fake")

	stdin, err := cmd.StdinPipe()
	require.NoError(t, err)

	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)

	cmd.Stderr = io.Discard // slog пишет сюда, не мешаем JSON-RPC

	require.NoError(t, cmd.Start(), "failed to start MCP server")
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	return stdin, bufio.NewScanner(stdout)
}

func send(t *testing.T, w io.Writer, msg map[string]any) {
	t.Helper()
	b, err := json.Marshal(msg)
	require.NoError(t, err)
	_, err = w.Write(append(b, '\n'))
	require.NoError(t, err)
}

// recvResponse читает stdout, пока не найдёт сообщение с нужным id.
// Сервер может слать notifications (без id) — их пропускаем.
func recvResponse(t *testing.T, sc *bufio.Scanner, wantID float64) map[string]any {
	t.Helper()
	for sc.Scan() {
		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m), "stdout is not JSON: %s", sc.Text())
		if id, ok := m["id"].(float64); ok && id == wantID {
			return m
		}
	}
	t.Fatalf("no response with id=%v before stdout closed", wantID)
	return nil
}

// TestConformance_InitializeToolsCall — базовый wire-level сценарий:
// initialize → notifications/initialized → tools/list → tools/call (ошибка).
func TestConformance_InitializeToolsCall(t *testing.T) {
	stdin, scanner := startServer(t)

	// 1. initialize
	send(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "conformance", "version": "0.0.1"},
		},
	})
	initResp := recvResponse(t, scanner, 1)

	result, ok := initResp["result"].(map[string]any)
	require.True(t, ok, "initialize response has no result: %v", initResp)
	assert.NotEmpty(t, result["protocolVersion"], "protocolVersion must be non-empty")
	assert.Equal(t, "deepseek-mcp", serverName(result), "serverInfo.name")

	// 2. notifications/initialized — обязательное уведомление после handshake.
	send(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	// 3. tools/list
	send(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	toolsResp := recvResponse(t, scanner, 2)

	toolsResult, ok := toolsResp["result"].(map[string]any)
	require.True(t, ok, "tools/list has no result")
	tools, ok := toolsResult["tools"].([]any)
	require.True(t, ok, "tools/list result.tools is not an array")

	names := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		require.True(t, ok)
		name, _ := tool["name"].(string)
		names = append(names, name)

		assert.NotEmpty(t, tool["description"], "tool %q description must be non-empty", name)
		schema, ok := tool["inputSchema"].(map[string]any)
		require.True(t, ok, "tool %q has no inputSchema", name)
		assert.Equal(t, "object", schema["type"], "tool %q inputSchema.type", name)
	}

	assert.Contains(t, names, "deepseek_search")
	assert.Contains(t, names, "deepseek_fetch")

	// 4. tools/call с невалидным URL — ошибка должна вернуться на уровне протокола,
	// не как JSON-RPC error, а как result.isError=true с текстом.
	send(t, stdin, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "deepseek_fetch",
			"arguments": map[string]any{"url": "http://127.0.0.1/"},
		},
	})
	callResp := recvResponse(t, scanner, 3)

	callResult, ok := callResp["result"].(map[string]any)
	require.True(t, ok, "tools/call returned JSON-RPC error instead of result: %v", callResp["error"])
	assert.Equal(t, true, callResult["isError"], "SSRF URL must produce isError=true")

	text := firstText(t, callResult)
	assert.Contains(t, text, "invalid_url", "error payload must carry invalid_url")
}

// TestConformance_SchemaEnforcedBySDK — пустой query не проходит
// minLength: 1 в JSON-схеме. SDK отклонит его ДО вызова нашего handler'а,
// вернув isError=true с текстом валидатора (или JSON-RPC error, в зависимости
// от версии SDK). Обе формы корректны по спеке — тест принимает любую.
func TestConformance_SchemaEnforcedBySDK(t *testing.T) {
	stdin, scanner := startServer(t)

	send(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "conformance", "version": "0.0.1"},
		},
	})
	recvResponse(t, scanner, 1)
	send(t, stdin, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	send(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "deepseek_search",
			"arguments": map[string]any{"query": ""},
		},
	})
	resp := recvResponse(t, scanner, 2)

	// Верхнеуровневый JSON-RPC error — валидный ответ SDK на нарушение схемы.
	if errField, ok := resp["error"]; ok && errField != nil {
		t.Logf("SDK rejected empty query via JSON-RPC error: %v", errField)
		return
	}

	// Либо isError=true внутри result.
	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "expected either JSON-RPC error or result, got: %v", resp)
	assert.Equal(t, true, result["isError"])
	t.Logf("SDK rejected empty query via tool error: %s", firstText(t, result))
}

// TestConformance_InvalidSearchArguments — пробел проходит minLength:1 в схеме,
// но TrimSpace в handler'е делает его пустым → наш invalid_query.
// Это единственный способ дотянуться до handler-валидации через протокол.
func TestConformance_InvalidSearchArguments(t *testing.T) {
	stdin, scanner := startServer(t)

	send(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "conformance", "version": "0.0.1"},
		},
	})
	recvResponse(t, scanner, 1)
	send(t, stdin, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})

	send(t, stdin, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{
			"name":      "deepseek_search",
			"arguments": map[string]any{"query": "   "},
		},
	})
	resp := recvResponse(t, scanner, 2)

	result, ok := resp["result"].(map[string]any)
	require.True(t, ok, "tools/call should return result for schema-valid input")
	assert.Equal(t, true, result["isError"])
	assert.Contains(t, firstText(t, result), "invalid_query",
		"whitespace query должен дойти до handler'а и получить наш код ошибки")
}

func serverName(result map[string]any) string {
	si, _ := result["serverInfo"].(map[string]any)
	name, _ := si["name"].(string)
	return name
}

func firstText(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]any)
	require.True(t, ok, "result.content is not an array")
	require.NotEmpty(t, content)
	item, ok := content[0].(map[string]any)
	require.True(t, ok)
	text, _ := item["text"].(string)
	return text
}
