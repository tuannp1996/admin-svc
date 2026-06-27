package service

import (
	"fmt"
	"log"
	"sync"
	"time"

	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	"admin-svc/internal/health"
	"admin-svc/internal/usecase/port"
)

// Statistics runs all checks on a fixed interval
type Statistics struct {
	cfg      *config.Config
	notifier port.Notifier
	docker   *docker.Checker
	health   *health.Checker
	curl     *health.Checker
	page     *health.Checker

	// Track which checks are currently in alert state to avoid re-sending
	alertState map[string]bool
	mu         sync.Mutex
}

func New(
	cfg *config.Config,
	notifier port.Notifier,
	dockerChecker *docker.Checker,
) *Statistics {
	return &Statistics{
		cfg:        cfg,
		notifier:   notifier,
		docker:     dockerChecker,
		health:     health.New(cfg.HealthChecks.TimeoutSeconds),
		curl:       health.New(cfg.CurlChecks.TimeoutSeconds),
		page:       health.New(cfg.PageChecks.TimeoutSeconds),
		alertState: make(map[string]bool),
	}
}

// Start blocks, running checks every interval
func (s *Statistics) Start() {
	interval := time.Duration(s.cfg.Scheduler.IntervalSeconds) * time.Second
	log.Printf("[scheduler] starting, interval=%s", interval)

	// Run once immediately on start
	s.runAll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.runAll()
	}
}

func (s *Statistics) runAll() {
	log.Printf("[scheduler] running checks...")

	var wg sync.WaitGroup

	if s.cfg.Docker.Enabled && s.docker != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runDockerChecks()
		}()
	}

	if s.cfg.HealthChecks.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runHealthChecks()
		}()
	}

	if s.cfg.CurlChecks.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runCurlChecks()
		}()
	}

	if s.cfg.PageChecks.Enabled {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runPageChecks()
		}()
	}

	wg.Wait()
	log.Printf("[Statistic] checks complete")
}

// StatusSummary returns a compact service status string for bot commands.
func (s *Statistics) StatusSummary() string {
	s.mu.Lock()
	alerting := 0
	for _, v := range s.alertState {
		if v {
			alerting++
		}
	}
	s.mu.Unlock()

	totalDocker := 0
	if s.cfg.Docker.Enabled {
		for _, c := range s.cfg.Docker.Containers {
			if c.AlertOnStopped {
				totalDocker++
			}
		}
	}

	totalHealth := 0
	if s.cfg.HealthChecks.Enabled {
		totalHealth = len(s.cfg.HealthChecks.Endpoints)
	}

	totalCurl := 0
	if s.cfg.CurlChecks.Enabled {
		totalCurl = len(s.cfg.CurlChecks.Requests)
	}

	totalPage := 0
	if s.cfg.PageChecks.Enabled {
		totalPage = len(s.cfg.PageChecks.Pages)
	}

	totalChecks := totalDocker + totalHealth + totalCurl + totalPage
	interval := time.Duration(s.cfg.Scheduler.IntervalSeconds) * time.Second

	healthDetails := ""
	if s.cfg.HealthChecks.Enabled {
		for _, ep := range s.cfg.HealthChecks.Endpoints {
			healthDetails += fmt.Sprintf("\n  - %s: %s : status: OK!", ep.Name, ep.URL)
		}
	}

	return fmt.Sprintf(
		"Admin service is running\nInterval: %s\nChecks: total=%d, docker=%d, health=%d, curl=%d, page=%d\nCurrent alerts: %d\nService Status:%s",
		interval,
		totalChecks,
		totalDocker,
		totalHealth,
		totalCurl,
		totalPage,
		alerting,
		healthDetails,
	)
}

// --- Docker ---

func (s *Statistics) runDockerChecks() {
	names := make([]string, 0, len(s.cfg.Docker.Containers))
	for _, c := range s.cfg.Docker.Containers {
		if c.AlertOnStopped {
			names = append(names, c.Name)
		}
	}

	results := s.docker.Check(names)
	for _, r := range results {
		key := "docker:" + r.ContainerName
		if r.Error != nil {
			detail := r.Error.Error()
			s.handleAlert(key, "Docker Container", r.ContainerName, detail)
		} else if !r.Running {
			s.handleAlert(key, "Docker Container", r.ContainerName, "container is not running")
		} else {
			s.handleRecovery(key, "Docker Container", r.ContainerName)
		}
	}
}

// --- Health ---

func (s *Statistics) runHealthChecks() {
	for _, ep := range s.cfg.HealthChecks.Endpoints {
		result := s.health.CheckEndpoint(ep.Name, ep.URL, ep.ExpectedStatus)
		key := "health:" + ep.Name
		if !result.OK {
			s.handleAlert(key, "HTTP Health", result.Name, result.Detail)
		} else {
			s.handleRecovery(key, "HTTP Health", result.Name)
		}
	}
}

// --- Curl ---

func (s *Statistics) runCurlChecks() {
	for _, req := range s.cfg.CurlChecks.Requests {
		result := s.curl.CheckCurl(health.CurlConfig{
			Name:           req.Name,
			Method:         req.Method,
			URL:            req.URL,
			Headers:        req.Headers,
			Body:           req.Body,
			ExpectedStatus: req.ExpectedStatus,
		})
		key := "curl:" + req.Name
		if !result.OK {
			s.handleAlert(key, "API Check", result.Name, result.Detail)
		} else {
			s.handleRecovery(key, "API Check", result.Name)
		}
	}
}

// --- Page ---

func (s *Statistics) runPageChecks() {
	for _, pg := range s.cfg.PageChecks.Pages {
		result := s.page.CheckPage(health.PageConfig{
			Name:           pg.Name,
			URL:            pg.URL,
			ExpectedStatus: pg.ExpectedStatus,
			ContainsText:   pg.ContainsText,
		})
		key := "page:" + pg.Name
		if !result.OK {
			s.handleAlert(key, "Page Availability", result.Name, result.Detail)
		} else {
			s.handleRecovery(key, "Page Availability", result.Name)
		}
	}
}

// --- Alert deduplication ---

// handleAlert sends an alert only if this check was previously OK (or first time)
func (s *Statistics) handleAlert(key, checkType, name, detail string) {
	log.Printf("[alert] %s | %s | %s", checkType, name, detail)
	s.mu.Lock()
	wasAlerting := s.alertState[key]
	s.alertState[key] = true
	s.mu.Unlock()

	if !wasAlerting {
		if err := s.notifier.SendAlert(checkType, name, detail); err != nil {
			log.Printf("[telegram] send alert error: %v", err)
		}
	}
}

// handleRecovery sends a recovery message only if check was previously alerting
func (s *Statistics) handleRecovery(key, checkType, name string) {
	s.mu.Lock()
	wasAlerting := s.alertState[key]
	s.alertState[key] = false
	s.mu.Unlock()

	if wasAlerting {
		log.Printf("[recovered] %s | %s", checkType, name)
		if err := s.notifier.SendRecovery(checkType, name); err != nil {
			log.Printf("[telegram] send recovery error: %v", err)
		}
	}
}
