package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
)

type FetchRequest struct {
	URL string `json:"url"`
}

type FetchResponse struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	RequestID string `json:"request_id,omitempty"`
}

func (c *Client) doFetch(ctx context.Context, req FetchRequest) (*FetchResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, &APIError{Op: "fetch", Kind: ErrorInvalidRequest, Err: err, Detail: "marshal request"}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/tools/fetch", bytes.NewReader(body))
	if err != nil {
		return nil, &APIError{Op: "fetch", Kind: ErrorInvalidRequest, Err: err, Detail: "build request"}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, wrapNetworkError("fetch", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, newAPIErrorFromResponse("fetch", resp)
	}

	var out FetchResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxRawResponseBytes)).Decode(&out); err != nil {
		return nil, &APIError{Op: "fetch", Kind: ErrorInvalidResponse, Err: err, Detail: "decode response"}
	}
	return &out, nil
}
