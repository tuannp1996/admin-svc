package blog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"admin-svc/internal/config"
)

type Client struct {
	cfg    config.BlogGenConfig
	client *http.Client
}

type triggerRequest struct {
	Topic string `json:"topic"`
}

func New(cfg config.BlogGenConfig) *Client {
	return &Client{
		cfg:    cfg,
		client: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second},
	}
}

func (c *Client) Trigger(ctx context.Context, topic string) (int, string, error) {
	if !c.cfg.Enabled {
		return 0, "", fmt.Errorf("blog_gen is disabled")
	}
	if strings.TrimSpace(c.cfg.URL) == "" {
		return 0, "", fmt.Errorf("blog_gen.url is empty")
	}

	method := strings.TrimSpace(c.cfg.Method)
	if method == "" {
		method = "POST"
	}

	requestBody := []byte(c.cfg.Body)
	if topic = strings.TrimSpace(topic); topic != "" {
		var err error
		requestBody, err = json.Marshal(triggerRequest{Topic: topic})
		if err != nil {
			return 0, "", fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL, bytes.NewReader(requestBody))
	if err != nil {
		return 0, "", fmt.Errorf("create request: %w", err)
	}

	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("call auto_blog: %w", err)
	}
	defer resp.Body.Close()

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	detail := strings.TrimSpace(string(responseBody))
	if detail == "" {
		detail = "(empty response body)"
	}

	if resp.StatusCode != c.cfg.ExpectedStatus {
		return resp.StatusCode, detail, fmt.Errorf("unexpected status %d (want %d)", resp.StatusCode, c.cfg.ExpectedStatus)
	}

	return resp.StatusCode, detail, nil
}
