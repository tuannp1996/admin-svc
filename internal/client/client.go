package client

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

	responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
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
