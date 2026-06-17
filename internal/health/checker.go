package health

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Result is the outcome of any HTTP-based check
type Result struct {
	Name    string
	URL     string
	OK      bool
	Status  int
	Detail  string
}

// Checker performs HTTP checks
type Checker struct {
	client *http.Client
}

func New(timeoutSeconds int) *Checker {
	return &Checker{
		client: &http.Client{
			Timeout: time.Duration(timeoutSeconds) * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// CheckEndpoint does a simple GET and validates status code
func (c *Checker) CheckEndpoint(name, url string, expectedStatus int) Result {
	resp, err := c.client.Get(url)
	if err != nil {
		log.Printf("[health] %s error: %v", name, err)
		return Result{Name: name, URL: url, OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	ok := resp.StatusCode == expectedStatus
	detail := ""
	if !ok {
		detail = fmt.Sprintf("got HTTP %d, want %d", resp.StatusCode, expectedStatus)
	}
	return Result{Name: name, URL: url, OK: ok, Status: resp.StatusCode, Detail: detail}
}

// CheckCurl performs a configurable HTTP request (any method, headers, body)
type CurlConfig struct {
	Name           string
	Method         string
	URL            string
	Headers        map[string]string
	Body           string
	ExpectedStatus int
}

func (c *Checker) CheckCurl(cfg CurlConfig) Result {
	method := cfg.Method
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	if cfg.Body != "" {
		bodyReader = strings.NewReader(cfg.Body)
	}

	req, err := http.NewRequest(method, cfg.URL, bodyReader)
	if err != nil {
		return Result{Name: cfg.Name, URL: cfg.URL, OK: false, Detail: err.Error()}
	}

	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		log.Printf("[curl] %s error: %v", cfg.Name, err)
		return Result{Name: cfg.Name, URL: cfg.URL, OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	ok := resp.StatusCode == cfg.ExpectedStatus
	detail := ""
	if !ok {
		detail = fmt.Sprintf("got HTTP %d, want %d", resp.StatusCode, cfg.ExpectedStatus)
	}
	return Result{Name: cfg.Name, URL: cfg.URL, OK: ok, Status: resp.StatusCode, Detail: detail}
}

// CheckPage does GET, validates status and optionally checks for a string in the body
type PageConfig struct {
	Name           string
	URL            string
	ExpectedStatus int
	ContainsText   string
}

func (c *Checker) CheckPage(cfg PageConfig) Result {
	resp, err := c.client.Get(cfg.URL)
	if err != nil {
		log.Printf("[page] %s error: %v", cfg.Name, err)
		return Result{Name: cfg.Name, URL: cfg.URL, OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode != cfg.ExpectedStatus {
		detail := fmt.Sprintf("got HTTP %d, want %d", resp.StatusCode, cfg.ExpectedStatus)
		return Result{Name: cfg.Name, URL: cfg.URL, OK: false, Status: resp.StatusCode, Detail: detail}
	}

	if cfg.ContainsText != "" {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // read max 1MB
		if err != nil {
			return Result{Name: cfg.Name, URL: cfg.URL, OK: false, Detail: "read body: " + err.Error()}
		}
		if !strings.Contains(string(body), cfg.ContainsText) {
			detail := fmt.Sprintf("page does not contain %q", cfg.ContainsText)
			return Result{Name: cfg.Name, URL: cfg.URL, OK: false, Status: resp.StatusCode, Detail: detail}
		}
	}

	return Result{Name: cfg.Name, URL: cfg.URL, OK: true, Status: resp.StatusCode}
}
