package scheduler

import (
	"log"
	"sync"
	"time"

	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	"admin-svc/internal/health"
	"admin-svc/internal/telegram"
)

// Scheduler runs all checks on a fixed interval
type Scheduler struct {
	cfg      *config.Config
	notifier *telegram.Notifier
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
	notifier *telegram.Notifier,
	dockerChecker *docker.Checker,
) *Scheduler {
	return &Scheduler{
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
func (s *Scheduler) Start() {
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

func (s *Scheduler) runAll() {
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
	log.Printf("[scheduler] checks complete")
}

// --- Docker ---

func (s *Scheduler) runDockerChecks() {
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

func (s *Scheduler) runHealthChecks() {
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

func (s *Scheduler) runCurlChecks() {
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

func (s *Scheduler) runPageChecks() {
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
func (s *Scheduler) handleAlert(key, checkType, name, detail string) {
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
func (s *Scheduler) handleRecovery(key, checkType, name string) {
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
