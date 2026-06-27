package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Telegram     TelegramConfig     `yaml:"telegram"`
	Scheduler    SchedulerConfig    `yaml:"scheduler"`
	Docker       DockerConfig       `yaml:"docker"`
	HealthChecks HealthChecksConfig `yaml:"health_checks"`
	CurlChecks   CurlChecksConfig   `yaml:"curl_checks"`
	PageChecks   PageChecksConfig   `yaml:"page_checks"`
	Clients      ClientConfig       `yaml:"clients"`
}

type TelegramConfig struct {
	BotToken string `yaml:"bot_token"`
	ChatID   string `yaml:"chat_id"`
}

type SchedulerConfig struct {
	IntervalSeconds int `yaml:"interval_seconds"`
}

type DockerConfig struct {
	Enabled    bool              `yaml:"enabled"`
	Containers []ContainerConfig `yaml:"containers"`
}

type ContainerConfig struct {
	Name           string `yaml:"name"`
	AlertOnStopped bool   `yaml:"alert_on_stopped"`
}

type HealthChecksConfig struct {
	Enabled        bool            `yaml:"enabled"`
	TimeoutSeconds int             `yaml:"timeout_seconds"`
	Endpoints      []EndpointCheck `yaml:"endpoints"`
}

type EndpointCheck struct {
	Name           string `yaml:"name"`
	URL            string `yaml:"url"`
	ExpectedStatus int    `yaml:"expected_status"`
}

type CurlChecksConfig struct {
	Enabled        bool        `yaml:"enabled"`
	TimeoutSeconds int         `yaml:"timeout_seconds"`
	Requests       []CurlCheck `yaml:"requests"`
}

type CurlCheck struct {
	Name           string            `yaml:"name"`
	Method         string            `yaml:"method"`
	URL            string            `yaml:"url"`
	Headers        map[string]string `yaml:"headers"`
	Body           string            `yaml:"body"`
	ExpectedStatus int               `yaml:"expected_status"`
}

type PageChecksConfig struct {
	Enabled        bool        `yaml:"enabled"`
	TimeoutSeconds int         `yaml:"timeout_seconds"`
	Pages          []PageCheck `yaml:"pages"`
}

type PageCheck struct {
	Name           string `yaml:"name"`
	URL            string `yaml:"url"`
	ExpectedStatus int    `yaml:"expected_status"`
	ContainsText   string `yaml:"contains_text"`
}

type ClientConfig struct {
	TimeoutSeconds int             `yaml:"timeout_seconds"`
	Service        []ServiceConfig `yaml:"service"`
}

type ServiceConfig struct {
	Enabled bool        `yaml:"enabled"`
	Name    *string     `yaml:"name"`
	API     []APIConfig `yaml:"api"`
}

type APIConfig struct {
	Enabled        bool              `yaml:"enabled"`
	Name           *string           `yaml:"name"`
	URL            string            `yaml:"url"`
	Method         string            `yaml:"method"`
	Headers        map[string]string `yaml:"headers"`
	Body           string            `yaml:"body"`
	ExpectedStatus int               `yaml:"expected_status"`
}

type BlogGenConfig struct {
	Enabled        bool              `yaml:"enabled"`
	URL            string            `yaml:"url"`
	Method         string            `yaml:"method"`
	Headers        map[string]string `yaml:"headers"`
	Body           string            `yaml:"body"`
	ExpectedStatus int               `yaml:"expected_status"`
	TimeoutSeconds int               `yaml:"timeout_seconds"`
}

// LoadEnv reads a .env file and injects variables into the process environment.
// Silently skips if the file does not exist. Real env vars are never overwritten.
func LoadEnv(envPath string) error {
	data, err := os.ReadFile(envPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read .env: %w", err)
	}

	for _, raw := range splitLines(string(data)) {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// Strip surrounding quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// Real env vars take priority
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, strings.TrimRight(s[start:i], "\r"))
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand ${VAR} references using current environment (including .env values)
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Defaults
	if cfg.Scheduler.IntervalSeconds <= 0 {
		cfg.Scheduler.IntervalSeconds = 60
	}
	if cfg.HealthChecks.TimeoutSeconds <= 0 {
		cfg.HealthChecks.TimeoutSeconds = 10
	}
	if cfg.CurlChecks.TimeoutSeconds <= 0 {
		cfg.CurlChecks.TimeoutSeconds = 15
	}
	if cfg.PageChecks.TimeoutSeconds <= 0 {
		cfg.PageChecks.TimeoutSeconds = 15
	}

	return &cfg, nil
}
