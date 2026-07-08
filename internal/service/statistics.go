package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"admin-svc/internal/client"
	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	"admin-svc/internal/health"
	"admin-svc/internal/usecase/port"

	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

var errNoTopicAvailable = errors.New("no topic available")

// Statistics runs all checks on a fixed interval
type Statistics struct {
	cfg      *config.Config
	notifier port.Notifier
	docker   *docker.Checker
	health   *health.Checker
	curl     *health.Checker
	page     *health.Checker
	services *[]client.Service

	// Track which checks are currently in alert state to avoid re-sending
	alertState map[string]bool
	mu         sync.Mutex
	topicIndex map[string]int
	redisConns map[string]*redis.Client
}

func New(
	cfg *config.Config,
	notifier port.Notifier,
	dockerChecker *docker.Checker,
	services *[]client.Service,
) *Statistics {
	return &Statistics{
		cfg:        cfg,
		notifier:   notifier,
		docker:     dockerChecker,
		health:     health.New(cfg.HealthChecks.TimeoutSeconds),
		curl:       health.New(cfg.CurlChecks.TimeoutSeconds),
		page:       health.New(cfg.PageChecks.TimeoutSeconds),
		services:   services,
		alertState: make(map[string]bool),
		topicIndex: make(map[string]int),
		redisConns: make(map[string]*redis.Client),
	}
}

// Start blocks, running checks every interval
func (s *Statistics) Start() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[scheduler] panic recovered: %v\n%s", r, debug.Stack())
		}
	}()

	interval := time.Duration(s.cfg.Scheduler.IntervalSeconds) * time.Second
	log.Printf("[scheduler] starting, interval=%s", interval)
	s.startCronJobs()

	// Run once immediately on start
	s.runAll()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.runAll()
	}
}

func (s *Statistics) startCronJobs() {
	if len(s.cfg.Scheduler.Jobs) == 0 {
		return
	}

	parser := cron.NewParser(
		cron.SecondOptional |
			cron.Minute |
			cron.Hour |
			cron.Dom |
			cron.Month |
			cron.Dow |
			cron.Descriptor,
	)

	c := cron.New(
		cron.WithParser(parser),
		cron.WithLocation(time.Local),
	)

	configured := 0
	for _, job := range s.cfg.Scheduler.Jobs {
		if !job.Enabled {
			continue
		}

		name := strings.TrimSpace(job.Name)
		if name == "" {
			log.Printf("[cron] skip job with empty name")
			continue
		}

		spec := strings.TrimSpace(job.Cron)
		if spec == "" {
			log.Printf("[cron] skip job=%s, empty cron expression", name)
			continue
		}

		apiClient := s.findAPIClient(job.Service, job.API)
		if apiClient == nil {
			log.Printf("[cron] skip job=%s, api not found (service=%q api=%q)", name, job.Service, job.API)
			continue
		}

		jobCopy := job
		if _, err := c.AddFunc(spec, func() {
			s.runCronJob(jobCopy, apiClient)
		}); err != nil {
			log.Printf("[cron] invalid cron for job=%s spec=%q err=%v", name, spec, err)
			continue
		}

		configured++
		log.Printf("[cron] registered job=%s spec=%q", name, spec)
	}

	if configured == 0 {
		return
	}

	c.Start()
	log.Printf("[cron] started with %d job(s)", configured)
}

func (s *Statistics) runCronJob(job config.CronJobConfig, apiClient *client.ApiClient) {
	name := strings.TrimSpace(job.Name)
	if name == "" {
		name = strings.TrimSpace(job.API)
	}
	if name == "" {
		name = "unnamed"
	}

	key := "cron:" + name
	timeoutSeconds := s.cfg.Clients.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 30
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()

	topic, ack, onFailure, err := s.resolveCronTopic(ctx, name, job)
	if err != nil {
		if errors.Is(err, errNoTopicAvailable) {
			log.Printf("[cron] no topic yet job=%s source=%s", name, job.TopicSource)
			return
		}
		s.handleAlert(key, "Cron Job", name, "resolve topic failed: "+err.Error())
		return
	}

	status, detail, err := apiClient.Trigger(ctx, topic)
	if err != nil {
		if onFailure != nil {
			if failureNote, handlerErr := onFailure(ctx, err); handlerErr != nil {
				log.Printf("[cron] failure handler error job=%s: %v", name, handlerErr)
			} else if failureNote != "" {
				log.Printf("[cron] %s", failureNote)
			}
		}

		s.handleAlert(
			key,
			"Cron Job",
			name,
			"api="+safeAPIName(apiClient)+" topic="+truncateDetail(topic, 120)+" status="+strconv.Itoa(status)+" error="+err.Error()+" detail="+truncateDetail(detail, 600),
		)
		return
	}

	if ack != nil {
		if err := ack(ctx); err != nil {
			s.handleAlert(key, "Cron Job", name, "ack topic failed: "+err.Error())
			return
		}
	}

	s.handleRecovery(key, "Cron Job", name)
	log.Printf("[cron] success job=%s api=%s topic=%q status=%d", name, safeAPIName(apiClient), topic, status)
}

func (s *Statistics) resolveCronTopic(ctx context.Context, jobName string, job config.CronJobConfig) (string, func(context.Context) error, func(context.Context, error) (string, error), error) {
	source := normalizeKey(job.TopicSource)
	if source == "" || source == "static" {
		return strings.TrimSpace(job.Topic), nil, nil, nil
	}

	if source == "txt" || source == "text" || source == "file" {
		topics, err := loadTopicsFromFile(job.TopicFile)
		if err != nil {
			return "", nil, nil, err
		}
		return s.nextTopicRoundRobin(jobName, topics), nil, nil, nil
	}

	if source == "redis" {
		return s.popTopicFromRedisStream(ctx, job)
	}

	return "", nil, nil, fmt.Errorf("unsupported topic_source=%q", job.TopicSource)
}

func loadTopicsFromFile(path string) ([]string, error) {
	filePath := strings.TrimSpace(path)
	if filePath == "" {
		return nil, fmt.Errorf("topic_file is empty")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read topic_file: %w", err)
	}

	var topics []string
	for _, line := range splitLinesLocal(string(data)) {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		topics = append(topics, t)
	}
	if len(topics) == 0 {
		return nil, fmt.Errorf("topic_file has no usable topics")
	}

	return topics, nil
}

func (s *Statistics) nextTopicRoundRobin(jobName string, topics []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := s.topicIndex[jobName]
	topic := topics[idx%len(topics)]
	s.topicIndex[jobName] = (idx + 1) % len(topics)
	return topic
}

func (s *Statistics) popTopicFromRedisStream(ctx context.Context, job config.CronJobConfig) (string, func(context.Context) error, func(context.Context, error) (string, error), error) {
	addr := strings.TrimSpace(job.RedisAddr)
	if addr == "" {
		addr = "localhost:6379"
	}

	streamKey := strings.TrimSpace(job.RedisTopicStream)
	if streamKey == "" {
		streamKey = strings.TrimSpace(job.RedisTopicList)
	}
	if streamKey == "" {
		return "", nil, nil, fmt.Errorf("redis_topic_stream is empty")
	}

	maxRetries := job.RedisTopicMaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}

	dlqStream := strings.TrimSpace(job.RedisTopicDeadLetterStream)
	if dlqStream == "" {
		dlqStream = streamKey + ":dlq"
	}

	redisKey := addr + "|" + strconv.Itoa(job.RedisDB) + "|" + job.RedisPassword
	client := s.getRedisClient(redisKey, addr, job.RedisPassword, job.RedisDB)
	retryStateKey := streamKey + ":retries"

	waitSeconds := job.RedisTopicWaitSeconds
	if waitSeconds <= 0 {
		waitSeconds = 20
	}

	streams, err := client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamKey, "0"},
		Count:   1,
		Block:   time.Duration(waitSeconds) * time.Second,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", nil, nil, errNoTopicAvailable
		}
		return "", nil, nil, fmt.Errorf("redis xread %q: %w", streamKey, err)
	}
	if len(streams) == 0 || len(streams[0].Messages) == 0 {
		return "", nil, nil, errNoTopicAvailable
	}

	msg := streams[0].Messages[0]
	topic := strings.TrimSpace(fmt.Sprint(msg.Values["topic"]))
	if topic == "" {
		topic = strings.TrimSpace(fmt.Sprint(msg.Values["value"]))
	}
	if topic == "" {
		for _, v := range msg.Values {
			topic = strings.TrimSpace(fmt.Sprint(v))
			if topic != "" {
				break
			}
		}
	}
	if topic == "" {
		return "", nil, nil, fmt.Errorf("redis stream message has empty topic")
	}

	ack := func(ackCtx context.Context) error {
		_, err := client.XDel(ackCtx, streamKey, msg.ID).Result()
		if err != nil {
			return fmt.Errorf("redis xdel %q id=%s: %w", streamKey, msg.ID, err)
		}
		if _, err := client.HDel(ackCtx, retryStateKey, msg.ID).Result(); err != nil {
			return fmt.Errorf("redis hdel %q field=%s: %w", retryStateKey, msg.ID, err)
		}
		return nil
	}

	onFailure := func(failCtx context.Context, triggerErr error) (string, error) {
		retries, err := client.HIncrBy(failCtx, retryStateKey, msg.ID, 1).Result()
		if err != nil {
			return "", fmt.Errorf("redis hincrby %q field=%s: %w", retryStateKey, msg.ID, err)
		}

		if retries < int64(maxRetries) {
			return fmt.Sprintf("cron redis retry scheduled stream=%s msg_id=%s retries=%d/%d", streamKey, msg.ID, retries, maxRetries), nil
		}

		dlqValues := map[string]interface{}{
			"topic":         topic,
			"original_id":   msg.ID,
			"retries":       retries,
			"error":         truncateDetail(triggerErr.Error(), 300),
			"failed_at":     time.Now().Format(time.RFC3339),
			"source_stream": streamKey,
		}
		for k, v := range msg.Values {
			dlqValues["original_"+k] = v
		}

		if _, err := client.XAdd(failCtx, &redis.XAddArgs{
			Stream: dlqStream,
			Values: dlqValues,
		}).Result(); err != nil {
			return "", fmt.Errorf("redis xadd dlq %q: %w", dlqStream, err)
		}

		if _, err := client.XDel(failCtx, streamKey, msg.ID).Result(); err != nil {
			return "", fmt.Errorf("redis xdel %q id=%s after dlq: %w", streamKey, msg.ID, err)
		}

		if _, err := client.HDel(failCtx, retryStateKey, msg.ID).Result(); err != nil {
			return "", fmt.Errorf("redis hdel %q field=%s after dlq: %w", retryStateKey, msg.ID, err)
		}

		return fmt.Sprintf("cron redis moved to dlq stream=%s msg_id=%s retries=%d", dlqStream, msg.ID, retries), nil
	}

	return topic, ack, onFailure, nil
}

func (s *Statistics) getRedisClient(key, addr, password string, db int) *redis.Client {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.redisConns[key]; ok {
		return c
	}

	c := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})
	s.redisConns[key] = c
	return c
}

func splitLinesLocal(s string) []string {
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

func (s *Statistics) findAPIClient(serviceName, apiName string) *client.ApiClient {
	if s.services == nil {
		return nil
	}

	serviceKey := normalizeKey(serviceName)
	apiKey := normalizeKey(apiName)
	for _, srv := range *s.services {
		if serviceKey != "" {
			if srv.ServiceName == nil || normalizeKey(*srv.ServiceName) != serviceKey {
				continue
			}
		}

		for _, c := range srv.ApiClients {
			if c == nil || c.Cfg.Name == nil {
				continue
			}
			if apiKey != "" && normalizeKey(*c.Cfg.Name) == apiKey {
				return c
			}
		}
	}

	return nil
}

func normalizeKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func safeAPIName(c *client.ApiClient) string {
	if c != nil && c.Cfg.Name != nil {
		return *c.Cfg.Name
	}
	return "unknown"
}

func truncateDetail(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(empty)"
	}
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func (s *Statistics) runAll() {
	log.Printf("[scheduler] running checks...")

	var wg sync.WaitGroup

	if s.cfg.Docker.Enabled && s.docker != nil {
		wg.Add(1)
		go s.runSafely("docker-checks", func() {
			defer wg.Done()
			s.runDockerChecks()
		})
	}

	if s.cfg.HealthChecks.Enabled {
		wg.Add(1)
		go s.runSafely("health-checks", func() {
			defer wg.Done()
			s.runHealthChecks()
		})
	}

	if s.cfg.CurlChecks.Enabled {
		wg.Add(1)
		go s.runSafely("curl-checks", func() {
			defer wg.Done()
			s.runCurlChecks()
		})
	}

	if s.cfg.PageChecks.Enabled {
		wg.Add(1)
		go s.runSafely("page-checks", func() {
			defer wg.Done()
			s.runPageChecks()
		})
	}

	wg.Wait()
	log.Printf("[Statistic] checks complete")
}

func (s *Statistics) runSafely(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[scheduler] %s panic recovered: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
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
			healthDetails += fmt.Sprintf("\n  - %s : OK!", ep.Name)
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
			newAlert := s.handleAlert(key, "Page Availability", result.Name, result.Detail)
			if newAlert {
				s.runPageRecoveryAsync(pg)
			}
		} else {
			s.handleRecovery(key, "Page Availability", result.Name)
		}
	}
}

func (s *Statistics) runPageRecoveryAsync(pg config.PageCheck) {
	command := strings.TrimSpace(pg.RecoveryCommand)
	if command == "" {
		return
	}

	timeoutSeconds := pg.RecoveryTimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		defer cancel()

		shell := "sh"
		shellArg := "-c"
		if runtime.GOOS == "windows" {
			shell = "cmd"
			shellArg = "/C"
		}

		cmd := exec.CommandContext(ctx, shell, shellArg, command)
		output, err := cmd.CombinedOutput()
		if err != nil {
			if len(output) > 0 {
				log.Printf("[page-recovery] %s failed: %v | output: %s", pg.Name, err, strings.TrimSpace(string(output)))
				return
			}
			log.Printf("[page-recovery] %s failed: %v", pg.Name, err)
			return
		}

		if len(output) > 0 {
			log.Printf("[page-recovery] %s command succeeded: %s", pg.Name, strings.TrimSpace(string(output)))
			return
		}

		log.Printf("[page-recovery] %s command succeeded", pg.Name)
	}()
}

// --- Alert deduplication ---

// handleAlert sends an alert only if this check was previously OK (or first time)
func (s *Statistics) handleAlert(key, checkType, name, detail string) bool {
	log.Printf("[alert] %s | %s | %s", checkType, name, detail)
	s.mu.Lock()
	wasAlerting := s.alertState[key]
	s.alertState[key] = true
	s.mu.Unlock()

	if !wasAlerting {
		if err := s.notifier.SendAlert(checkType, name, detail); err != nil {
			log.Printf("[telegram] send alert error: %v", err)
		}
		return true
	}

	return false
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
