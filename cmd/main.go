package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"admin-svc/internal/client"
	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	telegrampkg "admin-svc/internal/infrastructure/telegram"
	"admin-svc/internal/service"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	envPath := flag.String("env", ".env", "path to .env file")
	flag.Parse()

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

	statistics := service.New(cfg, notifier, dockerChecker)

	_, cancelCommands := startTelegramBotCommands(notifier, statistics, services, dockerChecker)
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

func startTelegramBotCommands(notifier *telegrampkg.Notifier, statistic *service.Statistics, services *[]client.Service, dockerChecker *docker.Checker) (context.Context, context.CancelFunc) {
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

			output, err := executeShellCommand(execCtx, command)
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

func triggerApiClient(ctx context.Context, c *client.ApiClient, input string) (string, error) {
	status, detail, err := c.Trigger(ctx, input)
	if err != nil {
		return "", err
	}
	return *c.Cfg.Name + " triggered successfully\nHTTP status: " + strconv.Itoa(status) + "\nResponse:\n" + detail, nil
}

func executeShellCommand(ctx context.Context, command string) (string, error) {
	parts := parseExecCommandParts(command)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
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
