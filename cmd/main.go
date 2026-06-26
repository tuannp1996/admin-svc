package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"admin-svc/internal/blog"
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
	blogClient := blog.New(cfg.BlogGen)

	var dockerChecker *docker.Checker
	if cfg.Docker.Enabled {
		dockerChecker, err = docker.New()
		if err != nil {
			log.Printf("[main] docker unavailable, disabling: %v", err)
			cfg.Docker.Enabled = false
		}
	}

	statistics := service.New(cfg, notifier, dockerChecker)

	_, cancelCommands := startTelegramBotCommands(notifier, statistics, blogClient)
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

func startTelegramBotCommands(notifier *telegrampkg.Notifier, statistic *service.Statistics, blogClient *blog.Client) (context.Context, context.CancelFunc) {
	commandCtx, cancelCommands := context.WithCancel(context.Background())

	go notifier.StartCommandListener(commandCtx, map[string]telegrampkg.CommandHandler{
		"/status": func(ctx context.Context, input string) (string, error) {
			return statistic.StatusSummary(), nil
		},
		"/restart": func(ctx context.Context, input string) (string, error) {
			go func() {
				time.Sleep(2 * time.Second)
				os.Exit(0)
			}()
			return "Restarting admin-svc...", nil
		},
		"/blog_gen": func(ctx context.Context, input string) (string, error) {
			return triggerBlog(ctx, blogClient, input)
		},
		"/gen_blog": func(ctx context.Context, input string) (string, error) {
			return triggerBlog(ctx, blogClient, input)
		},
	})

	return commandCtx, cancelCommands
}

func triggerBlog(ctx context.Context, blogClient *blog.Client, input string) (string, error) {
	status, detail, err := blogClient.Trigger(ctx, input)
	if err != nil {
		return "", err
	}
	return "auto_blog triggered successfully\nHTTP status: " + strconv.Itoa(status) + "\nResponse: " + detail, nil
}
