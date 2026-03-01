package caller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"time"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type callStartResponse struct {
	Status string `json:"status"`
	CallID string `json:"call_id"`
	Error  string `json:"error"`
}

func (c *Client) StartCall(ctx context.Context, message string) (string, error) {
	url := fmt.Sprintf("%s/call/start?message=%s", c.baseURL, neturl.QueryEscape(message))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	log.Printf("caller-service response: status=%d body=%s", resp.StatusCode, string(rawBody))

	var body callStartResponse
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if body.Error != "" {
		return "", fmt.Errorf("caller-service error: %s", body.Error)
	}

	if body.Status != "ok" {
		return "", fmt.Errorf("unexpected status: %s", body.Status)
	}

	return body.CallID, nil
}
