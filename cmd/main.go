package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"admin-svc/internal/config"
	"admin-svc/internal/docker"
	"admin-svc/internal/scheduler"
	"admin-svc/internal/telegram"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	envPath    := flag.String("env", ".env", "path to .env file")
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

	notifier := telegram.New(cfg.Telegram.BotToken, cfg.Telegram.ChatID)

	var dockerChecker *docker.Checker
	if cfg.Docker.Enabled {
		dockerChecker, err = docker.New()
		if err != nil {
			log.Printf("[main] docker unavailable, disabling: %v", err)
			cfg.Docker.Enabled = false
		}
	}

	sched := scheduler.New(cfg, notifier, dockerChecker)

	checks := collectEnabledChecks(cfg)
	if err := notifier.SendStartup(checks); err != nil {
		log.Printf("[main] startup notification error: %v", err)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go sched.Start()

	sig := <-quit
	log.Printf("[main] received signal %s, shutting down", sig)
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
