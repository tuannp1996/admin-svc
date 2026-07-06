package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

type CheckResult struct {
	ContainerName string
	Running       bool
	Error         error
}

type ContainerStatus struct {
	Name    string
	State   string
	Running bool
}

type Checker struct {
	httpClient *http.Client
	baseURL    string
}

type dockerContainer struct {
	Names  []string `json:"Names"`
	State  string   `json:"State"`
}

func New() (*Checker, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
		},
	}

	c := &Checker{
		httpClient: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		baseURL:    "http://localhost",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/_ping", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("docker ping: %w", err)
	}
	resp.Body.Close()
	return c, nil
}

func (c *Checker) Check(containerNames []string) []CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/containers/json?all=true", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		log.Printf("[docker] list error: %v", err)
		results := make([]CheckResult, len(containerNames))
		for i, name := range containerNames {
			results[i] = CheckResult{ContainerName: name, Error: err}
		}
		return results
	}
	defer resp.Body.Close()

	var containers []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		results := make([]CheckResult, len(containerNames))
		for i, name := range containerNames {
			results[i] = CheckResult{ContainerName: name, Error: err}
		}
		return results
	}

	statusMap := make(map[string]bool)
	for _, ctr := range containers {
		for _, name := range ctr.Names {
			statusMap[strings.TrimPrefix(name, "/")] = (ctr.State == "running")
		}
	}

	var results []CheckResult
	for _, name := range containerNames {
		running, found := statusMap[name]
		if !found {
			results = append(results, CheckResult{ContainerName: name, Error: fmt.Errorf("container %q not found", name)})
		} else {
			results = append(results, CheckResult{ContainerName: name, Running: running})
		}
	}
	return results
}

func (c *Checker) Restart(ctx context.Context, containerName string) error {
	containerName = strings.TrimSpace(containerName)
	if containerName == "" {
		return fmt.Errorf("container name is required")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/containers/"+containerName+"/restart?t=10", nil)
	if err != nil {
		return fmt.Errorf("build restart request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("restart %q: %w", containerName, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if len(body) > 0 {
		return fmt.Errorf("restart %q failed with status %d: %s", containerName, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return fmt.Errorf("restart %q failed with status %d", containerName, resp.StatusCode)
}

func (c *Checker) ListAll(ctx context.Context) ([]ContainerStatus, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/containers/json?all=true", nil)
	if err != nil {
		return nil, fmt.Errorf("build list request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if len(body) > 0 {
			return nil, fmt.Errorf("list containers failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		return nil, fmt.Errorf("list containers failed with status %d", resp.StatusCode)
	}

	var containers []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&containers); err != nil {
		return nil, fmt.Errorf("decode container list: %w", err)
	}

	statuses := make([]ContainerStatus, 0, len(containers))
	for _, ctr := range containers {
		if len(ctr.Names) == 0 {
			statuses = append(statuses, ContainerStatus{
				Name:    "<unknown>",
				State:   ctr.State,
				Running: ctr.State == "running",
			})
			continue
		}

		for _, name := range ctr.Names {
			statuses = append(statuses, ContainerStatus{
				Name:    strings.TrimPrefix(name, "/"),
				State:   ctr.State,
				Running: ctr.State == "running",
			})
		}
	}

	return statuses, nil
}

func (c *Checker) Close() {}