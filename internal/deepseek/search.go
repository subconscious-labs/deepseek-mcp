package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

const maxRawResponseBytes = 10 * 1024 * 1024 // 10 MB hard cap on raw DeepSeek response

type SearchRequest struct {
	Query       string `json:"query"`
	Count       int    `json:"count,omitempty"`
	NeedContent bool   `json:"need_content,omitempty"`
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
	Summary string `json:"summary,omitempty"`
	Content string `json:"content,omitempty"`
}

type SearchResponse struct {
	Results   []SearchResult `json:"results"`
	RequestID string         `json:"request_id,omitempty"`
}

func (c *Client) doSearch(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, &APIError{Op: "search", Kind: ErrorInvalidRequest, Err: err, Detail: "marshal request"}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/tools/search", bytes.NewReader(body))
	if err != nil {
		return nil, &APIError{Op: "search", Kind: ErrorInvalidRequest, Err: err, Detail: "build request"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, wrapNetworkError("search", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, newAPIErrorFromResponse("search", resp)
	}

	var out SearchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRawResponseBytes)).Decode(&out); err != nil {
		return nil, &APIError{Op: "search", Kind: ErrorInvalidResponse, Err: err, Detail: "decode response"}
	}
	return &out, nil
}
