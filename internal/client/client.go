package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"admin-svc/internal/config"
)

type Service struct {
	ServiceName *string
	ApiConfig   []config.APIConfig
	ApiClients  []*ApiClient
}

type ApiClient struct {
	ApiName *string
	Cfg     config.APIConfig
	client  *http.Client
}

type triggerRequest struct {
	Topic string `json:"topic"`
}

type GeneratedArticle struct {
	ID      string
	Slug    string
	Summary string
}

func ParseGeneratedArticle(detail string) (GeneratedArticle, error) {
	var payload interface{}
	if err := json.Unmarshal([]byte(detail), &payload); err != nil {
		return GeneratedArticle{}, fmt.Errorf("decode generated article response: %w", err)
	}

	articleMap := findArticleMap(payload)
	if articleMap == nil {
		return GeneratedArticle{}, fmt.Errorf("generated article response has no article object")
	}

	article := GeneratedArticle{
		ID:      jsonValueString(articleMap["id"]),
		Slug:    jsonValueString(articleMap["slug"]),
		Summary: firstString(articleMap, "summary", "description", "excerpt"),
	}
	if article.Summary == "" {
		if frontMatter, ok := articleMap["front_matter"].(map[string]interface{}); ok {
			article.Summary = firstString(frontMatter, "summary", "description", "excerpt")
		}
	}
	if article.ID == "" && article.Slug == "" {
		return GeneratedArticle{}, fmt.Errorf("generated article response has no id or slug")
	}
	return article, nil
}

func findArticleMap(value interface{}) map[string]interface{} {
	m, ok := value.(map[string]interface{})
	if !ok {
		return nil
	}
	if _, hasID := m["id"]; hasID {
		return m
	}
	if _, hasSlug := m["slug"]; hasSlug {
		return m
	}
	for _, key := range []string{"article", "data", "result"} {
		if nested := findArticleMap(m[key]); nested != nil {
			return nested
		}
	}
	return nil
}

func firstString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := jsonValueString(values[key]); value != "" {
			return value
		}
	}
	return ""
}

func jsonValueString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func New(cfg config.ClientConfig) *[]Service {
	var clients []Service
	for _, serviceCfg := range cfg.Service {
		if !serviceCfg.Enabled {
			continue
		}
		clients = append(clients, Service{
			ServiceName: serviceCfg.Name,
			ApiConfig:   serviceCfg.API,
			ApiClients:  buildApiClients(serviceCfg.API, cfg.TimeoutSeconds),
		})
	}
	return &clients
}

func buildApiClients(apiConfigs []config.APIConfig, timeoutSeconds int) []*ApiClient {
	var apiClients []*ApiClient
	for _, apiCfg := range apiConfigs {
		if !apiCfg.Enabled {
			continue
		}
		apiClients = append(apiClients, &ApiClient{
			ApiName: apiCfg.Name,
			Cfg:     apiCfg,
			client:  &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
		})
	}
	return apiClients
}

func (c *ApiClient) Trigger(ctx context.Context, topic string) (int, string, error) {
	if !c.Cfg.Enabled {
		return 0, "", fmt.Errorf("api is disabled")
	}
	if strings.TrimSpace(c.Cfg.URL) == "" {
		return 0, "", fmt.Errorf("api.url is empty")
	}

	method := strings.TrimSpace(c.Cfg.Method)
	if method == "" {
		method = "POST"
	}

	requestBody := []byte(c.Cfg.Body)
	if topic = strings.TrimSpace(topic); topic != "" {
		var err error
		requestBody, err = json.Marshal(triggerRequest{Topic: topic})
		if err != nil {
			return 0, "", fmt.Errorf("marshal request: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, c.Cfg.URL, bytes.NewReader(requestBody))
	if err != nil {
		return 0, "", fmt.Errorf("create request: %w", err)
	}

	for k, v := range c.Cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("call api: %w", err)
	}
	defer resp.Body.Close()

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	detail := strings.TrimSpace(string(responseBody))
	if detail == "" {
		detail = "(empty response body)"
	} else {
		detail = prettyJSON([]byte(detail))
	}

	if resp.StatusCode != c.Cfg.ExpectedStatus {
		return resp.StatusCode, detail, fmt.Errorf("unexpected status %d (want %d)", resp.StatusCode, c.Cfg.ExpectedStatus)
	}

	return resp.StatusCode, detail, nil
}

func prettyJSON(data []byte) string {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return strings.TrimSpace(string(data))
	}
	return out.String()
}
