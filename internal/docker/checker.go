package docker

import (
	"context"
	"encoding/json"
	"fmt"
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

func (c *Checker) Close() {}