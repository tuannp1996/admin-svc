package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"admin-svc/internal/client"
	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	telegrampkg "admin-svc/internal/infrastructure/telegram"
	"admin-svc/internal/service"
	"github.com/redis/go-redis/v9"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	envPath := flag.String("env", ".env", "path to .env file")
	flag.Parse()

	setTimeZoneUTCPlus7()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// Load .env first so ${VAR} in config.yaml get expanded
	if err := config.LoadEnv(*envPath); err != nil {
		log.Fatalf("[main] .env: %v", err)
	}
	log.Println("[main] loaded env from", *envPath)

	log.Println("[main] loading config:", *configPath)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("[main] config: %v", err)
	}

	notifier := telegrampkg.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
	services := client.New(cfg.Clients)

	var dockerChecker *docker.Checker
	if cfg.Docker.Enabled {
		dockerChecker, err = docker.New()
		if err != nil {
			log.Printf("[main] docker unavailable, disabling: %v", err)
			cfg.Docker.Enabled = false
		}
	}

	statistics := service.New(cfg, notifier, dockerChecker, services)

	_, cancelCommands := startTelegramBotCommands(cfg, notifier, statistics, services, dockerChecker)
	defer cancelCommands()

	checks := collectEnabledChecks(cfg)
	if err := notifier.SendStartup(checks); err != nil {
		log.Printf("[main] startup notification error: %v", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go statistics.Start()

	sig := <-quit
	log.Printf("[main] received signal %s, shutting down", sig)
	cancelCommands()
	if dockerChecker != nil {
		dockerChecker.Close()
	}
}

func setTimeZoneUTCPlus7() {
	// Prefer IANA location for proper timezone metadata, fallback to fixed UTC+7 offset.
	loc, err := time.LoadLocation("Asia/Ho_Chi_Minh")
	if err != nil {
		time.Local = time.FixedZone("UTC+7", 7*60*60)
		log.Printf("[main] timezone set to UTC+7 (fixed): %v", err)
		return
	}

	time.Local = loc
	log.Printf("[main] timezone set to %s", loc)
}

func collectEnabledChecks(cfg *config.Config) []string {
	var list []string
	if cfg.Docker.Enabled {
		for _, c := range cfg.Docker.Containers {
			list = append(list, "🐳 Docker: "+c.Name)
		}
	}
	if cfg.HealthChecks.Enabled {
		for _, e := range cfg.HealthChecks.Endpoints {
			list = append(list, "🏥 Health: "+e.Name)
		}
	}
	if cfg.CurlChecks.Enabled {
		for _, r := range cfg.CurlChecks.Requests {
			list = append(list, "🔁 API: "+r.Name)
		}
	}
	if cfg.PageChecks.Enabled {
		for _, p := range cfg.PageChecks.Pages {
			list = append(list, "🌐 Page: "+p.Name)
		}
	}
	return list
}

func startTelegramBotCommands(cfg *config.Config, notifier *telegrampkg.Notifier, statistic *service.Statistics, services *[]client.Service, dockerChecker *docker.Checker) (context.Context, context.CancelFunc) {
	commandCtx, cancelCommands := context.WithCancel(context.Background())

	allowedExecCommands := map[string]struct{}{
		"docker":    {},
		"pm2":       {},
		"systemctl": {},
		"curl":      {},
		"history":   {},
		"ls":        {},
		"cat":       {},
		"tail":      {},
		"head":      {},
	}

	go notifier.StartCommandListener(commandCtx, map[string]telegrampkg.CommandHandler{
		"/status": func(ctx context.Context, input string) (string, error) {
			return statistic.StatusSummary(), nil
		},
		"/restart": func(ctx context.Context, input string) (string, error) {
			if dockerChecker == nil {
				return "Docker integration is disabled", nil
			}

			containerName := strings.TrimSpace(input)
			if containerName == "" {
				return "Usage: /restart <container_name>", nil
			}

			restartCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			if err := dockerChecker.Restart(restartCtx, containerName); err != nil {
				return "", err
			}

			return "Container restarted: " + containerName, nil
		},
		"/blog_gen": func(ctx context.Context, input string) (string, error) {
			var blogClient *client.ApiClient
			for _, s := range *services {
				for _, c := range s.ApiClients {
					if c.Cfg.Name != nil && *c.Cfg.Name == "BLOG Gen Article" && c.Cfg.Enabled {
						blogClient = c
						break
					}
				}
			}
			return triggerApiClient(ctx, blogClient, input)
		},
		"/blog_topic": func(ctx context.Context, input string) (string, error) {
			topics := parseTopicsInput(input)
			if len(topics) == 0 {
				return "Usage: /blog_topic <topic1> <topic2> ... or /blog_topic \"topic with spaces\" \"another topic\"", nil
			}

			addr, password, db, streamKey, err := resolveBlogTopicRedisConfig(cfg)
			if err != nil {
				return "", err
			}

			redisClient := redis.NewClient(&redis.Options{
				Addr:     addr,
				Password: password,
				DB:       db,
			})
			defer redisClient.Close()

			published := 0
			lastID := ""
			for _, topic := range topics {
				id, err := redisClient.XAdd(ctx, &redis.XAddArgs{
					Stream: streamKey,
					Values: map[string]interface{}{
						"topic":      topic,
						"source":     "telegram",
						"publishedAt": time.Now().Format(time.RFC3339),
					},
				}).Result()
				if err != nil {
					return "", fmt.Errorf("redis xadd %q: %w", streamKey, err)
				}
				published++
				lastID = id
			}

			return fmt.Sprintf("Published %d topic(s) to redis stream %s. Last message ID: %s", published, streamKey, lastID), nil
		},
		"/tik_users": func(ctx context.Context, input string) (string, error) {
			var tikClient *client.ApiClient
			for _, s := range *services {
				for _, c := range s.ApiClients {
					if c.Cfg.Name != nil && *c.Cfg.Name == "TIKTOK Get Users" && c.Cfg.Enabled {
						tikClient = c
						break
					}
				}
			}
			return triggerApiClient(ctx, tikClient, "")
		},
		"/exec": func(ctx context.Context, input string) (string, error) {
			command := normalizeExecInput(input)
			if command == "" {
				return "Usage: /exec <command>\nAllowed: docker, pm2, systemctl, curl, history, ls, cat, tail, head", nil
			}

			if err := validateExecCommand(command, allowedExecCommands); err != nil {
				return "", err
			}

			execCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			output, err := executeShellCommand(execCtx, command, dockerChecker)
			if err != nil {
				if strings.TrimSpace(output) == "" {
					return "", err
				}
				return "Command failed:\n" + output, err
			}

			if strings.TrimSpace(output) == "" {
				return "Command completed (no output)", nil
			}

			return "Output:\n" + output, nil
		},
	})

	return commandCtx, cancelCommands
}

func parseTopicsInput(input string) []string {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}

	tokens := tokenizeQuoted(input)
	if len(tokens) == 0 {
		return nil
	}

	topics := make([]string, 0, len(tokens))
	for _, token := range tokens {
		topic := strings.TrimSpace(token)
		if topic != "" {
			topics = append(topics, topic)
		}
	}

	return topics
}

func tokenizeQuoted(s string) []string {
	var tokens []string
	var current strings.Builder

	inQuote := false
	quoteChar := byte(0)
	escaped := false

	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}

		if ch == '\\' {
			escaped = true
			continue
		}

		if inQuote {
			if ch == quoteChar {
				inQuote = false
				quoteChar = 0
				continue
			}
			current.WriteByte(ch)
			continue
		}

		if ch == '"' || ch == '\'' {
			inQuote = true
			quoteChar = ch
			continue
		}

		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' {
			flush()
			continue
		}

		current.WriteByte(ch)
	}

	flush()
	return tokens
}

func resolveBlogTopicRedisConfig(cfg *config.Config) (addr, password string, db int, streamKey string, err error) {
	if cfg == nil {
		return "", "", 0, "", fmt.Errorf("config is nil")
	}

	for _, job := range cfg.Scheduler.Jobs {
		if normalizeKeyMain(job.TopicSource) != "redis" {
			continue
		}

		apiName := normalizeKeyMain(job.API)
		jobName := normalizeKeyMain(job.Name)
		if apiName == "bloggenarticle" || jobName == "bloggenredis" || strings.Contains(jobName, "blog") {
			addr = strings.TrimSpace(job.RedisAddr)
			if addr == "" {
				addr = "localhost:6379"
			}
			password = job.RedisPassword
			db = job.RedisDB
			streamKey = strings.TrimSpace(job.RedisTopicStream)
			if streamKey == "" {
				streamKey = strings.TrimSpace(job.RedisTopicList)
			}
			if streamKey == "" {
				return "", "", 0, "", fmt.Errorf("redis_topic_stream is empty in scheduler redis job")
			}
			return addr, password, db, streamKey, nil
		}
	}

	return "", "", 0, "", fmt.Errorf("no scheduler redis job found for blog topics; configure scheduler.jobs with topic_source=redis")
}

func normalizeKeyMain(s string) string {
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

func triggerApiClient(ctx context.Context, c *client.ApiClient, input string) (string, error) {
	status, detail, err := c.Trigger(ctx, input)
	if err != nil {
		return "", err
	}
	return *c.Cfg.Name + " triggered successfully\nHTTP status: " + strconv.Itoa(status) + "\nResponse:\n" + detail, nil
}

func executeShellCommand(ctx context.Context, command string, dockerChecker *docker.Checker) (string, error) {
	parts := parseExecCommandParts(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}

	if parts[0] == "history" {
		return readShellHistory(parts[1:])
	}

	if parts[0] == "docker" {
		if _, err := exec.LookPath("docker"); err != nil {
			return executeDockerFallback(ctx, parts[1:], dockerChecker)
		}
	}

	if parts[0] == "systemctl" {
		return "", fmt.Errorf("systemctl is not available in this container runtime; use /exec docker restart <container_name> or run admin-svc on VPS host")
	}

	if _, err := exec.LookPath(parts[0]); err != nil {
		return "", fmt.Errorf("command %q is allowlisted but not installed in admin-svc runtime", parts[0])
	}

	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	output, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(output))

	const maxLen = 3000
	if len(out) > maxLen {
		out = out[:maxLen] + "\n...(truncated)"
	}

	return out, err
}

func executeDockerFallback(ctx context.Context, args []string, dockerChecker *docker.Checker) (string, error) {
	if dockerChecker == nil {
		return "", fmt.Errorf("docker CLI is unavailable and docker integration is disabled")
	}

	if len(args) == 0 || args[0] == "ps" {
		statuses, err := dockerChecker.ListAll(ctx)
		if err != nil {
			return "", err
		}
		if len(statuses) == 0 {
			return "No containers found", nil
		}

		var b strings.Builder
		b.WriteString("NAME\tSTATE\tRUNNING")
		for _, s := range statuses {
			b.WriteString("\n")
			b.WriteString(s.Name)
			b.WriteString("\t")
			b.WriteString(s.State)
			b.WriteString("\t")
			if s.Running {
				b.WriteString("yes")
			} else {
				b.WriteString("no")
			}
		}
		return b.String(), nil
	}

	if args[0] == "restart" {
		if len(args) < 2 {
			return "", fmt.Errorf("usage: /exec docker restart <container_name>")
		}
		if err := dockerChecker.Restart(ctx, args[1]); err != nil {
			return "", err
		}
		return "Container restarted: " + args[1], nil
	}

	return "", fmt.Errorf("docker CLI not found; supported fallback commands: docker ps, docker restart <container_name>")
}

func readShellHistory(args []string) (string, error) {
	limit := 50
	if len(args) > 0 {
		n, err := strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			return "", fmt.Errorf("history accepts one optional positive number, e.g. /exec history 20")
		}
		limit = n
	}

	var candidates []string
	if histFile := strings.TrimSpace(os.Getenv("HISTFILE")); histFile != "" {
		candidates = append(candidates, histFile)
	}

	if homeDir, err := os.UserHomeDir(); err == nil && homeDir != "" {
		candidates = append(candidates,
			filepath.Join(homeDir, ".bash_history"),
			filepath.Join(homeDir, ".zsh_history"),
			filepath.Join(homeDir, ".ash_history"),
		)
	}

	candidates = append(candidates, "/root/.bash_history")

	var lines []string
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		rawLines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		for _, line := range rawLines {
			line = strings.TrimSpace(line)
			if line != "" {
				lines = append(lines, line)
			}
		}

		if len(lines) > 0 {
			break
		}
	}

	if len(lines) == 0 {
		return "No history entries found", nil
	}

	if limit > len(lines) {
		limit = len(lines)
	}

	return strings.Join(lines[len(lines)-limit:], "\n"), nil
}

func validateExecCommand(command string, allowed map[string]struct{}) error {
	parts := parseExecCommandParts(command)
	if len(parts) == 0 {
		return fmt.Errorf("empty command")
	}

	baseCommand := parts[0]
	if _, ok := allowed[baseCommand]; !ok {
		return fmt.Errorf("command %q is not allowed", baseCommand)
	}

	return nil
}

func parseExecCommandParts(command string) []string {
	command = strings.TrimSpace(command)
	if len(command) >= 2 {
		if (command[0] == '"' && command[len(command)-1] == '"') ||
			(command[0] == '\'' && command[len(command)-1] == '\'') {
			command = strings.TrimSpace(command[1 : len(command)-1])
		}
	}
	return strings.Fields(command)
}

func normalizeExecInput(input string) string {
	command := strings.TrimSpace(input)
	if len(command) < 2 {
		return command
	}

	first := command[0]
	last := command[len(command)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return strings.TrimSpace(command[1 : len(command)-1])
	}

	return command
}
