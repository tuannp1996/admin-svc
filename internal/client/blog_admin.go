package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type BlogAdminClient struct {
	baseURL string
	client  *http.Client
}

type BlogArticle struct {
	ID          int                    `json:"id"`
	Title       string                 `json:"title"`
	Slug        string                 `json:"slug"`
	Status      string                 `json:"status"`
	Category    string                 `json:"category"`
	CoverImage  string                 `json:"coverImage"`
	PreviewURL  string                 `json:"previewUrl"`
	ViewURL     string                 `json:"viewUrl"`
	MarkdownURL string                 `json:"markdownUrl"`
	FrontMatter map[string]interface{} `json:"front_matter"`
	CreatedAt   string                 `json:"created_at"`
	PublishedAt string                 `json:"published_at"`
}

type blogArticleList struct {
	Articles []BlogArticle `json:"articles"`
}

type SetCoverResult struct {
	ArticleID  int    `json:"article_id"`
	Slug       string `json:"slug"`
	ImagePath  string `json:"imagePath"`
	CoverImage string `json:"coverImage"`
	ViewURL    string `json:"viewUrl"`
}

func NewBlogAdmin(baseURL string, timeoutSeconds int) *BlogAdminClient {
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}
	return &BlogAdminClient{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  &http.Client{Timeout: time.Duration(timeoutSeconds) * time.Second},
	}
}

func (c *BlogAdminClient) List(ctx context.Context, status string, limit int) ([]BlogArticle, error) {
	query := url.Values{}
	if status = strings.TrimSpace(status); status != "" && status != "all" {
		query.Set("status", status)
	}
	query.Set("limit", strconv.Itoa(limit))
	var result blogArticleList
	if err := c.do(ctx, http.MethodGet, "/articles?"+query.Encode(), nil, &result); err != nil {
		return nil, err
	}
	return result.Articles, nil
}

func (c *BlogAdminClient) Get(ctx context.Context, articleID int) (*BlogArticle, error) {
	var article BlogArticle
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/articles/%d", articleID), nil, &article)
	return &article, err
}

func (c *BlogAdminClient) Action(ctx context.Context, articleID int, action string) (*BlogArticle, error) {
	var article BlogArticle
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/articles/%d/%s", articleID, action), nil, &article)
	return &article, err
}

func (c *BlogAdminClient) SetCover(ctx context.Context, identifier, imagePath string) (*SetCoverResult, error) {
	payload := map[string]interface{}{"imagePath": imagePath}
	if articleID, err := strconv.Atoi(identifier); err == nil && articleID > 0 {
		payload["id"] = articleID
	} else {
		payload["slug"] = identifier
	}
	var result SetCoverResult
	err := c.do(ctx, http.MethodPost, "/articles/cover", payload, &result)
	return &result, err
}

func (c *BlogAdminClient) do(ctx context.Context, method, path string, input, output interface{}) error {
	if c.baseURL == "" {
		return fmt.Errorf("blog admin base_url is empty")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("call blog admin api: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("blog admin api status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if output != nil && len(data) > 0 {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
